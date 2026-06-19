package api

import (
	"context"
	"encoding/json"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"sb-ui/internal/executor"
	"sb-ui/internal/jobs"
	"sb-ui/internal/rclone"
	"sb-ui/internal/store"
)

// Smart Uploader (cloudplow++): watch a local staging folder; when it grows past
// a threshold, move its contents up to a cloud remote — spreading uploads across
// several remotes with per-remote daily caps + cooldowns to dodge quotas/bans.
// Built on runTransfer (rclone move), so it inherits flags/progress/stop.

type uploaderRemote struct {
	TaskID    string `json:"task_id"`   // run this saved transfer Task (reuses its op/items/dst/flags); "" = raw move below
	Name      string `json:"name"`      // ledger key + display label (rclone remote name, or task label)
	Dest      string `json:"dest"`      // raw mode: path within the remote ("" = root)
	CapPerDay string `json:"cap"`       // bytes/24h, "" = unlimited (gdrive 750G); teldrive often blank
	CapFiles  int    `json:"cap_files"` // files/24h, 0 = unlimited (teldrive rate/ban dimension)
	GapMin    int    `json:"gap_min"`   // min minutes between uses of this remote
	Bwlimit   string `json:"bwlimit"`   // raw mode bandwidth, e.g. "40M"
	Tpslimit  int    `json:"tpslimit"`  // raw mode teldrive ban-avoidance
}

// parseCapBytes reads a per-day byte cap. A bare number is treated as GB (the UI
// labels the field "Cap GB / day"); a unit-suffixed value (700G, 2T) parses as-is.
// Empty → 0 (unlimited).
func parseCapBytes(s string) int64 {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0
	}
	if c := s[len(s)-1]; c >= '0' && c <= '9' {
		s += "G"
	}
	return int64(parseSize(s))
}

// remoteKey is the ledger key for a destination: tasks are tracked by ID (a task
// may target the same remote as a raw entry), raw entries by remote name.
func remoteKey(r uploaderRemote) string {
	if r.TaskID != "" {
		return "task:" + r.TaskID
	}
	return r.Name
}

type uploaderConfig struct {
	Enabled         bool             `json:"enabled"`
	Source          string           `json:"source"`           // local staging path, e.g. /mnt/local/Media
	Threshold       string           `json:"threshold"`        // upload once source ≥ this size (e.g. "500G")
	Strategy        string           `json:"strategy"`         // lru | round_robin | most_free
	IntervalMinutes int              `json:"interval_minutes"` // how often to check (min 1)
	AllowedFrom     string           `json:"allowed_from"`     // HH:MM, "" = anytime (off-peak window)
	AllowedUntil    string           `json:"allowed_until"`    // HH:MM
	MinAge          string           `json:"min_age"`          // skip files newer than this (e.g. "15m") → don't upload in-progress
	DeleteEmptySrc  bool             `json:"delete_empty_src"` // tidy staging after move
	Excludes        []string         `json:"excludes"`         // rclone --exclude patterns
	Remotes         []uploaderRemote `json:"remotes"`
}

type ledgerEvent struct {
	At    time.Time `json:"at"`
	Bytes int64     `json:"bytes"`
	Files int       `json:"files"`
}
type ledgerRemote struct {
	Events      []ledgerEvent `json:"events"`
	LastUpload  time.Time     `json:"last_upload"`
	PausedUntil time.Time     `json:"paused_until,omitempty"` // set on FLOOD_WAIT/429 — skip until elapsed
}

const (
	uploaderCfgRel     = "cache/uploader.json"
	uploaderLedgerRel  = "cache/uploader_ledger.json"
	uploaderWindow     = 24 * time.Hour
	uploaderFloodPause = 60 * time.Minute // cooldown after a rate-limit/ban hit
)

var (
	upMu       sync.Mutex
	ucfg       uploaderConfig
	ledger     = map[string]*ledgerRemote{}
	upLoaded   bool
	upLastSize int64
	upLastAt   time.Time
	upLastMsg  string
	rrIndex    int
	upOnce     sync.Once
)

