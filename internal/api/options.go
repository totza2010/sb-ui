package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"sync"

	"sb-ui/internal/store"
)

// Central options (Options page). Plex integration closes the cloudplow loop:
// throttle uploads while people are streaming, and refresh the Plex library after
// an upload finishes (replacing a separate autoscan).

type plexConfig struct {
	URL             string `json:"url"`               // e.g. http://localhost:32400
	Token           string `json:"token"`             // X-Plex-Token
	Throttle        bool   `json:"throttle"`          // pause uploads while streaming
	MaxStreams      int    `json:"max_streams"`       // allowed concurrent streams before pausing
	ScanAfterUpload bool   `json:"scan_after_upload"` // refresh libraries when an upload finishes
}

// pathMapping translates an arr file path to its Plex-side equivalent (their
// library roots can differ, e.g. arr /Media/TV-UHD vs Plex /Media/tvuhd).
type pathMapping struct {
	From string `json:"from"` // arr path prefix
	To   string `json:"to"`   // Plex path prefix
}

type optionsConfig struct {
	Plex         plexConfig    `json:"plex"`
	PathMappings []pathMapping `json:"path_mappings"`
}

// mapArrPath rewrites an arr path to the Plex path using the longest matching
// prefix mapping. Returns the path unchanged when nothing matches.
func mapArrPath(p string) string {
	best := -1
	out := p
	for _, m := range loadOptions().PathMappings {
		if m.From != "" && strings.HasPrefix(p, m.From) && len(m.From) > best {
			best = len(m.From)
			out = m.To + p[len(m.From):]
		}
	}
	return out
}

const optionsRel = "cache/options.json"

var (
	optMu     sync.Mutex
	optCfg    optionsConfig
	optLoaded bool
)

func loadOptions() optionsConfig {
	optMu.Lock()
	defer optMu.Unlock()
	if !optLoaded {
		store.ReadJSON(optionsRel, &optCfg)
		optLoaded = true
	}
	return optCfg
}

func getOptions(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, loadOptions())
}

func putOptions(w http.ResponseWriter, req *http.Request) {
	var c optionsConfig
	if json.NewDecoder(req.Body).Decode(&c) != nil {
		http.Error(w, "bad config", http.StatusBadRequest)
		return
	}
	optMu.Lock()
	optCfg = c
	optLoaded = true
	store.WriteJSON(optionsRel, optCfg)
	optMu.Unlock()
	resetPlexDirs() // Plex URL/token may have changed → rebuild the path index
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// plexSection is one Plex library section. Sections + all other Plex calls are
// served by the plexgo client (see plexclient.go).
type plexSection struct {
	Key       string   `json:"key"`
	Title     string   `json:"title"`
	Type      string   `json:"type"`
	Locations []string `json:"locations,omitempty"` // library root paths
}

// plexTest reports connectivity: library sections + active streams, via plexgo.
func plexTest(w http.ResponseWriter, req *http.Request) {
	cfg := loadOptions().Plex
	// Allow testing the values currently in the form (before Save).
	var b struct {
		URL   string `json:"url"`
		Token string `json:"token"`
	}
	if json.NewDecoder(req.Body).Decode(&b); strings.TrimSpace(b.URL) != "" {
		cfg.URL = strings.TrimSpace(b.URL)
		if strings.TrimSpace(b.Token) != "" {
			cfg.Token = strings.TrimSpace(b.Token)
		}
	}
	if cfg.URL == "" {
		http.Error(w, "Plex URL not set", http.StatusBadRequest)
		return
	}
	secs := plexSections(cfg)
	if len(secs) == 0 {
		http.Error(w, "cannot reach Plex or no library sections (check URL/token)", http.StatusBadGateway)
		return
	}
	titles := make([]string, 0, len(secs))
	for _, s := range secs {
		titles = append(titles, s.Title)
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "streams": plexActiveStreams(cfg), "sections": titles})
}
