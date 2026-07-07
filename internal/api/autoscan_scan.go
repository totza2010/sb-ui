package api

// Built-in autoscan service (docs/autoscan-plan.md) — a debounced Plex partial-scan
// engine fed by arr webhooks / manual triggers / post-upload. It reuses the existing
// Plex primitives (mapArrPath rewrite, plexSectionForPath match, plexRefreshPath scan);
// its jobs are to coalesce rapid duplicate paths, drive one scan per folder, and keep
// a persistent history with a pending → scanning → completed/skipped/failed lifecycle.

import (
	"context"
	"path"
	"strings"
	"sync"
	"time"

	"sb-ui/internal/executor"
	"sb-ui/internal/store"
)

// Seams (overridden in tests).
var (
	autoscanScanFn = func(cfg plexConfig, sectionID, plexPath string) error {
		return plexRefreshPath(cfg, sectionID, plexPath)
	}
	autoscanSectionFn  = plexSectionForPath
	autoscanScanningFn = plexSectionScanning // is Plex still scanning this section?
	autoscanAnchorFn   = anchorsPresent      // are all anchor files present (mount up)?
	autoscanSaveFn     = func(f scanFile) { store.WriteJSON(autoscanScansRel, f) }
	autoscanGapFn      = func() time.Duration { // min gap between scans (rate limit)
		g := loadOptions().Autoscan.ScanGapSec
		if g <= 0 {
			g = 3
		}
		return time.Duration(g) * time.Second
	}
)

// anchorsPresent reports whether every configured anchor file exists (mount is up).
// Returns the first missing one so the scan record can explain the hold.
func anchorsPresent(anchors []string) (bool, string) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	e := executor.Get()
	for _, a := range anchors {
		if a = strings.TrimSpace(a); a == "" {
			continue
		}
		if ok, _ := e.FileExists(ctx, a); !ok {
			return false, a
		}
	}
	return true, ""
}

const (
	autoscanScansRel = "cache/autoscan_scans.json"
	autoscanScansMax = 500
)

type scanStatus string

const (
	scanPending   scanStatus = "pending"
	scanScanning  scanStatus = "scanning"
	scanCompleted scanStatus = "completed"
	scanSkipped   scanStatus = "skipped" // no matching Plex section
	scanFailed    scanStatus = "failed"
	scanIgnored   scanStatus = "ignored" // webhook received but not scanned (debug log)
)

// scanHit is one webhook/trigger that fed a scan record — several can coalesce into
// one scan, so this preserves what each *arr actually sent.
type scanHit struct {
	Time   time.Time `json:"time"`
	Source string    `json:"source"`
	Event  string    `json:"event,omitempty"`
	Path   string    `json:"path"` // the raw path the caller sent (before rewrite/collapse)
}

type scanRecord struct {
	ID        int64      `json:"id"`
	Path      string     `json:"path"`    // mapped Plex-side folder that gets scanned
	Section   string     `json:"section"` // Plex library section key
	Status    scanStatus `json:"status"`
	Source    string     `json:"source"`          // sonarr / radarr / manual / upload
	Event     string     `json:"event,omitempty"` // arr eventType (Download, Rename, …)
	Error     string     `json:"error,omitempty"`
	Hits      []scanHit  `json:"hits,omitempty"`    // the events that fed this scan
	FireAt    *time.Time `json:"fire_at,omitempty"` // when the debounce elapses and it scans
	CreatedAt time.Time  `json:"created_at"`
	StartedAt *time.Time `json:"started_at,omitempty"`
	EndedAt   *time.Time `json:"ended_at,omitempty"`
}

type scanFile struct {
	NextID  int64        `json:"next_id"`
	Records []scanRecord `json:"records"` // newest first
}

// defaultExcludeExts — subtitles, metadata and artwork changes don't need a Plex
// rescan. Applied to fresh configs (see autoscanGetConfig).
var defaultExcludeExts = []string{"srt", "sub", "ass", "ssa", "idx", "vtt", "nfo", "txt", "jpg", "jpeg", "png", "tbn"}

// autoscanKeep reports whether a raw (arr/local) path should trigger a scan given the
// include/exclude filters. Extensions are matched on the file itself; path filters are
// prefix matches on the raw path (before the file→folder collapse).
func autoscanKeep(raw string, ac autoscanConfig) bool {
	p := strings.TrimSpace(raw)
	if p == "" {
		return false
	}
	if ext := strings.ToLower(strings.TrimPrefix(path.Ext(p), ".")); ext != "" {
		for _, e := range ac.ExcludeExts {
			if strings.EqualFold(strings.TrimPrefix(strings.TrimSpace(e), "."), ext) {
				return false
			}
		}
	}
	for _, ex := range ac.ExcludePaths {
		if ex = strings.TrimSpace(ex); ex != "" && strings.HasPrefix(p, ex) {
			return false
		}
	}
	if inc := nonBlank(ac.IncludePaths); len(inc) > 0 {
		for _, in := range inc {
			if strings.HasPrefix(p, in) {
				return true
			}
		}
		return false
	}
	return true
}

