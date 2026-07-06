package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
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

// uploaderRemote is one rotation destination: the uploader moves its Source folder to
// Name:Dest, governed by this remote's daily caps + gap. (One source → many
// destinations is the whole point; the destinations are plain rclone remotes.)
type uploaderRemote struct {
	Name      string `json:"name"`             // rclone remote name (ledger key + label)
	Dest      string `json:"dest"`             // path within the remote ("" = root)
	CapPerDay string `json:"cap"`              // bytes/24h, "" = unlimited (gdrive 750G); teldrive often blank
	CapFiles  int    `json:"cap_files"`        // files/24h, 0 = unlimited (teldrive rate/ban dimension)
	GapMin    int    `json:"gap_min"`          // min minutes between uses of this remote
	Bwlimit   string `json:"bwlimit"`          // bandwidth, e.g. "40M"
	Tpslimit  int    `json:"tpslimit"`         // teldrive ban-avoidance
	TaskID    string `json:"task_id,omitempty"` // LEGACY: old task-mode entries, migrated to raw on load
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
func remoteKey(r uploaderRemote) string { return r.Name }

// resolveRemotes fills each destination's blank subpath / cap / files / gap from the
// shared defaults, so the picker and simulator work with fully-specified remotes.
func resolveRemotes(cfg uploaderConfig) []uploaderRemote {
	out := make([]uploaderRemote, len(cfg.Remotes))
	for i, r := range cfg.Remotes {
		if r.Dest == "" {
			r.Dest = cfg.Subpath
		}
		if r.CapPerDay == "" {
			r.CapPerDay = cfg.CapPerDay
		}
		if r.CapFiles == 0 {
			r.CapFiles = cfg.CapFiles
		}
		if r.GapMin == 0 {
			r.GapMin = cfg.GapMin
		}
		out[i] = r
	}
	return out
}

// splitRemoteDst splits an rclone "remote:path" destination into its parts.
func splitRemoteDst(dst string) (name, sub string) {
	if i := strings.Index(dst, ":"); i > 0 && !strings.HasPrefix(dst, "/") {
		return dst[:i], strings.TrimPrefix(dst[i+1:], "/")
	}
	return dst, ""
}

// balanceConfig is the opt-in capacity-balancing module: rank remotes by how full
// each account already is (lowest used → uploaded first, to level them) while never
// hammering one remote — never twice in a row, and a periodic "relief" upload to a
// fuller/neglected remote so request load spreads across every account.
type balanceConfig struct {
	Enabled   bool `json:"enabled"`
	MaxStreak int  `json:"max_streak"` // low-side uploads before one relief pick (0 = no relief)
	NoRepeat  bool `json:"no_repeat"`  // never pick the same remote twice in a row
}

// pauseConfig holds the "pause other activity while uploading" module — upload is the
// priority, so during a run we stop/throttle qBittorrent (which also starves imports)
// and pause *arr auto-import so nothing writes into the media root being moved.
type pauseConfig struct {
	ArrDisable        bool       `json:"arr_disable"`         // pause *arr auto-import (Completed Download Handling) during upload
	PlexKillTranscode bool       `json:"plex_kill_transcode"` // terminate Plex transcodes during upload (frees CPU/disk; direct-play untouched)
	AutoscanHold      bool       `json:"autoscan_hold"`       // tell autoscan to hold (not scan) during upload, release after
	Qbit              qbitConfig `json:"qbit"`                // pause/throttle qBittorrent during upload
}

type uploaderConfig struct {
	Enabled         bool             `json:"enabled"`
	Source          string           `json:"source"`   // local staging path, e.g. /mnt/local/Media
	Subpath         string           `json:"subpath"`  // shared path within each destination remote (per-remote Dest overrides)
	CapPerDay       string           `json:"cap"`      // shared daily byte cap (per-remote CapPerDay overrides)
	CapFiles        int              `json:"cap_files"` // shared daily file cap (per-remote CapFiles overrides)
	GapMin          int              `json:"gap_min"`  // shared min minutes between reuses (per-remote GapMin overrides)
	Threshold       string           `json:"threshold"`        // upload once source ≥ this size (e.g. "500G")
	Strategy        string           `json:"strategy"`         // lru | round_robin | most_free
	Balance         balanceConfig    `json:"balance"`          // capacity-balancing module (overrides Strategy when enabled)
	Pause           pauseConfig      `json:"pause"`            // pause/throttle other services during an upload
	IntervalMinutes int              `json:"interval_minutes"` // how often to check (min 1)
	AllowedFrom     string           `json:"allowed_from"`     // HH:MM, "" = anytime (off-peak window)
	AllowedUntil    string           `json:"allowed_until"`    // HH:MM
	MinAge          string           `json:"min_age"`          // skip files newer than this (e.g. "15m") → don't upload in-progress
	DeleteEmptySrc  bool             `json:"delete_empty_src"` // tidy staging after move
	Opts            transferOpts     `json:"opts"`             // rclone transfer flags applied to every destination
	Excludes        []string         `json:"excludes"`         // LEGACY: migrated into Opts.Exclude on load
	Remotes         []uploaderRemote `json:"remotes"`
}

// balanceState carries the balancing module's cross-cycle memory.
type balanceState struct {
	streak int    // consecutive low-side (least-used) picks so far
	last   string // remote name of the previous pick (for the no-repeat rule)
}

// defaultMaxStreak is used when balancing is on but no cap is configured.
const defaultMaxStreak = 3

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
	balState   balanceState
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
	if ucfg.Pause.Qbit.Action == "" {
		ucfg.Pause.Qbit.Action = "pause"
	}
	migrateTaskRemotes() // one-time: convert legacy task-mode destinations to raw remotes
	upLoaded = true
}

