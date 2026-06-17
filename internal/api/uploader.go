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
	"sb-ui/internal/store"
)

// Smart Uploader (cloudplow++): watch a local staging folder; when it grows past
// a threshold, move its contents up to a cloud remote — spreading uploads across
// several remotes with per-remote daily caps + cooldowns to dodge quotas/bans.
// Built on runTransfer (rclone move), so it inherits flags/progress/stop.

type uploaderRemote struct {
	Name      string `json:"name"`     // rclone remote name
	Dest      string `json:"dest"`     // path within the remote ("" = root)
	CapPerDay string `json:"cap"`      // size/24h, "" = unlimited (e.g. "700G")
	GapMin    int    `json:"gap_min"`  // min minutes between uses of this remote
	Bwlimit   string `json:"bwlimit"`  // e.g. "40M"
	Tpslimit  int    `json:"tpslimit"` // teldrive ban-avoidance
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
}
type ledgerRemote struct {
	Events     []ledgerEvent `json:"events"`
	LastUpload time.Time     `json:"last_upload"`
}

const (
	uploaderCfgRel    = "cache/uploader.json"
	uploaderLedgerRel = "cache/uploader_ledger.json"
	uploaderWindow    = 24 * time.Hour
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

func usedInWindow(name string, now time.Time) int64 {
	lr := ledger[name]
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

func recordUpload(name string, bytes int64, now time.Time) {
	lr := ledger[name]
	if lr == nil {
		lr = &ledgerRemote{}
		ledger[name] = lr
	}
	// prune old then append
	kept := lr.Events[:0]
	for _, e := range lr.Events {
		if now.Sub(e.At) < uploaderWindow {
			kept = append(kept, e)
		}
	}
	lr.Events = append(kept, ledgerEvent{At: now, Bytes: bytes})
	lr.LastUpload = now
	store.WriteJSON(uploaderLedgerRel, ledger)
}

// pickRemote chooses an eligible remote (cap not hit, cooldown elapsed) by strategy.
func pickRemote(now time.Time) (*uploaderRemote, int64) {
	type cand struct {
		r    uploaderRemote
		free int64 // remaining cap bytes; -1 = unlimited
		used int64
	}
	var cands []cand
	for _, r := range ucfg.Remotes {
		if r.Name == "" {
			continue
		}
		if r.GapMin > 0 {
			if lr := ledger[r.Name]; lr != nil && now.Sub(lr.LastUpload) < time.Duration(r.GapMin)*time.Minute {
				continue
			}
		}
		used := usedInWindow(r.Name, now)
		free := int64(-1)
		if c := r.CapPerDay; strings.TrimSpace(c) != "" {
			capB := int64(parseSize(c))
			if used >= capB {
				continue
			}
			free = capB - used
		}
		cands = append(cands, cand{r, free, used})
	}
	if len(cands) == 0 {
		return nil, 0
	}
	switch ucfg.Strategy {
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
		return &cands[0].r, cands[0].free
	case "round_robin":
		rrIndex = (rrIndex + 1) % len(cands)
		return &cands[rrIndex].r, cands[rrIndex].free
	default: // lru — least recently used
		sort.SliceStable(cands, func(i, j int) bool {
			li, lj := ledger[cands[i].r.Name], ledger[cands[j].r.Name]
			var ti, tj time.Time
			if li != nil {
				ti = li.LastUpload
			}
			if lj != nil {
				tj = lj.LastUpload
			}
			return ti.Before(tj)
		})
		return &cands[0].r, cands[0].free
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
	size := duBytes(cfg.Source)
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
	upLastMsg = "uploading to " + r.Name
	upMu.Unlock()

	opts := transferOpts{Bwlimit: r.Bwlimit, Tpslimit: r.Tpslimit, Exclude: cfg.Excludes}
	opts.Extra = []extraFlag{{Flag: "--cutoff-mode", Value: "cautious"}}
	if free > 0 { // cap the run to the remaining daily allowance (whole files only)
		opts.Extra = append(opts.Extra, extraFlag{Flag: "--max-transfer", Value: strconv.FormatInt(free, 10)})
	}
	if cfg.MinAge != "" { // skip files still being written/downloaded
		opts.Extra = append(opts.Extra, extraFlag{Flag: "--min-age", Value: cfg.MinAge})
	}
	if cfg.DeleteEmptySrc {
		opts.Extra = append(opts.Extra, extraFlag{Flag: "--delete-empty-src-dirs", Value: ""})
	}
	dest := r.Name + ":" + strings.TrimPrefix(r.Dest, "/")
	j := jobs.Create("uploader: "+cfg.Source+" → "+dest, "move")
	runTransfer(j.ID, "move", []transferItem{{Path: cfg.Source, IsDir: true}}, dest, false, opts)

	moved := int64(0)
	statsMu.Lock()
	if s := statsStore[j.ID]; s != nil {
		moved = s.Bytes
	}
	statsMu.Unlock()
	upMu.Lock()
	recordUpload(r.Name, moved, time.Now())
	upLastMsg = "uploaded " + humanBytes(moved) + " to " + r.Name
	upMu.Unlock()
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
		used := usedInWindow(r.Name, now)
		var last any
		if lr := ledger[r.Name]; lr != nil && !lr.LastUpload.IsZero() {
			last = lr.LastUpload.UTC().Format(time.RFC3339)
		}
		remotes = append(remotes, map[string]any{
			"name": r.Name, "cap": r.CapPerDay, "used_today": humanBytes(used),
			"used_bytes": used, "last_upload": last,
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
