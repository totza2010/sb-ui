package api

import (
	"context"
	"encoding/json"
	"fmt"
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

// uploaderRemote is one rotation destination: the uploader moves its Source folder to
// Name:Dest, governed by this remote's daily caps + gap. (One source → many
// destinations is the whole point; the destinations are plain rclone remotes.)
type uploaderRemote struct {
	Name      string `json:"name"`              // rclone remote name (ledger key + label)
	Dest      string `json:"dest"`              // path within the remote ("" = root)
	CapPerDay string `json:"cap"`               // bytes/24h, "" = unlimited (gdrive 750G); teldrive often blank
	CapFiles  int    `json:"cap_files"`         // files/24h, 0 = unlimited (teldrive rate/ban dimension)
	GapMin    int    `json:"gap_min"`           // min minutes between uses of this remote
	Bwlimit   string `json:"bwlimit"`           // bandwidth, e.g. "40M"
	Tpslimit  int    `json:"tpslimit"`          // teldrive ban-avoidance
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
// hammering one remote — never twice in a row, and never more than MaxStreak uploads
// to the same remote per cycle. Without that per-remote cap, a big capacity gap means
// the emptiest one or two accounts win every pick and the others are never used.
type balanceConfig struct {
	Enabled   bool `json:"enabled"`
	MaxStreak int  `json:"max_streak"` // max uploads one remote may take per cycle (0 = uncapped)
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
	Source          string           `json:"source"`           // local staging path, e.g. /mnt/local/Media
	Subpath         string           `json:"subpath"`          // shared path within each destination remote (per-remote Dest overrides)
	CapPerDay       string           `json:"cap"`              // shared daily byte cap (per-remote CapPerDay overrides)
	CapFiles        int              `json:"cap_files"`        // shared daily file cap (per-remote CapFiles overrides)
	GapMin          int              `json:"gap_min"`          // shared min minutes between reuses (per-remote GapMin overrides)
	Threshold       string           `json:"threshold"`        // upload once source ≥ this size (e.g. "500G")
	Op              string           `json:"op"`               // "move" (default) | "copy"; move = source freed, appears via unionfs
	DryRun          bool             `json:"dry_run"`          // run rclone with --dry-run: real command, moves nothing, no state touched
	Sequence        []string         `json:"sequence"`         // rotation order: remote names (may repeat); the single source of truth
	Strategy        string           `json:"strategy"`         // DEPRECATED (migrated to Sequence): lru | round_robin | most_free
	Balance         balanceConfig    `json:"balance"`          // DEPRECATED (migrated to Sequence): old capacity-balancing module
	Pause           pauseConfig      `json:"pause"`            // pause/throttle other services during an upload
	IntervalMinutes int              `json:"interval_minutes"` // how often to check (min 1)
	AllowedFrom     string           `json:"allowed_from"`     // HH:MM, "" = anytime (off-peak window)
	AllowedUntil    string           `json:"allowed_until"`    // HH:MM
	MinAge          string           `json:"min_age"`          // skip files newer than this (e.g. "15m") → don't upload in-progress
	EtaSpeed        string           `json:"eta_speed"`        // assumed speed for the plan ETA, e.g. "50M"; blank = per-remote calibrated avg
	DeleteEmptySrc  bool             `json:"delete_empty_src"` // tidy staging after move
	Opts            transferOpts     `json:"opts"`             // rclone transfer flags applied to every destination
	Excludes        []string         `json:"excludes"`         // LEGACY: migrated into Opts.Exclude on load
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
	Uploaded    int64         `json:"uploaded,omitempty"`     // cumulative lifetime bytes we've moved to this remote (fill proxy when the backend can't report usage)
}

const (
	uploaderCfgRel     = "cache/uploader.json"
	uploaderLedgerRel  = "cache/uploader_ledger.json"
	uploaderWindow     = 24 * time.Hour
	uploaderFloodPause = 60 * time.Minute // cooldown after a rate-limit/ban hit
	uploaderCooldown   = 45 * time.Second // pause after a remote finishes before re-listing + rotating to the next
)

var (
	upMu        sync.Mutex
	ucfg        uploaderConfig
	ledger      = map[string]*ledgerRemote{}
	upLoaded    bool
	upLastSize  int64
	upLastAt    time.Time
	upLastMsg   string
	upLastPlan  *uploadPlan // last manual "Check now" dry-run plan
	upChecking  bool        // a manual "Check now" is measuring/planning right now
	seqCursor   int         // position of the last pick in cfg.Sequence (advances each pick)
	upLastMoved int64       // bytes moved by the most recent cycle (0 = nothing) — drives the loop cooldown
	upOnce      sync.Once

	// cached per-remote fills for the (scan-free) rotation-order projection, refreshed in
	// the background so the order renders without a manual "Check now".
	balFillM    map[string]int64
	balFillAt   time.Time
	balFillBusy bool
)

// cachedBalanceFill returns the last-known per-remote fills, kicking off a background
// refresh when the cache is empty or stale. Caller holds upMu.
func cachedBalanceFill() map[string]int64 {
	if !balFillBusy && (balFillM == nil || time.Since(balFillAt) > 90*time.Second) {
		balFillBusy = true
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
			m := balanceFill(ctx)
			cancel()
			upMu.Lock()
			balFillM, balFillAt, balFillBusy = m, time.Now(), false
			upMu.Unlock()
		}()
	}
	return balFillM
}

func ensureUploader() { // under upMu
	if upLoaded {
		return
	}
	store.ReadJSON(uploaderCfgRel, &ucfg)
	store.ReadJSON(uploaderLedgerRel, &ledger)
	if ledger == nil {
		ledger = map[string]*ledgerRemote{}
	}
	loadHistory()
	if ucfg.IntervalMinutes <= 0 {
		ucfg.IntervalMinutes = 15
	}
	if ucfg.Pause.Qbit.Action == "" {
		ucfg.Pause.Qbit.Action = "pause"
	}
	migrateTaskRemotes()               // one-time: convert legacy task-mode destinations to raw remotes
	ucfg.Sequence = normSequence(ucfg) // migrate old strategy/balance configs to an even sequence
	loadResume()                       // restore a pending resume target across restarts
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
	qbitPauseFn      = qbitPause
	qbitResumeFn     = qbitResume
	arrImportsFn     = arrSetImportsEnabled
	plexKillFn       = startPlexTranscodeKill
	plexUnkillFn     = stopPlexTranscodeKill
	autoscanHoldFn   = autoscanHold                     // external autoscan container (docker pause)
	autoscanPauseFn  = func() { autoscanSvc().Pause() } // built-in autoscan hold
	autoscanResumeFn = func() { autoscanSvc().Resume() }
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
		autoscanPauseFn()        // hold the built-in autoscan queue
		_ = autoscanHoldFn(true) // and the external container, if any (best-effort)
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
		autoscanResumeFn()        // release the built-in autoscan queue
		_ = autoscanHoldFn(false) // and the external container, if any
	}
}