func nonBlank(ss []string) []string {
	out := ss[:0:0]
	for _, s := range ss {
		if strings.TrimSpace(s) != "" {
			out = append(out, strings.TrimSpace(s))
		}
	}
	return out
}

// plexScanKey maps a raw (arr/local) path to the Plex-side directory Plex should
// rescan: apply the path rewrite, then collapse a media file to its folder (Plex
// scans at directory granularity). This is the debounce/dedup key — no network.
func plexScanKey(raw string) string {
	p := mapArrPath(strings.TrimSpace(raw))
	if p == "" {
		return ""
	}
	if plexVideoExtRE.MatchString(p) {
		p = path.Dir(p)
	}
	return strings.TrimRight(p, "/")
}

type autoscanService struct {
	mu      sync.Mutex
	timers  map[string]*time.Timer
	active  map[string]int64 // key → id of the record currently pending/scanning
	records []scanRecord     // newest first, bounded
	nextID  int64
	paused  bool // held (e.g. during an upload); pending scans wait for Resume

	// gate serialises the actual scanning: one scan runs at a time, and when
	// WaitCompletion is on the gate is held until Plex reports the scan finished —
	// so queued scans drain one-completing-at-a-time, then wait the gap, instead of
	// all firing at once. nextScanAt is set (under the gate) after each scan ends.
	gate       sync.Mutex
	nextScanAt time.Time

	lastHook *inboundHook // most recent inbound webhook (any result), for the UI
}

// inboundHook is the last webhook the endpoint received — surfaced in the UI so a
// "nothing happens" report can be diagnosed: did the *arr reach us at all, and what
// did we make of it? Recorded even on auth failure (wrong token/password).
type inboundHook struct {
	At       time.Time `json:"at"`
	Source   string    `json:"source"`             // sonarr / radarr / … / unknown
	Instance string    `json:"instance,omitempty"` // instanceName from the payload
	AppURL   string    `json:"app_url,omitempty"`  // applicationUrl the arr advertised
	Event    string    `json:"event,omitempty"`    // eventType the *arr sent
	Result   string    `json:"result"`             // accepted | ignored | test | disabled | unauthorized | bad-request
	Code     int       `json:"code"`               // HTTP status we replied to the *arr with
	Detail   string    `json:"detail,omitempty"`   // e.g. how many paths queued, or why it was ignored
	Remote   string    `json:"remote,omitempty"`   // caller IP
}

func (s *autoscanService) noteInbound(h inboundHook) {
	// The loopback self-test hits this same endpoint; it's an internal probe, not a
	// real *arr, so keep it out of the inbound indicator and the connection registry.
	if h.Instance == "" && isLoopbackRemote(h.Remote) {
		return
	}
	h.At = time.Now()
	s.mu.Lock()
	s.lastHook = &h
	s.mu.Unlock()
	connReg().upsertInbound(h) // keep the persistent connection registry current
}

func (s *autoscanService) lastInbound() *inboundHook {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastHook
}

// Pause holds the scan queue — pending scans stay pending and new triggers still
// queue, but nothing is sent to Plex until Resume. Used by the uploader to stop
// autoscan from scanning a folder that's mid-move (in-memory; a restart clears it).
func (s *autoscanService) Pause() {
	s.mu.Lock()
	s.paused = true
	for _, t := range s.timers {
		t.Stop()
	}
	s.mu.Unlock()
}

// Resume releases the hold and restarts each queued scan's countdown — a fresh
// debounce, staggered by the scan gap — so after an unpause the rows visibly count
// down again and fire one at a time instead of all scanning at once.
func (s *autoscanService) Resume() {
	delay := autoscanDelay()
	gap := autoscanGapFn()
	now := time.Now()
	s.mu.Lock()
	s.paused = false
	i := 0
	for key := range s.active {
		wait := delay + time.Duration(i)*gap
		fireAt := now.Add(wait)
		id := s.active[key]
		for j := range s.records {
			if s.records[j].ID == id {
				s.records[j].FireAt = &fireAt
				break
			}
		}
		k := key
		if t := s.timers[k]; t != nil { // drop any stale timer armed before/during the pause
			t.Stop()
		}
		s.timers[k] = time.AfterFunc(wait, func() { s.fire(k) })
		i++
	}
	snap := s.snapshotLocked()
	s.mu.Unlock()
	autoscanSaveFn(snap)
}

func (s *autoscanService) isPaused() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.paused
}