func ensureUploader() { // under upMu
	if upLoaded {
		return
	}
	store.ReadJSON(uploaderCfgRel, &ucfg)
	store.ReadJSON(uploaderLedgerRel, &ledger)
	if ledger == nil {
		ledger = map[string]*ledgerRemote{}
	}
	if ucfg.IntervalMinutes <= 0 {
		ucfg.IntervalMinutes = 15
	}
	if ucfg.Strategy == "" {
		ucfg.Strategy = "lru"
	}
	upLoaded = true
}

func usedInWindow(led map[string]*ledgerRemote, name string, now time.Time) int64 {
	lr := led[name]
	if lr == nil {
		return 0
	}
	var sum int64
	for _, e := range lr.Events {
		if now.Sub(e.At) < uploaderWindow {
			sum += e.Bytes
		}
	}
	return sum
}

func usedFilesInWindow(led map[string]*ledgerRemote, name string, now time.Time) int {
	lr := led[name]
	if lr == nil {
		return 0
	}
	var sum int
	for _, e := range lr.Events {
		if now.Sub(e.At) < uploaderWindow {
			sum += e.Files
		}
	}
	return sum
}

// ledgerAdd prunes the 24h window then appends an upload event (shared by the live
// ledger and the dry-run simulator so both account identically).
func ledgerAdd(led map[string]*ledgerRemote, name string, bytes int64, files int, now time.Time) {
	lr := led[name]
	if lr == nil {
		lr = &ledgerRemote{}
		led[name] = lr
	}
	kept := lr.Events[:0]
	for _, e := range lr.Events {
		if now.Sub(e.At) < uploaderWindow {
			kept = append(kept, e)
		}
	}
	lr.Events = append(kept, ledgerEvent{At: now, Bytes: bytes, Files: files})
	lr.LastUpload = now
}

func recordUpload(name string, bytes int64, files int, now time.Time) {
	ledgerAdd(ledger, name, bytes, files, now)
	store.WriteJSON(uploaderLedgerRel, ledger)
}

// pauseRemote benches a remote after a rate-limit/ban hit so the picker skips it.
func pauseRemote(name string, until time.Time) {
	lr := ledger[name]
	if lr == nil {
		lr = &ledgerRemote{}
		ledger[name] = lr
	}
	lr.PausedUntil = until
	store.WriteJSON(uploaderLedgerRel, ledger)
}

// pickRemote chooses an eligible remote from the live config/ledger.
func pickRemote(now time.Time) (*uploaderRemote, int64) {
	r, free, _ := selectRemote(ucfg.Remotes, ledger, ucfg.Strategy, &rrIndex, now)
	return r, free
}

// selectRemote is the pure remote-picker: given a set of remotes, a ledger, the
// strategy and a round-robin cursor, it returns the chosen remote + remaining cap
// bytes (-1 = unlimited), or (nil, 0, reason) when none is eligible. Shared by the
// live uploader and the dry-run simulator so both behave identically.
func selectRemote(remotes []uploaderRemote, led map[string]*ledgerRemote, strategy string, rr *int, now time.Time) (*uploaderRemote, int64, string) {
	type cand struct {
		r    uploaderRemote
		free int64
	}
	var cands []cand
	reason := "no remotes configured"
	for _, r := range remotes {
		if r.Name == "" && r.TaskID == "" {
			continue
		}
		key := remoteKey(r)
		if lr := led[key]; lr != nil && now.Before(lr.PausedUntil) {
			reason = "all remotes cooling down (rate-limit pause)"
			continue // benched after a flood/429 hit
		}
		if r.GapMin > 0 {
			if lr := led[key]; lr != nil && now.Sub(lr.LastUpload) < time.Duration(r.GapMin)*time.Minute {
				reason = "all remotes within gap cooldown"
				continue
			}
		}
		if r.CapFiles > 0 && usedFilesInWindow(led, key, now) >= r.CapFiles {
			reason = "all remotes hit their daily caps"
			continue // hit the daily file/request budget (teldrive rate dimension)
		}
		used := usedInWindow(led, key, now)
		free := int64(-1)
		if capB := parseCapBytes(r.CapPerDay); capB > 0 {
			if used >= capB {
				reason = "all remotes hit their daily caps"
				continue
			}
			free = capB - used
		}
		cands = append(cands, cand{r, free})
	}
	if len(cands) == 0 {
		return nil, 0, reason
	}
	switch strategy {
	case "most_free":
		sort.SliceStable(cands, func(i, j int) bool {
			fi, fj := cands[i].free, cands[j].free
			if fi == -1 {
				return true
			}
			if fj == -1 {
				return false
			}
			return fi > fj
		})
		return &cands[0].r, cands[0].free, ""
	case "round_robin":
		*rr = (*rr + 1) % len(cands)
		return &cands[*rr].r, cands[*rr].free, ""
	default: // lru — least recently used
		sort.SliceStable(cands, func(i, j int) bool {
			li, lj := led[remoteKey(cands[i].r)], led[remoteKey(cands[j].r)]
			var ti, tj time.Time
			if li != nil {
				ti = li.LastUpload
			}
			if lj != nil {
				tj = lj.LastUpload
			}
			return ti.Before(tj)
		})
		return &cands[0].r, cands[0].free, ""
	}
}

