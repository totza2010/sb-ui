package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestWebhookAuthorized(t *testing.T) {
	const tok = "secret123"
	mk := func(f func(*http.Request)) *http.Request {
		r := httptest.NewRequest("POST", "/api/autoscan/webhook", nil)
		f(r)
		return r
	}
	cases := []struct {
		name string
		r    *http.Request
		want bool
	}{
		{"header", mk(func(r *http.Request) { r.Header.Set("X-API-Key", tok) }), true},
		{"query", mk(func(r *http.Request) { r.URL.RawQuery = "apikey=" + tok }), true},
		{"basic", mk(func(r *http.Request) { r.SetBasicAuth("anyuser", tok) }), true},
		{"basic-wrong", mk(func(r *http.Request) { r.SetBasicAuth("u", "nope") }), false},
		{"none", mk(func(*http.Request) {}), false},
	}
	for _, c := range cases {
		if got := webhookAuthorized(c.r, tok); got != c.want {
			t.Errorf("%s: webhookAuthorized = %v, want %v", c.name, got, c.want)
		}
	}
	if webhookAuthorized(mk(func(r *http.Request) { r.Header.Set("X-API-Key", "x") }), "") {
		t.Error("empty configured token must reject everything")
	}
}

// setOptForTest replaces the cached options for the duration of a test.
func setOptForTest(t *testing.T, c optionsConfig) {
	t.Helper()
	optMu.Lock()
	prev, prevLoaded := optCfg, optLoaded
	optCfg, optLoaded = c, true
	optMu.Unlock()
	t.Cleanup(func() {
		optMu.Lock()
		optCfg, optLoaded = prev, prevLoaded
		optMu.Unlock()
	})
}

// noPersist disables the store write so tests don't touch disk.
func noPersist(t *testing.T) {
	t.Helper()
	prev := autoscanSaveFn
	autoscanSaveFn = func(scanFile) {}
	t.Cleanup(func() { autoscanSaveFn = prev })
}

// noThrottle removes the inter-scan gap so fire() runs synchronously in tests.
func noThrottle(t *testing.T) {
	t.Helper()
	prev := autoscanGapFn
	autoscanGapFn = func() time.Duration { return 0 }
	t.Cleanup(func() { autoscanGapFn = prev })
}