// remoteFree is THE definition of "how many bytes may this remote still take today":
// its daily cap minus what it used inside the rolling window. Returns -1 for an
// unlimited remote and 0 when the cap is spent.
//
// Every consumer goes through this — the picker, the plan, the free-space map and the
// self-test command preview — so the number the UI shows, the number the plan splits by
// and the --max-transfer the run actually passes to rclone can never drift apart.
func remoteFree(r uploaderRemote, led map[string]*ledgerRemote, now time.Time) int64 {
	capB := parseCapBytes(r.CapPerDay)
	if capB <= 0 {
		return -1 // unlimited
	}
	if f := capB - usedInWindow(led, remoteKey(r), now); f > 0 {
		return f
	}
	return 0
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
	lr.Uploaded += bytes // lifetime tally — a fill proxy for the balancer
}

func recordUpload(name string, bytes int64, files int, now time.Time) {
	ledgerAdd(ledger, name, bytes, files, now)
	store.WriteJSON(uploaderLedgerRel, ledger)
}

// balanceFill is the per-remote fill signal for capacity balancing: live rclone `about`
// used bytes when the backend reports them, else our persisted cumulative-uploaded tally
// — so balancing still works for remotes that can't report usage (e.g. teldrive). Not
// called with upMu held.
func balanceFill(ctx context.Context) map[string]int64 {
	m := remoteUsedBytes(ctx) // rclone `about` (works for gdrive/onedrive/…)
	if m == nil {
		m = map[string]int64{}
	}
	for name, used := range teldriveUsedBytes(ctx) { // real per-account fill for teldrive
		if used > 0 {
			m[name] = used
		}
	}
	upMu.Lock()
	for key, lr := range ledger { // last resort: our own cumulative-uploaded tally
		if lr != nil && lr.Uploaded > 0 && m[key] == 0 {
			m[key] = lr.Uploaded
		}
	}
	upMu.Unlock()
	return m
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
// Rotation is a single explicit sequence of remote names (may repeat); the picker just
// advances a cursor along it and skips any step that isn't eligible right now.
type pickCtx struct {
	seq    []string // the rotation sequence (remote names, repeats allowed)
	cursor *int     // position of the last pick in seq; advances each call
	resume string   // a remote to finish first (interrupted upload); doesn't advance the cursor
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
		free := remoteFree(r, led, now)
		if free == 0 { // cap spent for this window
			reason = "all remotes hit their daily caps"
			continue
		}
		cands = append(cands, upCand{r, free})
	}
	return cands, reason
}

