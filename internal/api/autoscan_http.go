package api

// HTTP intake for the built-in autoscan: manual trigger, Sonarr/Radarr webhook,
// status, and config. See docs/autoscan-plan.md.

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"

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
func autoscanWebhook(w http.ResponseWriter, req *http.Request) {
	ac := loadOptions().Autoscan
	if ac.WebhookToken == "" || chi.URLParam(req, "token") != ac.WebhookToken {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	var b struct {
		EventType string   `json:"eventType"`
		Paths     []string `json:"paths"`
		Series    struct {
			Path string `json:"path"`
		} `json:"series"`
		Movie struct {
			FolderPath string `json:"folderPath"`
		} `json:"movie"`
	}
	_ = json.NewDecoder(req.Body).Decode(&b)
	if b.EventType == "Test" { // Sonarr/Radarr "Test" button
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "test": true})
		return
	}
	paths := append([]string{}, b.Paths...)
	if b.Series.Path != "" {
		paths = append(paths, b.Series.Path)
	}
	if b.Movie.FolderPath != "" {
		paths = append(paths, b.Movie.FolderPath)
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