// inWindow reports whether now falls in [from,until) (HH:MM); handles overnight
// windows (e.g. 22:00–06:00). Empty bounds = always allowed.
func inWindow(from, until string, now time.Time) bool {
	f, u := hm(from), hm(until)
	if f < 0 || u < 0 {
		return true
	}
	cur := now.Hour()*60 + now.Minute()
	if f <= u {
		return cur >= f && cur < u
	}
	return cur >= f || cur < u
}

func hm(s string) int {
	p := strings.SplitN(strings.TrimSpace(s), ":", 2)
	if len(p) != 2 {
		return -1
	}
	h, e1 := strconv.Atoi(p[0])
	m, e2 := strconv.Atoi(p[1])
	if e1 != nil || e2 != nil {
		return -1
	}
	return h*60 + m
}

func duBytes(path string) int64 {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	rc, out, _ := executor.Get().Run(ctx, []string{"du", "-sb", "--", path}, "")
	if rc != 0 {
		return 0
	}
	if f := strings.Fields(out); len(f) > 0 {
		n, _ := strconv.ParseInt(f[0], 10, 64)
		return n
	}
	return 0
}

// Seams (overridden in tests to drive the full cycle without rclone/du).
var (
	measureSource = duBytes

	// uploadRunner performs the move and reports what moved + whether the run hit a
	// provider rate-limit (FLOOD_WAIT/429), so the cycle can pause that remote.
	uploadRunner = func(label, taskID, op string, items []transferItem, dst string, opts transferOpts) (int64, int, bool) {
		j := jobs.Create(label, op)
		runTransfer(j.ID, taskID, op, items, dst, false, opts)
		var moved int64
		var files int
		statsMu.Lock()
		if s := statsStore[j.ID]; s != nil {
			moved, files = s.Bytes, s.Transfers
		}
		statsMu.Unlock()
		return moved, files, floodHit(j.ID)
	}
)