// ── rotation picker: walk the explicit sequence, skipping ineligible steps ───────

// pickBySequence advances the cursor along seq and returns the index (into cands) of the
// next remote that is currently eligible. Steps whose remote is capped/cooling/benched are
// skipped. Returns -1 when a full lap finds none eligible (caller then waits for a reset).
// An empty sequence degrades to plain round-robin over the eligible candidates, so the
// uploader still works before a sequence is authored.
func pickBySequence(cands []upCand, seq []string, cursor *int) int {
	if len(cands) == 0 {
		return -1
	}
	// cursor is the NEXT position to try; a fresh cursor (0) picks seq[0]. After choosing
	// position p the cursor advances to p+1.
	if len(seq) == 0 {
		i := ((*cursor % len(cands)) + len(cands)) % len(cands)
		*cursor = i + 1
		return i
	}
	byName := make(map[string]int, len(cands))
	for i, c := range cands {
		byName[c.r.Name] = i
	}
	n := len(seq)
	start := ((*cursor % n) + n) % n
	for k := 0; k < n; k++ {
		pos := (start + k) % n
		if idx, ok := byName[seq[pos]]; ok {
			*cursor = pos + 1
			return idx
		}
	}
	return -1
}

// selectRemote is the pure remote-picker: filter to eligible remotes, then take the next
// one the rotation sequence points at. Returns the chosen remote + remaining cap bytes
// (-1 = unlimited), or (nil, 0, reason) when none fit.
func selectRemote(remotes []uploaderRemote, led map[string]*ledgerRemote, pc pickCtx, now time.Time) (*uploaderRemote, int64, string) {
	cands, reason := eligibleCands(remotes, led, now)
	if len(cands) == 0 {
		return nil, 0, reason
	}
	// Resume override: an interrupted remote is finished first (if it's eligible now),
	// without advancing the sequence — so the rotation continues from where it stood once
	// the partial is done.
	if pc.resume != "" {
		for i := range cands {
			if cands[i].r.Name == pc.resume {
				return &cands[i].r, cands[i].free, ""
			}
		}
	}
	idx := pickBySequence(cands, pc.seq, pc.cursor)
	if idx < 0 {
		return nil, 0, "no remote in the rotation sequence is eligible right now"
	}
	return &cands[idx].r, cands[idx].free, ""
}