// migrateTaskRemotes converts legacy task-referencing destinations into plain remotes
// (Name:Dest + inherited bwlimit/tpslimit), and renames their ledger keys so daily-cap
// history carries over. The uploader now owns the Source; destinations are just remotes.
func migrateTaskRemotes() {
	changed := false
	if len(ucfg.Opts.Exclude) == 0 && len(ucfg.Excludes) > 0 { // fold the old global excludes in
		ucfg.Opts.Exclude = ucfg.Excludes
		ucfg.Excludes = nil
		changed = true
	}
	for i := range ucfg.Remotes {
		r := &ucfg.Remotes[i]
		if r.TaskID == "" {
			continue
		}
		oldKey := "task:" + r.TaskID
		if t, ok := findTask(r.TaskID); ok {
			name, sub := splitRemoteDst(t.Dst)
			r.Name = name // the old Name held the task's display label, not the remote
			r.Dest = sub
			if r.Bwlimit == "" {
				r.Bwlimit = t.Opts.Bwlimit
			}
			if r.Tpslimit == 0 {
				r.Tpslimit = t.Opts.Tpslimit
			}
		}
		if lr := ledger[oldKey]; lr != nil && r.Name != "" { // carry the cap ledger over
			ledger[r.Name] = lr
			delete(ledger, oldKey)
		}
		r.TaskID = ""
		changed = true
	}
	// Recovery for configs migrated by the earlier (buggy) version, where the remote
	// name was left as the task's label: if a destination's name matches a task whose
	// real destination remote differs, repoint it to the actual remote.
	for i := range ucfg.Remotes {
		r := &ucfg.Remotes[i]
		if r.Name == "" {
			continue
		}
		if t, ok := findTaskByName(r.Name); ok {
			if name, sub := splitRemoteDst(t.Dst); name != "" && name != r.Name {
				if lr := ledger[r.Name]; lr != nil {
					ledger[name] = lr
					delete(ledger, r.Name)
				}
				r.Name = name
				if r.Dest == "" {
					r.Dest = sub
				}
				changed = true
			}
		}
	}
	// Hoist values shared by EVERY destination up to the config defaults, so the UI
	// shows one shared value instead of repeating it on every row.
	if len(ucfg.Remotes) > 0 {
		r0 := ucfg.Remotes[0]
		sameSub, sameCap, sameFiles, sameGap := true, true, true, true
		for _, r := range ucfg.Remotes {
			sameSub = sameSub && r.Dest == r0.Dest
			sameCap = sameCap && r.CapPerDay == r0.CapPerDay
			sameFiles = sameFiles && r.CapFiles == r0.CapFiles
			sameGap = sameGap && r.GapMin == r0.GapMin
		}
		if ucfg.Subpath == "" && sameSub && r0.Dest != "" {
			ucfg.Subpath = r0.Dest
			for i := range ucfg.Remotes {
				ucfg.Remotes[i].Dest = ""
			}
			changed = true
		}
		if ucfg.CapPerDay == "" && sameCap && r0.CapPerDay != "" {
			ucfg.CapPerDay = r0.CapPerDay
			for i := range ucfg.Remotes {
				ucfg.Remotes[i].CapPerDay = ""
			}
			changed = true
		}
		if ucfg.CapFiles == 0 && sameFiles && r0.CapFiles != 0 {
			ucfg.CapFiles = r0.CapFiles
			for i := range ucfg.Remotes {
				ucfg.Remotes[i].CapFiles = 0
			}
			changed = true
		}
		if ucfg.GapMin == 0 && sameGap && r0.GapMin != 0 {
			ucfg.GapMin = r0.GapMin
			for i := range ucfg.Remotes {
				ucfg.Remotes[i].GapMin = 0
			}
			changed = true
		}
	}
	if changed {
		store.WriteJSON(uploaderCfgRel, ucfg)
		store.WriteJSON(uploaderLedgerRel, ledger)
	}
}