// uploaderCheck runs one cycle: measure the source, and if it's over threshold,
// move it to the next eligible remote (blocking — uploads run one at a time).
func uploaderCheck() {
	upMu.Lock()
	ensureUploader()
	cfg := ucfg
	upMu.Unlock()

	if !cfg.Enabled || cfg.Source == "" {
		return
	}
	now := time.Now()
	if !inWindow(cfg.AllowedFrom, cfg.AllowedUntil, now) {
		upMu.Lock()
		upLastAt, upLastMsg = now, "outside upload window"
		upMu.Unlock()
		return
	}
	// Plex throttle: hold off while people are streaming (cloudplow-style).
	opt := loadOptions()
	if opt.Plex.Throttle && opt.Plex.URL != "" {
		if n := plexActiveStreams(opt.Plex); n >= 0 && n >= opt.Plex.MaxStreams {
			upMu.Lock()
			upLastAt, upLastMsg = time.Now(), "paused: "+strconv.Itoa(n)+" Plex stream(s) active"
			upMu.Unlock()
			return
		}
	}

	size := measureSource(cfg.Source)
	thr := int64(parseSize(cfg.Threshold))

	upMu.Lock()
	upLastSize, upLastAt = size, time.Now()
	if thr > 0 && size < thr {
		upLastMsg = "below threshold"
		upMu.Unlock()
		return
	}
	r, free := pickRemote(time.Now())
	if r == nil {
		upLastMsg = "no eligible remote (caps/cooldowns)"
		upMu.Unlock()
		return
	}
	upLastMsg = "uploading via " + r.Name
	upMu.Unlock()

	// Resolve what to run: a saved Task (reuses its op/items/dst/flags so we don't
	// re-implement transfer config here) or a raw move of the staging source.
	op := "move"
	items := []transferItem{{Path: cfg.Source, IsDir: true}}
	dst := r.Name + ":" + strings.TrimPrefix(r.Dest, "/")
	var opts transferOpts
	if r.TaskID != "" {
		t, ok := findTask(r.TaskID)
		if !ok {
			upMu.Lock()
			upLastMsg = "task not found for " + r.Name
			upMu.Unlock()
			return
		}
		op, items, dst, opts = t.Op, t.Items, t.Dst, t.Opts
	} else {
		opts = transferOpts{Bwlimit: r.Bwlimit, Tpslimit: r.Tpslimit}
	}
	// Layer the uploader's safety knobs on top of the task/raw options.
	opts.Exclude = append(append([]string{}, opts.Exclude...), cfg.Excludes...)
	opts.Extra = append(opts.Extra, extraFlag{Flag: "--cutoff-mode", Value: "cautious"})
	if free > 0 { // cap the run to the remaining daily allowance (whole files only)
		opts.Extra = append(opts.Extra, extraFlag{Flag: "--max-transfer", Value: strconv.FormatInt(free, 10)})
	}
	if cfg.MinAge != "" { // skip files still being written/downloaded
		opts.Extra = append(opts.Extra, extraFlag{Flag: "--min-age", Value: cfg.MinAge})
	}
	if cfg.DeleteEmptySrc {
		opts.Extra = append(opts.Extra, extraFlag{Flag: "--delete-empty-src-dirs", Value: ""})
	}
	moved, files, flood := uploadRunner("uploader: "+transferLabel(op, items, dst), r.TaskID, op, items, dst, opts)

	now = time.Now()
	upMu.Lock()
	recordUpload(remoteKey(*r), moved, files, now)
	if flood { // rate-limited/banned — bench this remote so the next cycle picks another
		pauseRemote(remoteKey(*r), now.Add(uploaderFloodPause))
		upLastMsg = "rate-limited on " + r.Name + " — paused " + uploaderFloodPause.String() + " (moved " + humanBytes(moved) + ")"
	} else {
		upLastMsg = "uploaded " + humanBytes(moved) + " / " + strconv.Itoa(files) + " files via " + r.Name
	}
	upMu.Unlock()

	// Refresh Plex libraries after a successful upload (replaces autoscan).
	if !flood && moved > 0 && opt.Plex.ScanAfterUpload && opt.Plex.URL != "" {
		go plexRefreshAll(opt.Plex)
	}
}

func startUploader() {
	upOnce.Do(func() {
		go func() {
			for {
				upMu.Lock()
				ensureUploader()
				iv := ucfg.IntervalMinutes
				upMu.Unlock()
				if iv <= 0 {
					iv = 15
				}
				time.Sleep(time.Duration(iv) * time.Minute)
				uploaderCheck()
			}
		}()
	})
}

// ── endpoints ─────────────────────────────────────────────────────────────────

func getUploader(w http.ResponseWriter, _ *http.Request) {
	upMu.Lock()
	ensureUploader()
	cfg := ucfg
	upMu.Unlock()
	if cfg.Remotes == nil {
		cfg.Remotes = []uploaderRemote{}
	}
	writeJSON(w, http.StatusOK, cfg)
}