// livePickCtx builds the picker context from the live config + package state.
func livePickCtx() pickCtx {
	return pickCtx{seq: ucfg.Sequence, cursor: &seqCursor, resume: resumeRemote}
}

// pickRemote chooses an eligible remote from the live config/ledger.
func pickRemote(now time.Time) (*uploaderRemote, int64) {
	r, free, _ := selectRemote(resolveRemotes(ucfg), ledger, livePickCtx(), now)
	return r, free
}

// selectedNames lists the configured destination remote names in order (deduped).
func selectedNames(cfg uploaderConfig) []string {
	seen := map[string]bool{}
	var out []string
	for _, r := range cfg.Remotes {
		if n := strings.TrimSpace(r.Name); n != "" && !seen[n] {
			seen[n] = true
			out = append(out, n)
		}
	}
	return out
}

// normSequence keeps only sequence entries that are still selected remotes; an empty result
// falls back to "even" (each selected remote once). This also migrates an old config that
// has no sequence yet — it simply gets the even rotation over its selected remotes.
func normSequence(cfg uploaderConfig) []string {
	sel := selectedNames(cfg)
	valid := map[string]bool{}
	for _, n := range sel {
		valid[n] = true
	}
	var seq []string
	for _, n := range cfg.Sequence {
		if valid[strings.TrimSpace(n)] {
			seq = append(seq, strings.TrimSpace(n))
		}
	}
	if len(seq) == 0 {
		return sel
	}
	return seq
}

// wrrSequence expands per-remote weights into a smoothly interleaved sequence (the nginx
// smooth weighted round-robin), deterministic and length = sum(weights), capped for sanity.
func wrrSequence(order []string, weights map[string]int) []string {
	wOf := func(n string) int {
		if weights != nil {
			if w, ok := weights[n]; ok && w >= 1 {
				return w
			}
		}
		return 1
	}
	total := 0
	for _, n := range order {
		total += wOf(n)
	}
	if len(order) == 0 || total == 0 {
		return append([]string{}, order...)
	}
	if total > 100 { // keep the authored list manageable
		total = 100
	}
	cw := make(map[string]int, len(order))
	out := make([]string, 0, total)
	for len(out) < total {
		best := ""
		for _, n := range order {
			cw[n] += wOf(n)
			if best == "" || cw[n] > cw[best] {
				best = n
			}
		}
		twTotal := 0
		for _, n := range order {
			twTotal += wOf(n)
		}
		cw[best] -= twTotal
		out = append(out, best)
	}
	return out
}

// genByRank orders the selected remotes by a numeric key, then weights them N..1 by rank so
// the first (emptiest / most-free) appears most often. asc=true ranks smallest-first.
func genByRank(sel []string, key map[string]int64, asc bool) []string {
	order := append([]string{}, sel...)
	sort.SliceStable(order, func(i, j int) bool {
		if asc {
			return key[order[i]] < key[order[j]]
		}
		return key[order[i]] > key[order[j]]
	})
	weights := map[string]int{}
	for i, n := range order {
		weights[n] = len(order) - i
	}
	return wrrSequence(order, weights)
}

// generateSequence builds a rotation sequence for the given mode. Callers pass fill/free
// only for the modes that need them.
func generateSequence(cfg uploaderConfig, mode string, weights map[string]int, fill, free map[string]int64) []string {
	sel := selectedNames(cfg)
	switch mode {
	case "weights":
		return wrrSequence(sel, weights)
	case "byfill":
		return genByRank(sel, fill, true) // emptiest first
	case "byfree":
		return genByRank(sel, free, false) // most daily quota left first
	default: // "even"
		return wrrSequence(sel, nil)
	}
}

// freeToday is each remote's remaining daily byte allowance (cap − used in the window),
// unlimited caps reported as a large sentinel so they sort as "most free".
func freeToday(cfg uploaderConfig, led map[string]*ledgerRemote, now time.Time) map[string]int64 {
	out := map[string]int64{}
	for _, r := range resolveRemotes(cfg) {
		if r.Name == "" {
			continue
		}
		f := remoteFree(r, led, now)
		if f < 0 {
			f = 1 << 62 // unlimited → most free
		}
		out[r.Name] = f
	}
	return out
}

