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

// seerrConfig points at a Jellyseerr/Overseerr instance — used ONLY to submit
// requests (its core job). Discover/detail metadata comes from TMDB directly.
// Multiple instances (jellyseerr, seerr, …) are stored in cache/seerr_instances.json;
// this legacy single entry is migrated into that list on first use (see seerr.go).
type seerrConfig struct {
	Name    string `json:"name,omitempty"`    // container/instance name (multi-instance)
	URL     string `json:"url"`               // e.g. https://requests.example.com
	APIKey  string `json:"api_key"`           // X-Api-Key
	Default bool   `json:"default,omitempty"` // the instance used for Discover requests
}

// tmdbConfig holds a TMDb v3 API key — the source of all Discover display metadata.
type tmdbConfig struct {
	APIKey string `json:"api_key"`
}

// autoscanConfig drives the built-in autoscan (docs/autoscan-plan.md): a debounced
// Plex partial-scan service fed by arr webhooks / manual triggers / post-upload.
// Path rewriting reuses the top-level PathMappings (mapArrPath).
type autoscanConfig struct {
	Enabled      bool   `json:"enabled"`
	DelaySec     int    `json:"delay_sec"`     // debounce window before a path is scanned; default 5
	ScanGapSec   int    `json:"scan_gap_sec"`  // min gap between consecutive scans (rate limit); default 3
	OnUpload     bool   `json:"on_upload"`     // scan the moved paths after an uploader run
	WebhookToken string `json:"webhook_token"` // shared secret embedded in the arr webhook URL
	LogSkipped   bool   `json:"log_skipped"`   // also record webhook events we don't scan (debug)
	// Anchors — absolute files that must exist before a scan is sent (autoplow-style).
	// If any is missing the mount is considered down and the scan is held, so Plex
	// won't remove items when a rclone mount drops.
	Anchors []string `json:"anchors"`
	// Completion detection — poll Plex /activities so a scan only shows Completed once
	// Plex has actually finished (not just when the refresh was triggered).
	WaitCompletion bool `json:"wait_completion"`
	IdleSec        int  `json:"idle_sec"`    // no scan activity for this long = done (default 30)
	TimeoutSec     int  `json:"timeout_sec"` // give up waiting after this (default 300)
	// Filtering (autoplow-style) — drop events that don't warrant a Plex scan.
	ExcludeExts  []string `json:"exclude_exts"`  // file extensions to ignore (srt, nfo, …)
	ExcludePaths []string `json:"exclude_paths"` // path prefixes to ignore
	IncludePaths []string `json:"include_paths"` // if set, only paths under one of these scan
	// WebhookEvents — which *arr Connection triggers "Wire & test" enables (canonical
	// keys: import, upgrade, rename, delete). Empty = import+upgrade+rename.
	WebhookEvents []string `json:"webhook_events"`
}

type optionsConfig struct {
	Plex         plexConfig     `json:"plex"`
	PathMappings []pathMapping  `json:"path_mappings"`
	Seerr        seerrConfig    `json:"seerr"`
	Tmdb         tmdbConfig     `json:"tmdb"`
	Qbit         qbitConn       `json:"qbit"`     // qBittorrent WebUI (used by the uploader's block module)
	Autoscan     autoscanConfig `json:"autoscan"` // built-in autoscan service
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

// saveAutoscanConfig patches only the Autoscan field of the persisted options and
// returns the stored value (so the autoscan endpoints don't clobber the rest).
func saveAutoscanConfig(ac autoscanConfig) autoscanConfig {
	optMu.Lock()
	defer optMu.Unlock()
	if !optLoaded {
		store.ReadJSON(optionsRel, &optCfg)
		optLoaded = true
	}
	optCfg.Autoscan = ac
	store.WriteJSON(optionsRel, optCfg)
	return optCfg.Autoscan
}

func putOptions(w http.ResponseWriter, req *http.Request) {
	var c optionsConfig
	if json.NewDecoder(req.Body).Decode(&c) != nil {
		http.Error(w, "bad config", http.StatusBadRequest)
		return
	}
	optMu.Lock()
	if !optLoaded {
		store.ReadJSON(optionsRel, &optCfg)
		optLoaded = true
	}
	c.Autoscan = optCfg.Autoscan // autoscan is managed only via /api/autoscan/config
	optCfg = c
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