// Injectable seams for the block actions, so tests can assert the orchestration
// (what runs, in what order) without touching a real qBittorrent / *arr.
var (
	qbitPauseFn    = qbitPause
	qbitResumeFn   = qbitResume
	arrImportsFn   = arrSetImportsEnabled
	plexKillFn     = startPlexTranscodeKill
	plexUnkillFn   = stopPlexTranscodeKill
	autoscanHoldFn = autoscanHold
)

// applyUploadPause slows down other services just before an upload runs; restore undoes
// it after. Both are best-effort — a failure here never blocks the upload itself.
func applyUploadPause(p pauseConfig) {
	if p.Qbit.Enabled {
		_ = qbitPauseFn(resolveQbit(p.Qbit))
	}
	if p.ArrDisable {
		arrImportsFn(false)
	}
	if p.PlexKillTranscode {
		plexKillFn(loadOptions().Plex)
	}
	if p.AutoscanHold {
		_ = autoscanHoldFn(true)
	}
}

func restoreUploadPause(p pauseConfig) {
	if p.Qbit.Enabled {
		_ = qbitResumeFn(resolveQbit(p.Qbit))
	}
	if p.ArrDisable {
		arrImportsFn(true)
	}
	if p.PlexKillTranscode {
		plexUnkillFn()
	}
	if p.AutoscanHold {
		_ = autoscanHoldFn(false)
	}
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

// pickCtx bundles everything the picker needs beyond the remotes + ledger, so the
// live uploader, the dry-run simulator and tests all drive selectRemote identically.
type pickCtx struct {
	strategy string           // lru | round_robin | most_free (used when Balance is off)
	rr       *int             // round-robin cursor
	balance  balanceConfig    // capacity-balancing module (overrides strategy when Enabled)
	bstate   *balanceState    // the module's cross-cycle memory
	used     map[string]int64 // total used bytes per remote name (balance ranking input)
}

type upCand struct {
	r    uploaderRemote
	free int64 // remaining daily cap, -1 = unlimited
}

// eligibleCands filters the remotes down to those that can accept an upload right now
// (not benched, past their gap cooldown, under their daily byte/file caps), returning
// each with its remaining allowance. The reason explains an empty result.
func eligibleCands(remotes []uploaderRemote, led map[string]*ledgerRemote, now time.Time) ([]upCand, string) {
	var cands []upCand
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
		cands = append(cands, upCand{r, free})
	}
	return cands, reason
}

