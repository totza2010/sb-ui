package api

// HTTP intake for the built-in autoscan: manual trigger, Sonarr/Radarr webhook,
// status, and config. See docs/autoscan-plan.md.

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"path"
	"strings"

	"github.com/go-chi/chi/v5"
)

// autoscanTrigger scans the given paths (manual / generic caller).
func autoscanTrigger(w http.ResponseWriter, req *http.Request) {
	var b struct {
		Paths []string `json:"paths"`
	}
	_ = json.NewDecoder(req.Body).Decode(&b)
	if len(b.Paths) == 0 {
		http.Error(w, "paths required", http.StatusBadRequest)
		return
	}
	n := autoscanSvc().Enqueue("manual", b.Paths...)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "queued": n})
}

// autoscanWebhook accepts Sonarr/Radarr "Connect" webhooks (and a generic {paths}
// body). The {token} in the URL must match the configured webhook token — the API is
// otherwise unauthenticated, so this guards the publicly-pointed endpoint.
// webhookAuthorized accepts the autoscan token presented any way an *arr Connect
// webhook can send it: in the URL path, an X-API-Key header, an ?apikey= query
// param, or as the HTTP Basic Auth password (username ignored).
func webhookAuthorized(req *http.Request, token string) bool {
	if token == "" {
		return false
	}
	if chi.URLParam(req, "token") == token ||
		req.Header.Get("X-API-Key") == token || req.Header.Get("Apikey") == token ||
		req.URL.Query().Get("apikey") == token {
		return true
	}
	if _, pass, ok := req.BasicAuth(); ok && pass == token {
		return true
	}
	return false
}

func autoscanWebhook(w http.ResponseWriter, req *http.Request) {
	ac := loadOptions().Autoscan
	if !webhookAuthorized(req, ac.WebhookToken) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	// Mirrors Cloudbox/autoscan's Sonarr + Radarr triggers: the file's folder is
	// series.path/movie.folderPath joined with the file's relativePath (plexScanKey
	// then collapses a file to its directory).
	var b struct {
		EventType string   `json:"eventType"`
		Paths     []string `json:"paths"` // generic caller
		Series    struct {
			Path string `json:"path"`
		} `json:"series"`
		EpisodeFile struct {
			RelativePath string `json:"relativePath"`
		} `json:"episodeFile"`
		RenamedEpisodeFiles []struct {
			PreviousPath string `json:"previousPath"`
			RelativePath string `json:"relativePath"`
		} `json:"renamedEpisodeFiles"`
		Movie struct {
			FolderPath string `json:"folderPath"`
		} `json:"movie"`
		MovieFile struct {
			RelativePath string `json:"relativePath"`
		} `json:"movieFile"`
	}
	_ = json.NewDecoder(req.Body).Decode(&b)

	if strings.EqualFold(b.EventType, "Test") { // Sonarr/Radarr "Test" button — just needs a 2xx
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "test": true})
		return
	}

	paths := append([]string{}, b.Paths...)
	// Sonarr
	if b.Series.Path != "" {
		if b.EpisodeFile.RelativePath != "" {
			paths = append(paths, path.Join(b.Series.Path, b.EpisodeFile.RelativePath))
		} else {
			paths = append(paths, b.Series.Path)
		}
		for _, rf := range b.RenamedEpisodeFiles {
			if rf.PreviousPath != "" {
				paths = append(paths, rf.PreviousPath)
			}
			if rf.RelativePath != "" {
				paths = append(paths, path.Join(b.Series.Path, rf.RelativePath))
			}
		}
	}
	// Radarr
	if b.Movie.FolderPath != "" {
		if b.MovieFile.RelativePath != "" {
			paths = append(paths, path.Join(b.Movie.FolderPath, b.MovieFile.RelativePath))
		} else {
			paths = append(paths, b.Movie.FolderPath)
		}
	}

	n := autoscanSvc().Enqueue("webhook", paths...)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "queued": n})
}

func autoscanStatusHandler(w http.ResponseWriter, _ *http.Request) {
	svc := autoscanSvc()
	writeJSON(w, http.StatusOK, map[string]any{
		"enabled": loadOptions().Autoscan.Enabled,
		"queued":  svc.queueDepth(),
		"counts":  svc.counts(),
		"scans":   svc.recentScans(),
	})
}

func autoscanClear(w http.ResponseWriter, _ *http.Request) {
	autoscanSvc().clear()
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func autoscanGetConfig(w http.ResponseWriter, _ *http.Request) {
	ac := loadOptions().Autoscan
	// Seed sensible defaults on a never-configured instance (token + exclude exts).
	if changed := false; true {
		if ac.WebhookToken == "" {
			ac.WebhookToken = randToken()
			changed = true
		}
		if ac.ExcludeExts == nil {
			ac.ExcludeExts = defaultExcludeExts
			changed = true
		}
		if changed {
			ac = saveAutoscanConfig(ac)
		}
	}
	writeJSON(w, http.StatusOK, ac)
}

func autoscanPutConfig(w http.ResponseWriter, req *http.Request) {
	var ac autoscanConfig
	if json.NewDecoder(req.Body).Decode(&ac) != nil {
		http.Error(w, "bad config", http.StatusBadRequest)
		return
	}
	if ac.WebhookToken == "" {
		ac.WebhookToken = randToken()
	}
	writeJSON(w, http.StatusOK, saveAutoscanConfig(ac))
}

func randToken() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