func putUploader(w http.ResponseWriter, req *http.Request) {
	var c uploaderConfig
	if json.NewDecoder(req.Body).Decode(&c) != nil {
		http.Error(w, "bad config", http.StatusBadRequest)
		return
	}
	if c.IntervalMinutes <= 0 {
		c.IntervalMinutes = 15
	}
	if c.Strategy == "" {
		c.Strategy = "lru"
	}
	upMu.Lock()
	ensureUploader()
	ucfg = c
	store.WriteJSON(uploaderCfgRel, ucfg)
	upMu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func uploaderStatus(w http.ResponseWriter, _ *http.Request) {
	upMu.Lock()
	ensureUploader()
	now := time.Now()
	remotes := make([]map[string]any, 0, len(ucfg.Remotes))
	for _, r := range ucfg.Remotes {
		key := remoteKey(r)
		used := usedInWindow(ledger, key, now)
		var last, paused any
		if lr := ledger[key]; lr != nil {
			if !lr.LastUpload.IsZero() {
				last = lr.LastUpload.UTC().Format(time.RFC3339)
			}
			if now.Before(lr.PausedUntil) {
				paused = lr.PausedUntil.UTC().Format(time.RFC3339)
			}
		}
		remotes = append(remotes, map[string]any{
			"name": r.Name, "task_id": r.TaskID, "cap": r.CapPerDay, "used_today": humanBytes(used),
			"used_bytes": used, "cap_files": r.CapFiles, "files_today": usedFilesInWindow(ledger, key, now),
			"last_upload": last, "paused_until": paused,
		})
	}
	resp := map[string]any{
		"enabled": ucfg.Enabled, "source": ucfg.Source, "threshold": ucfg.Threshold,
		"last_size": humanBytes(upLastSize), "last_size_bytes": upLastSize,
		"last_check": nil, "message": upLastMsg, "remotes": remotes,
	}
	if !upLastAt.IsZero() {
		resp["last_check"] = upLastAt.UTC().Format(time.RFC3339)
	}
	upMu.Unlock()
	writeJSON(w, http.StatusOK, resp)
}

func uploaderRun(w http.ResponseWriter, _ *http.Request) {
	go uploaderCheck()
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// nextWindowOpen returns the next time the upload window opens at/after now (now
// itself if no window is configured or we're already inside it).
func nextWindowOpen(from, until string, now time.Time) time.Time {
	f := hm(from)
	if f < 0 || inWindow(from, until, now) {
		return now
	}
	y, m, d := now.Date()
	t := time.Date(y, m, d, f/60, f%60, 0, 0, now.Location())
	if !t.After(now) {
		t = t.Add(24 * time.Hour)
	}
	return t
}

// nextEligible returns the soonest time some remote regains capacity (a 24h window
// slot frees up) or comes off a rate-limit pause, so the drain can skip the idle
// gap in one hop instead of stepping through every check interval.
func nextEligible(cfg uploaderConfig, led map[string]*ledgerRemote, now time.Time) time.Time {
	best := time.Time{}
	consider := func(t time.Time) {
		if t.After(now) && (best.IsZero() || t.Before(best)) {
			best = t
		}
	}
	for _, r := range cfg.Remotes {
		if r.Name == "" && r.TaskID == "" {
			continue
		}
		key := remoteKey(r)
		lr := led[key]
		if lr == nil {
			return now // a fresh remote is eligible right now
		}
		t := now
		if now.Before(lr.PausedUntil) {
			t = lr.PausedUntil
		}
		capped := false
		if capB := parseCapBytes(r.CapPerDay); capB > 0 && usedInWindow(led, key, now) >= capB {
			capped = true
		}
		if r.CapFiles > 0 && usedFilesInWindow(led, key, now) >= r.CapFiles {
			capped = true
		}
		if capped {
			var oldest time.Time
			for _, e := range lr.Events {
				if now.Sub(e.At) < uploaderWindow && (oldest.IsZero() || e.At.Before(oldest)) {
					oldest = e.At
				}
			}
			if !oldest.IsZero() {
				if ft := oldest.Add(uploaderWindow); ft.After(t) {
					t = ft
				}
			}
		}
		consider(t)
	}
	return best
}

func confInt(p map[string]string, key string) int {
	if p == nil {
		return 0
	}
	n, _ := strconv.Atoi(strings.TrimSpace(p[key]))
	return n
}

func remoteOfDst(dst string) string {
	if i := strings.Index(dst, ":"); i > 0 && !strings.HasPrefix(dst, "/") {
		return dst[:i]
	}
	return dst
}

// simRate estimates a remote's aggregate upload throughput (bytes/sec) for the
// timeline. A task bwlimit caps it directly. Otherwise throughput is concurrency ×
// per-connection speed, matching how rclone/teldrive actually push data: `transfers`
// files in parallel (task, rclone default 4) × `upload_concurrency` channels per
// file (rclone.conf, default 4) × the assumed per-connection speed. tpslimit is a
// request-rate ban guard, not a throughput knob, so it never sets the speed here.
func simRate(r uploaderRemote, conf map[string]map[string]string, calib map[string]int64, perConn int64) (rate int64, src string, limited bool) {
	var bw int64
	transfers := 0
	remoteName := r.Name
	if r.TaskID != "" {
		if t, ok := findTask(r.TaskID); ok {
			bw = int64(parseSize(t.Opts.Bwlimit))
			transfers = t.Opts.Transfers
			remoteName = remoteOfDst(t.Dst)
		}
	} else {
		bw = int64(parseSize(r.Bwlimit))
	}
	if bw > 0 {
		return bw, "bwlimit", true
	}
	if m := calib[remoteName]; m > 0 { // measured from this remote's real runs (P3.2)
		return m, "measured", true
	}
	if transfers <= 0 {
		transfers = 4 // rclone default
	}
	conc := confInt(conf[remoteName], "upload_concurrency")
	if conc <= 0 {
		conc = 4 // rclone default
	}
	streams := int64(transfers * conc)
	return streams * perConn, strconv.Itoa(transfers) + "×" + strconv.Itoa(conc) + " streams", true
}

// uploaderSimulate dry-runs the rotation engine on the posted (unsaved) config with
// a throwaway ledger — no real uploads, the live ledger is untouched. It DRAINS a
// given backlog of data across the remotes, honouring per-day caps, gaps, the
// window and rate-limit pauses, and returns a compact move-by-move timeline so you
// can see how the spread plays out and how long it takes.
func uploaderSimulate(w http.ResponseWriter, req *http.Request) {
	var body struct {
		Total       string          `json:"total"`        // backlog to upload, e.g. "3000G"
		AvgFile     string          `json:"avg_file"`     // average file size, e.g. "5G" (derives file counts)
		Scenario    string          `json:"scenario"`     // "" | flood | offline | flaky
		FloodRemote string          `json:"flood_remote"` // target remote for flood/offline scenarios
		PerConn     string          `json:"per_conn"`     // assumed per-connection speed, e.g. "5M"
		Config      *uploaderConfig `json:"config"`
	}
	_ = json.NewDecoder(req.Body).Decode(&body)

	total := int64(parseSize(body.Total))
	if total <= 0 {
		total = 2 << 40 // 2 TiB
	}
	avg := int64(parseSize(body.AvgFile))
	if avg <= 0 {
		avg = 5 << 30 // 5 GiB
	}
	perConn := int64(parseSize(body.PerConn))
	if perConn <= 0 {
		perConn = 5 << 20 // 5 MiB/s per connection
	}
	conf, _ := rclone.Remotes(rcloneConfPath()) // for per-remote upload_concurrency
	// measured throughput per remote from real runs (auto-calibration, P3.2)
	calib := map[string]int64{}

	var cfg uploaderConfig
	if body.Config != nil && len(body.Config.Remotes) > 0 {
		cfg = *body.Config
	} else {
		upMu.Lock()
		ensureUploader()
		cfg = ucfg
		upMu.Unlock()
	}
	if cfg.Strategy == "" {
		cfg.Strategy = "lru"
	}
	iv := time.Duration(cfg.IntervalMinutes) * time.Minute
	if iv <= 0 {
		iv = 15 * time.Minute
	}

	// scenario: a remote going entirely offline drops out of rotation
	if body.Scenario == "offline" && body.FloodRemote != "" {
		kept := make([]uploaderRemote, 0, len(cfg.Remotes))
		for _, r := range cfg.Remotes {
			if r.Name != body.FloodRemote {
				kept = append(kept, r)
			}
		}
		cfg.Remotes = kept
	}

	for _, r := range cfg.Remotes { // fill measured throughput per destination remote
		rn := r.Name
		if r.TaskID != "" {
			if t, ok := findTask(r.TaskID); ok {
				rn = remoteOfDst(t.Dst)
			}
		}
		if rn != "" {
			if sp := calibratedSpeed(rn); sp > 0 {
				calib[rn] = sp
			}
		}
	}

	led := map[string]*ledgerRemote{}
	rr := 0
	start := nextWindowOpen(cfg.AllowedFrom, cfg.AllowedUntil, time.Now())
	now := start
	remaining := total

	steps := []map[string]any{}
	done := false
	moveCount := 0
	for iter := 0; iter < 5000; iter++ {
		if remaining <= 0 {
			done = true
			break
		}
		// jump to the window opening if we're outside it
		if open := nextWindowOpen(cfg.AllowedFrom, cfg.AllowedUntil, now); open.After(now) {
			steps = append(steps, map[string]any{"kind": "wait", "at": now.Format(time.RFC3339), "until": open.Format(time.RFC3339), "note": "waited for upload window"})
			now = open
			continue
		}
		r, free, reason := selectRemote(cfg.Remotes, led, cfg.Strategy, &rr, now)
		if r == nil {
			nt := nextEligible(cfg, led, now)
			if nt.IsZero() || !nt.After(now) {
				steps = append(steps, map[string]any{"kind": "blocked", "at": now.Format(time.RFC3339), "note": reason})
				break
			}
			steps = append(steps, map[string]any{"kind": "wait", "at": now.Format(time.RFC3339), "until": nt.Format(time.RFC3339), "note": reason + " — waiting for daily caps to reset"})
			now = nt
			continue
		}
		key := remoteKey(*r)
		move := remaining
		if free >= 0 && move > free { // cap-bounded: whole-file, never past the remaining allowance
			move = free
		}
		if r.CapFiles > 0 { // also bound by the remaining file budget
			if fb := int64(r.CapFiles-usedFilesInWindow(led, key, now)) * avg; fb > 0 && move > fb {
				move = fb
			}
		}
		files := int(move / avg)
		if files < 1 {
			files = 1
		}
		ledgerAdd(led, key, move, files, now)
		remaining -= move
		// How long this upload actually takes at the remote's rate (the next remote
		// only starts after it finishes, plus the check interval). Unthrottled uploads
		// have no config-known speed, so the daily cap is the only pacing.
		rate, rateSrc, limited := simRate(*r, conf, calib, perConn)
		var dur time.Duration
		step := map[string]any{
			"kind": "move", "at": now.Format(time.RFC3339), "remote": r.Name, "task_id": r.TaskID,
			"bytes": humanBytes(move), "files": files, "remaining": humanBytes(max64(remaining, 0)),
		}
		if limited && rate > 0 {
			dur = time.Duration(move/rate) * time.Second
			step["rate"] = humanBytes(rate) + "/s (" + rateSrc + ")"
			step["took_min"] = int(dur.Minutes())
		} else {
			step["rate"] = rateSrc
		}
		if free >= 0 {
			step["max_transfer"] = humanBytes(free)
		}
		moveCount++
		flood := false
		switch body.Scenario {
		case "flaky": // every remote trips a rate-limit occasionally (every 3rd move)
			flood = moveCount%3 == 0
		default: // "flood" or legacy: only the chosen remote rate-limits
			flood = body.FloodRemote != "" && r.Name == body.FloodRemote
		}
		if flood {
			led[key].PausedUntil = now.Add(uploaderFloodPause)
			step["paused"] = true
			step["note"] = "rate-limited → paused " + uploaderFloodPause.String()
		}
		steps = append(steps, step)
		now = now.Add(dur + iv) // upload time + gap before the next run
	}

	summary := []map[string]any{}
	for _, r := range cfg.Remotes {
		if r.Name == "" && r.TaskID == "" {
			continue
		}
		key := remoteKey(r)
		summary = append(summary, map[string]any{
			"name": r.Name, "task_id": r.TaskID,
			"bytes": humanBytes(usedInWindow(led, key, now)), "files": usedFilesInWindow(led, key, now),
			"cap": r.CapPerDay, "cap_files": r.CapFiles,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"steps": steps, "summary": summary,
		"total": humanBytes(total), "moved": humanBytes(total - max64(remaining, 0)),
		"done": done, "elapsed_min": int(now.Sub(start).Minutes()),
	})
}

func max64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