// ── strategy pickers: each returns the chosen index into cands ──────────────────

// pickMostFree favours the remote with the largest remaining daily allowance
// (unlimited wins); ties keep input order.
func pickMostFree(cands []upCand) int {
	best := 0
	for i := 1; i < len(cands); i++ {
		if moreFree(cands[i].free, cands[best].free) {
			best = i
		}
	}
	return best
}

func moreFree(a, b int64) bool {
	if a == -1 {
		return b != -1 // unlimited beats any finite cap; unlimited-vs-unlimited keeps order
	}
	if b == -1 {
		return false
	}
	return a > b
}

// pickLRU favours the least-recently-used remote (never-used = oldest).
func pickLRU(cands []upCand, led map[string]*ledgerRemote) int {
	best, bestT := 0, lastUpload(led, cands[0].r)
	for i := 1; i < len(cands); i++ {
		if t := lastUpload(led, cands[i].r); t.Before(bestT) {
			best, bestT = i, t
		}
	}
	return best
}

func lastUpload(led map[string]*ledgerRemote, r uploaderRemote) time.Time {
	if lr := led[remoteKey(r)]; lr != nil {
		return lr.LastUpload
	}
	return time.Time{}
}

// pickBalance implements the capacity-balancing module (see balanceConfig):
//  1. drop the previous remote (no two uploads in a row to the same one),
//  2. normally pick the least-used (emptiest) account to level them up,
//  3. every maxStreak picks, do one relief upload to the least-recently-used of the
//     rest instead — which favours the fuller, neglected accounts — so request load
//     spreads across every remote.
//
// It mutates st (streak/last) and returns the chosen index into cands.
func pickBalance(cands []upCand, led map[string]*ledgerRemote, used map[string]int64, cfg balanceConfig, st *balanceState) int {
	avail := make([]int, 0, len(cands))
	for i := range cands {
		if cfg.NoRepeat && len(cands) > 1 && cands[i].r.Name == st.last {
			continue // never the same remote twice running (unless it's the only option)
		}
		avail = append(avail, i)
	}
	if len(avail) == 0 { // only the just-used remote is eligible → allow the repeat
		for i := range cands {
			avail = append(avail, i)
		}
	}
	maxStreak := cfg.MaxStreak
	var pick int
	if maxStreak > 0 && st.streak >= maxStreak {
		pick = leastRecent(avail, cands, led) // relief: give a fuller/neglected remote a turn
		st.streak = 0
	} else {
		pick = leastUsed(avail, cands, used) // level up: emptiest account first
		st.streak++
	}
	st.last = cands[pick].r.Name
	return pick
}

func leastUsed(idxs []int, cands []upCand, used map[string]int64) int {
	best := idxs[0]
	for _, i := range idxs[1:] {
		if used[cands[i].r.Name] < used[cands[best].r.Name] {
			best = i
		}
	}
	return best
}

func leastRecent(idxs []int, cands []upCand, led map[string]*ledgerRemote) int {
	best, bestT := idxs[0], lastUpload(led, cands[idxs[0]].r)
	for _, i := range idxs[1:] {
		if t := lastUpload(led, cands[i].r); t.Before(bestT) {
			best, bestT = i, t
		}
	}
	return best
}

// selectRemote is the pure remote-picker: filter to eligible remotes, then dispatch
// to the balancing module (if enabled) or the configured strategy. Returns the chosen
// remote + remaining cap bytes (-1 = unlimited), or (nil, 0, reason) when none fit.
func selectRemote(remotes []uploaderRemote, led map[string]*ledgerRemote, pc pickCtx, now time.Time) (*uploaderRemote, int64, string) {
	cands, reason := eligibleCands(remotes, led, now)
	if len(cands) == 0 {
		return nil, 0, reason
	}
	var idx int
	switch {
	case pc.balance.Enabled:
		idx = pickBalance(cands, led, pc.used, pc.balance, pc.bstate)
	case pc.strategy == "most_free":
		idx = pickMostFree(cands)
	case pc.strategy == "round_robin":
		*pc.rr = (*pc.rr + 1) % len(cands)
		idx = *pc.rr
	default: // lru
		idx = pickLRU(cands, led)
	}
	return &cands[idx].r, cands[idx].free, ""
}

