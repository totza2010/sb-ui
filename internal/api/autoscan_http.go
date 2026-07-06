package api

// HTTP intake for the built-in autoscan: manual trigger, Sonarr/Radarr webhook,
// status, and config. See docs/autoscan-plan.md.

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
)

// serverAddr is the address the HTTP server bound to (set from main). Used to build
// the real webhook URL — the browser origin is the Traefik/Authelia front, not the
// port arr must hit directly.
var serverAddr string

// SetListenAddr records the bind address so the UI can show the true webhook port.
func SetListenAddr(a string) { serverAddr = a }

func serverPort() string {
	if _, port, err := net.SplitHostPort(serverAddr); err == nil && port != "" {
		return port
	}
	return "8000"
}

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
	n := autoscanSvc().Enqueue("manual", "", b.Paths...)
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
	body, _ := io.ReadAll(req.Body)

	var meta struct {
		EventType string   `json:"eventType"`
		Paths     []string `json:"paths"` // generic caller
	}
	_ = json.Unmarshal(body, &meta)

	if strings.EqualFold(meta.EventType, "Test") { // arr "Test" button — just needs a 2xx
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "test": true})
		return
	}

	// Master switch: acknowledge the webhook but don't scan while autoscan is off.
	// (The manual "Scan now" box stays usable so config can be tested.)
	if !ac.Enabled {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "queued": 0, "disabled": true})
		return
	}

	// Detect the *arr and extract the folders to scan (per-app parsers in autoscan_arr.go).
	scan, matched := parseArrWebhook(body)
	source := "webhook"
	if scan.Source != "" {
		source = scan.Source
	}
	paths := append(append([]string{}, meta.Paths...), scan.Paths...)
	if len(paths) == 0 { // unknown app or an event we don't scan on — acknowledge, do nothing
		if matched && ac.LogSkipped { // debug: record what the *arr sent (e.g. a series-level rename)
			autoscanSvc().LogIgnored(scan.Source, scan.Event, scan.Ref, "")
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "queued": 0, "ignored": meta.EventType})
		return
	}
	n := autoscanSvc().Enqueue(source, scan.Event, paths...)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "queued": n})
}

func autoscanStatusHandler(w http.ResponseWriter, _ *http.Request) {
	svc := autoscanSvc()
	writeJSON(w, http.StatusOK, map[string]any{
		"enabled": loadOptions().Autoscan.Enabled,
		"queued":  svc.queueDepth(),
		"counts":  svc.counts(),
		"scans":   svc.recentScans(),
		"port":    serverPort(), // real port arr must hit (not the browser origin's)
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