// uploaderGenSequence returns a generated rotation sequence for the on-screen config, so
// the UI's "Even / Weights / By fill / By free quota" buttons all resolve server-side.
func uploaderGenSequence(w http.ResponseWriter, req *http.Request) {
	var body struct {
		Mode    string         `json:"mode"`
		Weights map[string]int `json:"weights"`
		Config  uploaderConfig `json:"config"`
	}
	_ = json.NewDecoder(req.Body).Decode(&body)
	cfg := body.Config
	if len(cfg.Remotes) == 0 {
		upMu.Lock()
		ensureUploader()
		cfg = ucfg
		upMu.Unlock()
	}
	var fill, free map[string]int64
	if body.Mode == "byfill" {
		ctx, cancel := context.WithTimeout(req.Context(), 15*time.Second)
		fill = balanceFill(ctx)
		cancel()
	}
	if body.Mode == "byfree" {
		upMu.Lock()
		led := cloneLedger(ledger)
		upMu.Unlock()
		free = freeToday(cfg, led, time.Now())
	}
	writeJSON(w, http.StatusOK, map[string]any{"sequence": generateSequence(cfg, body.Mode, body.Weights, fill, free)})
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

// uploaderOp is the rclone operation the uploader runs: move by default (source freed,
// reappears via unionfs on the same path), or copy when explicitly configured.
func uploaderOp(cfg uploaderConfig) string {
	if cfg.Op == "copy" {
		return "copy"
	}
	return "move"
}

// uploaderRemoteJob assembles the source items, destination, and layered transfer options
// for one destination remote — the global opts plus per-remote bwlimit/tps and the
// uploader's safety knobs (cutoff-mode, the remaining daily allowance as --max-transfer,
// min-age, delete-empty). Pure, so both the real run and the command preview use it and
// stay identical. free is the remaining daily byte allowance (0 = no cap this run).
func uploaderRemoteJob(cfg uploaderConfig, r uploaderRemote, free int64) ([]transferItem, string, transferOpts) {
	items := []transferItem{{Path: cfg.Source, IsDir: true, Contents: true}}
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
	// Copy the slices so cfg.Opts isn't mutated.
	opts.Exclude = append(append([]string{}, opts.Exclude...), cfg.Excludes...)
	opts.Extra = append(append([]extraFlag{}, opts.Extra...), extraFlag{Flag: "--cutoff-mode", Value: "cautious"})
	if free > 0 { // cap the run to the remaining daily allowance (whole files only)
		// The value MUST carry the "B" suffix: rclone reads a bare number as KiB, which would
		// make the cap 1024× too large — the remote would blow past its daily quota.
		opts.Extra = append(opts.Extra, extraFlag{Flag: "--max-transfer", Value: strconv.FormatInt(free, 10) + "B"})
	}
	if cfg.MinAge != "" { // skip files still being written/downloaded
		opts.Extra = append(opts.Extra, extraFlag{Flag: "--min-age", Value: cfg.MinAge})
	}
	if cfg.DeleteEmptySrc {
		opts.Extra = append(opts.Extra, extraFlag{Flag: "--delete-empty-src-dirs", Value: ""})
	}
	if cfg.DryRun {
		opts.Extra = append(opts.Extra, extraFlag{Flag: "--dry-run", Value: ""})
	}
	return items, dst, opts
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

	// uploadRunner performs the move and reports what moved, whether the run hit a provider
	// rate-limit (FLOOD_WAIT/429) so the cycle can pause that remote, and whether it was
	// stopped by the user (→ the cycle resumes the same remote next time so teldrive's
	// partial isn't wasted).
	uploadRunner = func(label, taskID, op string, items []transferItem, dst string, opts transferOpts) (int64, int, bool, bool) {
		j := jobs.Create(label, op)
		for _, e := range opts.Extra { // so the Activity shows the capped target, not the whole-source total
			if e.Flag == "--max-transfer" {
				setJobCap(j.ID, int64(parseSize(e.Value))) // value carries a "B" suffix
			}
		}
		runTransfer(j.ID, taskID, op, items, dst, false, opts)
		var moved int64
		var files int
		statsMu.Lock()
		if s := statsStore[j.ID]; s != nil {
			moved, files = s.Bytes, s.Transfers
		}
		statsMu.Unlock()
		return moved, files, floodHit(j.ID), jobs.Status(j.ID) == "stopped"
	}
)

// resumeRemote names a remote whose upload was interrupted (user Stop) with a partial that
// teldrive still holds; the next cycle re-picks it to finish before rotating on. Persisted
// so a restart doesn't lose the resume target.
var resumeRemote string

const uploaderResumeRel = "cache/uploader_resume.json"

func loadResume() { // caller holds upMu
	var s struct {
		Remote string `json:"remote"`
	}
	store.ReadJSON(uploaderResumeRel, &s)
	resumeRemote = s.Remote
}

func setResume(name string) { // caller holds upMu
	if resumeRemote == name {
		return
	}
	resumeRemote = name
	store.WriteJSON(uploaderResumeRel, map[string]string{"remote": name})
}

// uploaderCheck runs one cycle: measure the source, and if it's over threshold,
// move it to the next eligible remote (blocking — uploads run one at a time).
func uploaderCheck(manual bool) {
	upMu.Lock()
	ensureUploader()
	cfg := ucfg
	ledSnap := cloneLedger(ledger)
	upLastMoved = 0 // reset; only a real upload below sets it
	upMu.Unlock()

	now := time.Now()
	if cfg.Source == "" {
		upMu.Lock()
		upLastAt, upLastMsg = now, "no source folder set"
		upMu.Unlock()
		return
	}
	// A manual run refreshes the detailed plan first (which remote gets what, where it
	// stops, size per remote, ETA), then proceeds to upload below.
	if manual {
		pl := storeManualPlan(cfg, ledSnap, now)
		upMu.Lock()
		upLastSize, upLastAt, upLastMsg = pl.SourceBytes, time.Now(), planMsg(pl, cfg.Enabled)
		upMu.Unlock()
	}
	// The enable toggle governs only the PERIODIC scheduler. "Run now" (manual) is an
	// explicit request, so it uploads even while auto-upload is off.
	if !cfg.Enabled && !manual {
		return
	}
	// Window + threshold gate only the automatic scheduler. A manual Run now is an explicit
	// "upload now" and bypasses both (same as the enable toggle above).
	if !inWindow(cfg.AllowedFrom, cfg.AllowedUntil, now) && !manual {
		upMu.Lock()
		upLastAt, upLastMsg = now, "outside upload window"
		upMu.Unlock()
		return
	}
	size := measureSource(cfg.Source)
	thr := int64(parseSize(cfg.Threshold))

	upMu.Lock()
	upLastSize, upLastAt = size, time.Now()
	if thr > 0 && size < thr && !manual {
		upLastMsg = "below threshold"
		upMu.Unlock()
		return
	}
	cursorBefore := seqCursor
	r, free := pickRemote(time.Now()) // advances seqCursor
	if r == nil {
		upLastMsg = "no eligible remote (caps/cooldowns)"
		upMu.Unlock()
		return
	}
	if cfg.DryRun {
		seqCursor = cursorBefore // a dry-run must not disturb the real rotation position
	}
	upLastMsg = "uploading via " + r.Name
	upMu.Unlock()

	op := uploaderOp(cfg)
	items, dst, opts := uploaderRemoteJob(cfg, *r, free) // includes --dry-run when cfg.DryRun

	// Dry-run: run the real rclone --dry-run so the Activity log shows exactly what would
	// move, but touch no state — no service pause, no ledger/history, no autoscan, and the
	// cursor was already restored above.
	if cfg.DryRun {
		uploadRunner("uploader DRY-RUN: "+transferLabel(op, items, dst), r.TaskID, op, items, dst, opts)
		upMu.Lock()
		upLastAt, upLastMsg = time.Now(), "dry-run via "+r.Name+" — nothing moved (see Activity log)"
		upMu.Unlock()
		return
	}

	// Slow down other services (qBittorrent, *arr imports) for the duration of the run.
	applyUploadPause(cfg.Pause)
	runStart := time.Now()
	moved, files, flood, stopped := uploadRunner("uploader: "+transferLabel(op, items, dst), r.TaskID, op, items, dst, opts)
	dur := time.Since(runStart)
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
	upLastMoved = moved // drives the loop's short cooldown → re-check + re-group promptly
	if moved > 0 {      // log the real upload to the persistent history (past sequence)
		recordHistory(r.Name, moved, files, dur, now)
	}
	// Resume bookkeeping: a user Stop leaves a partial teldrive holds, so remember this
	// remote and finish it next cycle (the cursor already advanced at pick time, but the
	// resume override re-picks this remote first). Any clean finish clears the resume.
	switch {
	case stopped:
		setResume(r.Name)
	case r.Name == resumeRemote:
		setResume("") // this remote's resume is done
	}
	if flood { // rate-limited/banned — bench this remote so the next cycle picks another
		pauseRemote(remoteKey(*r), now.Add(uploaderFloodPause))
		upLastMsg = "rate-limited on " + r.Name + " — paused " + uploaderFloodPause.String() + " (moved " + humanBytes(moved) + ")"
	} else if stopped {
		upLastMsg = "stopped on " + r.Name + " after " + humanBytes(moved) + " — will resume there next run (teldrive keeps the partial)"
	} else {
		upLastMsg = "uploaded " + humanBytes(moved) + " / " + strconv.Itoa(files) + " files via " + r.Name
	}
	ledAfter := cloneLedger(ledger)
	upMu.Unlock()

	// A real upload just changed the state → recompute the dry-run plan so the UI's
	// future sequence stays in sync with what actually happened.
	if moved > 0 {
		storeManualPlan(cfg, ledAfter, time.Now())
	}
}

func startUploader() {
	upOnce.Do(func() {
		go func() {
			for {
				upMu.Lock()
				ensureUploader()
				iv := ucfg.IntervalMinutes
				moved := upLastMoved
				upMu.Unlock()
				if iv <= 0 {
					iv = 15
				}
				// After a remote actually uploaded, wait only a short cooldown, then re-check:
				// the source has changed (rclone picks its own files, not our planned grouping),
				// so we re-measure + re-group + rotate to the next remote with a fresh list.
				// When nothing moved (below threshold / all capped), fall back to the interval.
				wait := time.Duration(iv) * time.Minute
				if moved > 0 {
					wait = uploaderCooldown
				}
				time.Sleep(wait)
				uploaderCheck(false) // periodic — respects the enable toggle
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
	c.Sequence = normSequence(c)
	upMu.Lock()
	ensureUploader()
	ucfg = c
	seqCursor = 0 // restart the rotation cursor after a config change
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
		builtin := "running"
		if autoscanSvc().isPaused() {
			builtin = "paused"
		}
		resp["autoscan"] = fmt.Sprintf("built-in: %s · container: %s", builtin, autoscanStatus())
	} else {
		resp["autoscan"] = "not enabled"
	}
	writeJSON(w, http.StatusOK, resp)
}

func uploaderStatus(w http.ResponseWriter, _ *http.Request) {
	upMu.Lock()
	ensureUploader()
	now := time.Now()
	// resolveRemotes so a remote inheriting the shared cap/files shows the effective value,
	// not a bare "∞".
	remotes := make([]map[string]any, 0, len(ucfg.Remotes))
	for _, r := range resolveRemotes(ucfg) {
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
	balNext := projectRotation(ucfg, ledger, seqCursor, 24, resumeRemote)
	resp := map[string]any{
		"enabled": ucfg.Enabled, "source": ucfg.Source, "threshold": ucfg.Threshold,
		"last_size": humanBytes(upLastSize), "last_size_bytes": upLastSize,
		"last_check": nil, "message": upLastMsg, "remotes": remotes, "plan": upLastPlan,
		"checking": upChecking, "history": recentHistory(30), "balance_next": balNext,
		"resume": resumeRemote, // a remote to finish (interrupted upload) before rotating on
	}
	if !upLastAt.IsZero() {
		resp["last_check"] = upLastAt.UTC().Format(time.RFC3339)
	}
	upMu.Unlock()
	writeJSON(w, http.StatusOK, resp)
}

// uploaderRun executes one upload cycle right now ("Run now") — the real move (or the
// rclone --dry-run when dry-run mode is on), regardless of the check interval. Still honours
// the enable toggle, window, threshold and caps.
func uploaderRun(w http.ResponseWriter, _ *http.Request) {
	// Not gated by upChecking: a real run can take hours, and its progress is already shown
	// by the live Activity job + the live Upload plan. Gating it here would leave "Planning…"
	// spinning for the whole upload.
	go uploaderCheck(true)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// uploaderPlan builds the dry-run PLAN only ("Check now") — it measures the source and
// projects the rotation, but never uploads. This is the safe preview; use Run now to
// actually move (or dry-run) files.
func uploaderPlan(w http.ResponseWriter, _ *http.Request) {
	upMu.Lock()
	upChecking = true
	ensureUploader()
	cfg := ucfg
	ledSnap := cloneLedger(ledger)
	upMu.Unlock()
	go func() {
		now := time.Now()
		if cfg.Source == "" {
			upMu.Lock()
			upLastAt, upLastMsg, upChecking = now, "no source folder set", false
			upMu.Unlock()
			return
		}
		pl := storeManualPlan(cfg, ledSnap, now)
		upMu.Lock()
		upLastSize, upLastAt, upLastMsg, upChecking = pl.SourceBytes, time.Now(), planMsg(pl, cfg.Enabled), false
		upMu.Unlock()
	}()
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// resetCaps clears the rolling-window usage for one remote (or all, when name is empty):
// the upload events that count against the daily byte/file cap, the flood bench, and the
// last-upload stamp that drives the gap cooldown. The lifetime Uploaded tally is kept —
// it's the balancer's fill proxy, not a daily quota.
//
// Returns the remotes it touched. Caller must NOT hold upMu.
func resetCaps(name string) []string {
	upMu.Lock()
	defer upMu.Unlock()
	ensureUploader()
	cleared := []string{}
	for key, lr := range ledger {
		if lr == nil || (name != "" && key != name) {
			continue
		}
		lr.Events = nil
		lr.PausedUntil = time.Time{}
		lr.LastUpload = time.Time{}
		cleared = append(cleared, key)
	}
	sort.Strings(cleared)
	store.WriteJSON(uploaderLedgerRel, ledger)
	return cleared
}

// POST /api/uploader/caps/reset — zero today's usage so the rotation starts clean.
// Body: {"remote":"name"} to reset one, or {} / omitted for every remote.
func uploaderResetCaps(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Remote string `json:"remote"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body) // empty body = reset all
	cleared := resetCaps(strings.TrimSpace(body.Remote))
	upMu.Lock()
	upLastMsg = "daily caps reset (" + strconv.Itoa(len(cleared)) + " remote(s))"
	upLastAt = time.Now()
	upMu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "cleared": cleared})
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
		// per-remote gap cooldown — a remote that just uploaded can't be reused until
		// GapMin elapses (so a gap-only backlog still advances instead of stalling).
		if r.GapMin > 0 {
			if gt := lr.LastUpload.Add(time.Duration(r.GapMin) * time.Minute); gt.After(t) {
				t = gt
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
	simUsed := map[string]int64{} // per-remote fill, grown as sim uploads land (for display)
	cur := 0
	pc := pickCtx{seq: cfg.Sequence, cursor: &cur}
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