func newAutoscanService() *autoscanService {
	s := &autoscanService{
		timers: map[string]*time.Timer{},
		active: map[string]int64{},
	}
	var f scanFile
	store.ReadJSON(autoscanScansRel, &f)
	s.records = f.Records
	s.nextID = f.NextID
	// Records left pending/scanning from a previous run were interrupted by the
	// restart — mark them failed so counts stay honest and nothing sticks.
	for i := range s.records {
		if st := s.records[i].Status; st == scanPending || st == scanScanning {
			s.records[i].Status = scanFailed
			if s.records[i].Error == "" {
				s.records[i].Error = "interrupted (restart)"
			}
		}
		if s.records[i].ID > s.nextID {
			s.nextID = s.records[i].ID
		}
	}
	return s
}

var (
	autoscanOnce sync.Once
	autoscanInst *autoscanService
)

func autoscanSvc() *autoscanService {
	autoscanOnce.Do(func() { autoscanInst = newAutoscanService() })
	return autoscanInst
}

func autoscanDelay() time.Duration {
	d := loadOptions().Autoscan.DelaySec
	if d <= 0 {
		d = 5
	}
	return time.Duration(d) * time.Second
}

func (s *autoscanService) snapshotLocked() scanFile {
	return scanFile{NextID: s.nextID, Records: append([]scanRecord{}, s.records...)}
}

// Enqueue schedules a debounced scan for each raw path; rapid duplicates for the
// same target folder collapse into one pending record. Returns how many were accepted.
func (s *autoscanService) Enqueue(source, event string, raws ...string) int {
	delay := autoscanDelay()
	ac := loadOptions().Autoscan
	now := time.Now()
	n := 0
	s.mu.Lock()
	// While paused (e.g. mid-upload) we still record hits and hold records pending,
	// but arm NO timers — otherwise a timer set during the pause keeps its old
	// countdown and fires early right after Resume. Resume is the sole thing that
	// arms timers after a pause, all with a fresh staggered countdown. FireAt stays
	// nil so the UI shows the row as "held".
	paused := s.paused
	var fireAt *time.Time
	if !paused {
		t := now.Add(delay)
		fireAt = &t
	}
	for _, raw := range raws {
		if !autoscanKeep(raw, ac) {
			continue
		}
		key := plexScanKey(raw)
		if key == "" {
			continue
		}
		n++
		hit := scanHit{Time: now, Source: source, Event: event, Path: raw}
		if id, ok := s.active[key]; ok { // already queued for this folder → record the hit + extend debounce
			for i := range s.records {
				if s.records[i].ID == id {
					s.records[i].Hits = append(s.records[i].Hits, hit)
					s.records[i].FireAt = fireAt // debounce reset (nil while paused; Resume sets it)
					break
				}
			}
			if !paused {
				if t := s.timers[key]; t != nil {
					t.Reset(delay)
				}
			}
			continue
		}
		s.nextID++
		s.records = append([]scanRecord{{ID: s.nextID, Path: key, Status: scanPending, Source: source, Event: event, Hits: []scanHit{hit}, FireAt: fireAt, CreatedAt: now}}, s.records...)
		if len(s.records) > autoscanScansMax {
			s.records = s.records[:autoscanScansMax]
		}
		s.active[key] = s.nextID
		if !paused {
			k := key
			s.timers[k] = time.AfterFunc(delay, func() { s.fire(k) })
		}
	}
	snap := s.snapshotLocked()
	s.mu.Unlock()
	if n > 0 {
		autoscanSaveFn(snap)
	}
	return n
}

// LogIgnored records a webhook event we received but chose not to scan (for the
// debug "log skipped" view — no timer, no scan). ref = the folder the *arr sent.
func (s *autoscanService) LogIgnored(source, event, ref, note string) {
	now := time.Now()
	s.mu.Lock()
	s.nextID++
	s.records = append([]scanRecord{{ID: s.nextID, Path: ref, Status: scanIgnored, Source: source, Event: event, Error: note, Hits: []scanHit{{Time: now, Source: source, Event: event, Path: ref}}, CreatedAt: now}}, s.records...)
	if len(s.records) > autoscanScansMax {
		s.records = s.records[:autoscanScansMax]
	}
	snap := s.snapshotLocked()
	s.mu.Unlock()
	autoscanSaveFn(snap)
}

