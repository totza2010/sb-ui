package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"sb-ui/internal/executor"
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

type optionsConfig struct {
	Plex plexConfig `json:"plex"`
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
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// plexCurl calls the Plex API over curl on the host (where Plex lives), asking
// for JSON. Returns (exitcode, body).
func plexCurl(cfg plexConfig, p string) (int, string) {
	u := strings.TrimRight(cfg.URL, "/") + p
	if strings.Contains(p, "?") {
		u += "&"
	} else {
		u += "?"
	}
	u += "X-Plex-Token=" + url.QueryEscape(cfg.Token)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	rc, out, _ := executor.Get().Run(ctx, []string{"curl", "-fsS", "--max-time", "12", "-H", "Accept: application/json", u}, "")
	return rc, out
}

// plexActiveStreams returns the number of active Plex sessions (-1 on error).
func plexActiveStreams(cfg plexConfig) int {
	if cfg.URL == "" {
		return -1
	}
	rc, out := plexCurl(cfg, "/status/sessions")
	if rc != 0 {
		return -1
	}
	var r struct {
		MediaContainer struct {
			Size int `json:"size"`
		} `json:"MediaContainer"`
	}
	if json.Unmarshal([]byte(out), &r) != nil {
		return -1
	}
	return r.MediaContainer.Size
}

type plexSection struct {
	Key   string `json:"key"`
	Title string `json:"title"`
	Type  string `json:"type"`
}

func plexSections(cfg plexConfig) []plexSection {
	rc, out := plexCurl(cfg, "/library/sections")
	if rc != 0 {
		return nil
	}
	var r struct {
		MediaContainer struct {
			Directory []plexSection `json:"Directory"`
		} `json:"MediaContainer"`
	}
	_ = json.Unmarshal([]byte(out), &r)
	return r.MediaContainer.Directory
}

// plexRefreshAll triggers a scan on every library section (post-upload).
func plexRefreshAll(cfg plexConfig) {
	for _, s := range plexSections(cfg) {
		plexCurl(cfg, "/library/sections/"+s.Key+"/refresh")
	}
}

// plexTest reports connectivity: active streams + library sections.
func plexTest(w http.ResponseWriter, _ *http.Request) {
	cfg := loadOptions().Plex
	if cfg.URL == "" {
		http.Error(w, "Plex URL not set", http.StatusBadRequest)
		return
	}
	rc, _ := plexCurl(cfg, "/identity")
	if rc != 0 {
		http.Error(w, "cannot reach Plex (check URL/token)", http.StatusBadGateway)
		return
	}
	secs := plexSections(cfg)
	titles := make([]string, 0, len(secs))
	for _, s := range secs {
		titles = append(titles, s.Title)
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "streams": plexActiveStreams(cfg), "sections": titles})
}