func TestPlexScanKey(t *testing.T) {
	setOptForTest(t, optionsConfig{PathMappings: []pathMapping{{From: "/mnt/local", To: "/mnt/unionfs"}}})
	cases := map[string]string{
		"/mnt/local/Media/TV/Show/S01/ep.mkv": "/mnt/unionfs/Media/TV/Show/S01", // file → folder + rewrite
		"/mnt/local/Media/Movies/Film (2020)": "/mnt/unionfs/Media/Movies/Film (2020)",
		"/other/path/":                        "/other/path", // no mapping, trailing slash trimmed
		"":                                    "",
	}
	for in, want := range cases {
		if got := plexScanKey(in); got != want {
			t.Errorf("plexScanKey(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestAutoscanCoalesce(t *testing.T) {
	noPersist(t)
	noThrottle(t)
	// Long debounce so nothing fires during the test — we only assert coalescing.
	setOptForTest(t, optionsConfig{Autoscan: autoscanConfig{DelaySec: 3600}})
	s := newAutoscanService()

	s.Enqueue("webhook", "", "/m/TV/Show/a.mkv", "/m/TV/Show/b.mkv") // same folder → 1 record
	s.Enqueue("webhook", "", "/m/Movies/Film")                       // distinct → 2nd record
	s.Enqueue("webhook", "", "/m/TV/Show/c.mkv")                     // same folder again → still 2

	if d := s.queueDepth(); d != 2 {
		t.Fatalf("queueDepth = %d, want 2 (coalesced)", d)
	}
	if got := len(s.recentScans()); got != 2 {
		t.Fatalf("records = %d, want 2", got)
	}
	if c := s.counts()["pending"]; c != 2 {
		t.Fatalf("pending count = %d, want 2", c)
	}
}

func TestAutoscanFireCompleted(t *testing.T) {
	noPersist(t)
	noThrottle(t)
	setOptForTest(t, optionsConfig{Plex: plexConfig{URL: "http://plex:32400"}, Autoscan: autoscanConfig{DelaySec: 3600}})

	var gotSection, gotPath string
	prevScan, prevSection := autoscanScanFn, autoscanSectionFn
	autoscanSectionFn = func(_ plexConfig, _ string) (string, bool) { return "3", true }
	autoscanScanFn = func(_ plexConfig, section, p string) error { gotSection, gotPath = section, p; return nil }
	t.Cleanup(func() { autoscanScanFn, autoscanSectionFn = prevScan, prevSection })

	s := newAutoscanService()
	s.Enqueue("manual", "", "/mnt/unionfs/Media/TV/Show/ep.mkv")
	key := plexScanKey("/mnt/unionfs/Media/TV/Show/ep.mkv")
	s.fire(key)

	if gotSection != "3" || gotPath != key {
		t.Fatalf("scan called with (%q,%q), want (\"3\",%q)", gotSection, gotPath, key)
	}
	recs := s.recentScans()
	if len(recs) != 1 || recs[0].Status != scanCompleted || recs[0].Section != "3" {
		t.Fatalf("want 1 completed record §3, got %+v", recs)
	}
	if recs[0].StartedAt == nil || recs[0].EndedAt == nil {
		t.Fatalf("expected started/ended timestamps set")
	}
}

func TestAutoscanAnchorHold(t *testing.T) {
	noPersist(t)
	noThrottle(t)
	setOptForTest(t, optionsConfig{Plex: plexConfig{URL: "http://plex:32400"}, Autoscan: autoscanConfig{DelaySec: 3600, Anchors: []string{"/mnt/unionfs/mounted.bin"}}})
	prevAnchor, prevScan := autoscanAnchorFn, autoscanScanFn
	scanned := false
	autoscanAnchorFn = func([]string) (bool, string) { return false, "/mnt/unionfs/mounted.bin" } // mount down
	autoscanScanFn = func(plexConfig, string, string) error { scanned = true; return nil }
	t.Cleanup(func() { autoscanAnchorFn, autoscanScanFn = prevAnchor, prevScan })

	s := newAutoscanService()
	s.Enqueue("manual", "", "/mnt/unionfs/Media/TV/Show/ep.mkv")
	s.fire(plexScanKey("/mnt/unionfs/Media/TV/Show/ep.mkv"))

	if scanned {
		t.Fatal("must not scan Plex while an anchor is missing")
	}
	if recs := s.recentScans(); len(recs) != 1 || recs[0].Status != scanSkipped {
		t.Fatalf("want 1 skipped (mount not ready) record, got %+v", recs)
	}
}

func TestAutoscanPauseHold(t *testing.T) {
	noPersist(t)
	noThrottle(t)
	setOptForTest(t, optionsConfig{Plex: plexConfig{URL: "http://plex:32400"}, Autoscan: autoscanConfig{DelaySec: 3600}})
	scanned := false
	prevScan, prevSection := autoscanScanFn, autoscanSectionFn
	autoscanSectionFn = func(plexConfig, string) (string, bool) { return "1", true }
	autoscanScanFn = func(plexConfig, string, string) error { scanned = true; return nil }
	t.Cleanup(func() { autoscanScanFn, autoscanSectionFn = prevScan, prevSection })

	s := newAutoscanService()
	s.Pause()
	s.Enqueue("upload", "", "/m/Show/ep.mkv")
	key := plexScanKey("/m/Show/ep.mkv")
	s.fire(key) // paused → held, not scanned
	if scanned {
		t.Fatal("must not scan while paused")
	}
	if recs := s.recentScans(); len(recs) != 1 || recs[0].Status != scanPending {
		t.Fatalf("want record still pending while paused, got %+v", recs)
	}
	// resume (set the flag directly to avoid the async re-arm, then drive fire)
	s.mu.Lock()
	s.paused = false
	s.mu.Unlock()
	s.fire(key)
	if !scanned {
		t.Fatal("must scan after resume")
	}
}

// While paused, Enqueue must not arm timers (a timer armed mid-pause keeps its old
// countdown and would fire early right after Resume). Resume re-arms every queued
// scan with a fresh, staggered countdown.
func TestAutoscanPauseNoTimersResumeReArms(t *testing.T) {
	noPersist(t)
	setOptForTest(t, optionsConfig{Autoscan: autoscanConfig{DelaySec: 3600, ScanGapSec: 5}})

	s := newAutoscanService()
	s.Pause()
	s.Enqueue("upload", "", "/m/A/ep.mkv")
	s.Enqueue("upload", "", "/m/B/ep.mkv")

	s.mu.Lock()
	nTimers, nActive := len(s.timers), len(s.active)
	var heldFire bool
	for _, r := range s.records {
		if r.FireAt != nil {
			heldFire = true
		}
	}
	s.mu.Unlock()
	if nTimers != 0 {
		t.Fatalf("no timers should be armed while paused, got %d", nTimers)
	}
	if nActive != 2 {
		t.Fatalf("both scans should be queued (active), got %d", nActive)
	}
	if heldFire {
		t.Fatal("FireAt must be nil (held) while paused")
	}

	s.Resume()
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.timers) != 2 {
		t.Fatalf("Resume must re-arm a timer per queued scan, got %d", len(s.timers))
	}
	for _, r := range s.records {
		if r.Status == scanPending && r.FireAt == nil {
			t.Fatalf("Resume must set a fresh FireAt on each pending record: %+v", r)
		}
	}
}

func TestAutoscanFireSkipped(t *testing.T) {
	noPersist(t)
	noThrottle(t)
	setOptForTest(t, optionsConfig{Plex: plexConfig{URL: "http://plex:32400"}, Autoscan: autoscanConfig{DelaySec: 3600}})
	prev := autoscanSectionFn
	autoscanSectionFn = func(_ plexConfig, _ string) (string, bool) { return "", false }
	t.Cleanup(func() { autoscanSectionFn = prev })

	s := newAutoscanService()
	s.Enqueue("webhook", "", "/unmapped/path")
	s.fire(plexScanKey("/unmapped/path"))

	if recs := s.recentScans(); len(recs) != 1 || recs[0].Status != scanSkipped {
		t.Fatalf("want 1 skipped record, got %+v", recs)
	}
}

func TestAutoscanKeep(t *testing.T) {
	ac := autoscanConfig{ExcludeExts: []string{"nfo", ".srt"}, ExcludePaths: []string{"/mnt/junk"}, IncludePaths: []string{"/mnt/media"}}
	cases := map[string]bool{
		"/mnt/media/Show/ep.mkv":  true,
		"/mnt/media/Show/ep.nfo":  false, // excluded ext
		"/mnt/media/Show/sub.srt": false, // excluded ext (dot-prefixed rule)
		"/mnt/other/ep.mkv":       false, // not under an include path
		"/mnt/junk/ep.mkv":        false, // excluded path
		"":                        false,
	}
	for in, want := range cases {
		if got := autoscanKeep(in, ac); got != want {
			t.Errorf("autoscanKeep(%q) = %v, want %v", in, got, want)
		}
	}
	// exclude-only (no include list) keeps everything except the excludes
	ex := autoscanConfig{ExcludeExts: []string{"nfo"}, ExcludePaths: []string{"/mnt/junk"}}
	if !autoscanKeep("/anywhere/ep.mkv", ex) || autoscanKeep("/mnt/junk/ep.mkv", ex) {
		t.Fatal("exclude-only filtering wrong")
	}
}

func TestAutoscanEnqueueFilter(t *testing.T) {
	noPersist(t)
	noThrottle(t)
	setOptForTest(t, optionsConfig{Autoscan: autoscanConfig{DelaySec: 3600, ExcludeExts: []string{"nfo"}}})
	s := newAutoscanService()
	if n := s.Enqueue("webhook", "", "/m/Show/ep.mkv", "/m/Show/ep.nfo"); n != 1 {
		t.Fatalf("Enqueue accepted %d, want 1 (.nfo filtered)", n)
	}
}

func TestParseArrWebhook(t *testing.T) {
	cases := []struct {
		name, body, wantSource string
		wantPath               string // first scan path (empty = no match / no scan)
	}{
		{"sonarr download", `{"eventType":"Download","series":{"path":"/tv/Show"},"episodeFile":{"relativePath":"Season 1/ep.mkv"}}`, "sonarr", "/tv/Show/Season 1/ep.mkv"},
		{"radarr download", `{"eventType":"Download","movie":{"folderPath":"/movies/Film (2020)"},"movieFile":{"relativePath":"Film.mkv"}}`, "radarr", "/movies/Film (2020)/Film.mkv"},
		{"sonarr seriesdelete", `{"eventType":"SeriesDelete","series":{"path":"/tv/Show"}}`, "sonarr", "/tv/Show"},
		{"sonarr download no-file (no show-root scan)", `{"eventType":"Download","series":{"path":"/tv/Show"}}`, "sonarr", ""},
		{"sonarr rename", `{"eventType":"Rename","series":{"path":"/tv/Show"},"renamedEpisodeFiles":[{"relativePath":"Season 1/ep.mkv"}]}`, "sonarr", "/tv/Show/Season 1/ep.mkv"},
		{"lidarr download", `{"eventType":"Download","artist":{"path":"/music/Artist"},"trackFiles":[{"path":"/music/Artist/Album/t.flac"}]}`, "lidarr", "/music/Artist/Album/t.flac"},
		{"sonarr grab (no scan)", `{"eventType":"Grab","series":{"path":"/tv/Show"}}`, "sonarr", ""},
		{"unknown", `{"eventType":"Download","foo":{"bar":1}}`, "", ""},
	}
	for _, c := range cases {
		s, ok := parseArrWebhook([]byte(c.body))
		if c.wantSource == "" {
			if ok {
				t.Errorf("%s: expected no match, got %+v", c.name, s)
			}
			continue
		}
		if !ok || s.Source != c.wantSource {
			t.Errorf("%s: source = %q (ok=%v), want %q", c.name, s.Source, ok, c.wantSource)
		}
		got := ""
		if len(s.Paths) > 0 {
			got = s.Paths[0]
		}
		if got != c.wantPath {
			t.Errorf("%s: path = %q, want %q", c.name, got, c.wantPath)
		}
	}
}

func TestAutoscanEnqueueCount(t *testing.T) {
	noPersist(t)
	noThrottle(t)
	setOptForTest(t, optionsConfig{Autoscan: autoscanConfig{DelaySec: 3600}})
	s := newAutoscanService()
	if n := s.Enqueue("manual", "", "/m/A/x.mkv", "", "/m/B"); n != 2 {
		t.Fatalf("Enqueue accepted %d, want 2 (blank skipped)", n)
	}
}
