package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
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

func TestAutoscanFireSkipped(t *testing.T) {
	noPersist(t)
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
	setOptForTest(t, optionsConfig{Autoscan: autoscanConfig{DelaySec: 3600}})
	s := newAutoscanService()
	if n := s.Enqueue("manual", "", "/m/A/x.mkv", "", "/m/B"); n != 2 {
		t.Fatalf("Enqueue accepted %d, want 2 (blank skipped)", n)
	}
}