func (s *autoscanService) fire(key string) {
	s.mu.Lock()
	if s.paused { // held — keep it pending; Resume() will re-fire from active[]
		s.mu.Unlock()
		return
	}
	delete(s.timers, key)
	id := s.active[key]
	delete(s.active, key) // claim this queue slot
	s.mu.Unlock()
	if id == 0 {
		return
	}

	ac := loadOptions().Autoscan
	cfg := loadOptions().Plex

	// Pre-flight before taking a turn in the scan queue — a hold/skip shouldn't
	// occupy the serializer or delay the next real scan. The row stays "pending"
	// (not "scanning") until it actually reaches the head of the queue.
	//
	// Hold if the mount looks down (a required anchor is missing) — a scan against a
	// dropped mount can make Plex trash the whole library.
	if ok, missing := autoscanAnchorFn(ac.Anchors); !ok {
		s.setStatus(id, scanSkipped, "", "mount not ready — missing anchor: "+missing, false)
		return
	}
	if cfg.URL == "" {
		s.setStatus(id, scanFailed, "", "Plex not configured", false)
		return
	}
	secID, ok := autoscanSectionFn(cfg, key)
	if !ok {
		s.setStatus(id, scanSkipped, "", "no Plex section matches — add a path mapping", false)
		return
	}

	// Serialize: only one scan runs at a time. When WaitCompletion is on we hold the
	// gate until Plex reports this scan finished, so the next queued scan waits for
	// the previous to actually complete — then the gap — instead of all starting at
	// once. nextScanAt (set on the way out) spaces the following scan by the gap.
	s.gate.Lock()
	defer func() {
		s.nextScanAt = time.Now().Add(autoscanGapFn())
		s.gate.Unlock()
	}()
	if wait := time.Until(s.nextScanAt); wait > 0 {
		time.Sleep(wait)
	}

	// Paused while we waited our turn → re-queue (held) for Resume, don't scan. This
	// and Resume both run under s.mu, so the re-queue and the re-arm can't race.
	s.mu.Lock()
	if s.paused {
		s.active[key] = id
		for i := range s.records {
			if s.records[i].ID == id {
				s.records[i].FireAt = nil
				break
			}
		}
		snap := s.snapshotLocked()
		s.mu.Unlock()
		autoscanSaveFn(snap)
		return
	}
	s.mu.Unlock()

	s.setStatus(id, scanScanning, secID, "", true)
	switch err := autoscanScanFn(cfg, secID, key); {
	case err != nil:
		s.setStatus(id, scanFailed, secID, err.Error(), false)
	case ac.WaitCompletion:
		s.waitComplete(id, secID, cfg, ac) // poll Plex until it actually finishes
	default:
		s.setStatus(id, scanCompleted, secID, "", false)
	}
}

// waitComplete polls Plex /activities until the section's scan goes idle (or a
// timeout), then marks the record completed. Runs in the fire() goroutine.
func (s *autoscanService) waitComplete(id int64, secID string, cfg plexConfig, ac autoscanConfig) {
	idle := time.Duration(ac.IdleSec) * time.Second
	if idle < 10*time.Second {
		idle = 30 * time.Second
	}
	timeout := time.Duration(ac.TimeoutSec) * time.Second
	if timeout <= 0 {
		timeout = 300 * time.Second
	}
	const grace = 20 * time.Second // if no scan activity ever appears, treat it as instant
	start := time.Now()
	deadline := start.Add(timeout)
	lastActive := start
	sawActive := false
	tick := time.NewTicker(3 * time.Second)
	defer tick.Stop()
	for time.Now().Before(deadline) {
		<-tick.C
		if autoscanScanningFn(cfg, secID) {
			sawActive, lastActive = true, time.Now()
			continue
		}
		if sawActive && time.Since(lastActive) >= idle {
			break
		}
		if !sawActive && time.Since(start) >= grace {
			break
		}
	}
	s.setStatus(id, scanCompleted, secID, "", false)
}

func (s *autoscanService) setStatus(id int64, st scanStatus, section, errMsg string, starting bool) {
	s.mu.Lock()
	now := time.Now()
	for i := range s.records {
		if s.records[i].ID != id {
			continue
		}
		s.records[i].Status = st
		if section != "" {
			s.records[i].Section = section
		}
		s.records[i].Error = errMsg
		if starting {
			s.records[i].StartedAt = &now
		} else {
			s.records[i].EndedAt = &now
		}
		break
	}
	snap := s.snapshotLocked()
	s.mu.Unlock()
	autoscanSaveFn(snap)
}

func (s *autoscanService) recentScans() []scanRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]scanRecord, len(s.records))
	copy(out, s.records)
	return out
}

func (s *autoscanService) counts() map[string]int {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := map[string]int{"pending": 0, "scanning": 0, "completed": 0, "skipped": 0, "failed": 0, "ignored": 0}
	for _, r := range s.records {
		c[string(r.Status)]++
	}
	return c
}

func (s *autoscanService) queueDepth() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.active) // pending scans (accurate even while paused, when no timers are armed)
}

func (s *autoscanService) clear() {
	s.mu.Lock()
	s.records = nil
	snap := s.snapshotLocked()
	s.mu.Unlock()
	autoscanSaveFn(snap)
}