// livePickCtx builds the picker context from the live config + package state.
func livePickCtx(used map[string]int64) pickCtx {
	return pickCtx{strategy: ucfg.Strategy, rr: &rrIndex, balance: normBalance(ucfg.Balance), bstate: &balState, used: used}
}

// normBalance fills in the default relief cap when balancing is on but unset.
func normBalance(b balanceConfig) balanceConfig {
	if b.Enabled && b.MaxStreak <= 0 {
		b.MaxStreak = defaultMaxStreak
	}
	return b
}

// pickRemote chooses an eligible remote from the live config/ledger. used carries the
// per-remote total-used bytes (only needed/fetched when balancing is enabled).
func pickRemote(now time.Time, used map[string]int64) (*uploaderRemote, int64) {
	r, free, _ := selectRemote(resolveRemotes(ucfg), ledger, livePickCtx(used), now)
	return r, free
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
	size := measureSource(cfg.Source)
	thr := int64(parseSize(cfg.Threshold))

	// Capacity-balancing ranks remotes by how full each account is — fetch that
	// (cached rclone about) outside the lock so a cache miss doesn't stall the mutex.
	var used map[string]int64
	if cfg.Balance.Enabled {
		uctx, ucancel := context.WithTimeout(context.Background(), 12*time.Second)
		used = remoteUsedBytes(uctx)
		ucancel()
	}

	upMu.Lock()
	upLastSize, upLastAt = size, time.Now()
	if thr > 0 && size < thr {
		upLastMsg = "below threshold"
		upMu.Unlock()
		return
	}
	r, free := pickRemote(time.Now(), used)
	if r == nil {
		upLastMsg = "no eligible remote (caps/cooldowns)"
		upMu.Unlock()
		return
	}
	upLastMsg = "uploading via " + r.Name
	upMu.Unlock()

	// Move the staging source up to this destination remote (Name:Dest), using the
	// global transfer options + any per-remote bandwidth/tps override.
	op := "move"
	items := []transferItem{{Path: cfg.Source, IsDir: true}}
	sub := r.Dest // per-remote subpath overrides the shared one
	if sub == "" {
		sub = cfg.Subpath
	}
	dst := r.Name + ":" + strings.TrimPrefix(sub, "/")
	opts := cfg.Opts
	if r.Bwlimit != "" {
		opts.Bwlimit = r.Bwlimit
	}
	if r.Tpslimit != 0 {
		opts.Tpslimit = r.Tpslimit
	}
	// Layer the uploader's safety knobs on top (copy the slices so cfg.Opts isn't mutated).
	opts.Exclude = append(append([]string{}, opts.Exclude...), cfg.Excludes...)
	opts.Extra = append(append([]extraFlag{}, opts.Extra...), extraFlag{Flag: "--cutoff-mode", Value: "cautious"})
	if free > 0 { // cap the run to the remaining daily allowance (whole files only)
		opts.Extra = append(opts.Extra, extraFlag{Flag: "--max-transfer", Value: strconv.FormatInt(free, 10)})
	}
	if cfg.MinAge != "" { // skip files still being written/downloaded
		opts.Extra = append(opts.Extra, extraFlag{Flag: "--min-age", Value: cfg.MinAge})
	}
	if cfg.DeleteEmptySrc {
		opts.Extra = append(opts.Extra, extraFlag{Flag: "--delete-empty-src-dirs", Value: ""})
	}
	// Slow down other services (qBittorrent, *arr imports) for the duration of the run.
	applyUploadPause(cfg.Pause)
	moved, files, flood := uploadRunner("uploader: "+transferLabel(op, items, dst), r.TaskID, op, items, dst, opts)
	restoreUploadPause(cfg.Pause)

	// Post-upload: let the built-in autoscan pick up the moved paths (Plex-visible
	// side, via path mappings) instead of docker-pausing an external autoscan.
	if files > 0 {
		if au := loadOptions().Autoscan; au.Enabled && au.OnUpload {
			paths := make([]string, 0, len(items))
			for _, it := range items {
				paths = append(paths, it.Path)
			}
			autoscanSvc().Enqueue("upload", "", paths...)
		}
	}

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
	balState = balanceState{} // fresh streak/last after a config change
	store.WriteJSON(uploaderCfgRel, ucfg)
	upMu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// uploaderTestBlock lets the user verify the "pause activity" block for real: it
// applies (or restores) the configured block right now against the live qBittorrent /
// *arr, then reads their state back so the effect is visible without an actual upload.
func uploaderTestBlock(w http.ResponseWriter, req *http.Request) {
	var b struct {
		Action string       `json:"action"` // apply | restore
		Pause  *pauseConfig `json:"pause"`  // test the current (maybe unsaved) form settings
	}
	_ = json.NewDecoder(req.Body).Decode(&b)
	upMu.Lock()
	ensureUploader()
	p := ucfg.Pause
	upMu.Unlock()
	if b.Pause != nil { // honour the settings the user has in the form right now
		p = *b.Pause
	}

	if b.Action == "restore" {
		restoreUploadPause(p)
	} else {
		b.Action = "apply"
		applyUploadPause(p)
	}

	resp := map[string]any{"action": b.Action}
	if p.Qbit.Enabled {
		resp["qbit"] = qbitStatus(resolveQbit(p.Qbit))
	} else {
		resp["qbit"] = "not enabled"
	}
	if p.ArrDisable {
		blocked, tot := arrImportsStatus()
		resp["arr"] = fmt.Sprintf("auto-import blocked on %d of %d instances", blocked, tot)
	} else {
		resp["arr"] = "not enabled"
	}
	if p.PlexKillTranscode {
		resp["plex"] = fmt.Sprintf("%d transcoding session(s) still active", plexTranscodeCount(loadOptions().Plex))
	} else {
		resp["plex"] = "not enabled"
	}
	if p.AutoscanHold {
		resp["autoscan"] = autoscanStatus()
	} else {
		resp["autoscan"] = "not enabled"
	}
	writeJSON(w, http.StatusOK, resp)
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
func nextEligible(remotes []uploaderRemote, led map[string]*ledgerRemote, now time.Time) time.Time {
	best := time.Time{}
	consider := func(t time.Time) {
		if t.After(now) && (best.IsZero() || t.Before(best)) {
			best = t
		}
	}
	for _, r := range remotes {
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
	transfers := 0
	remoteName := r.Name
	bw := int64(parseSize(r.Bwlimit))
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
		if r.Name != "" {
			if sp := calibratedSpeed(r.Name); sp > 0 {
				calib[r.Name] = sp
			}
		}
	}

	remotes := resolveRemotes(cfg) // apply shared subpath/cap/files/gap defaults
	led := map[string]*ledgerRemote{}
	rr := 0
	// Balancing sim: seed each remote's account fill from the live `rclone about`, then
	// grow it as the simulated uploads land, so the picker ranks them realistically.
	var bstate balanceState
	simUsed := map[string]int64{}
	if cfg.Balance.Enabled {
		uctx, ucancel := context.WithTimeout(context.Background(), 12*time.Second)
		for k, v := range remoteUsedBytes(uctx) {
			simUsed[k] = v
		}
		ucancel()
	}
	pc := pickCtx{strategy: cfg.Strategy, rr: &rr, balance: normBalance(cfg.Balance), bstate: &bstate, used: simUsed}
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
		r, free, reason := selectRemote(remotes, led, pc, now)
		if r == nil {
			nt := nextEligible(remotes, led, now)
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
		if cfg.Balance.Enabled {
			simUsed[r.Name] += move // account fills up → re-ranks on the next pick
		}
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
