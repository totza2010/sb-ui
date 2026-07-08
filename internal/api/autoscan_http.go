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
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
)

// DefaultAddr is the canonical loopback bind used when SB_UI_ADDR is unset. The port is
// FIXED (9180) so arr webhooks and the tsdproxy target stay valid across redeploys — the
// Saltbox role pins the same port (see deploy/.../sbui). DefaultPort is that port alone.
const (
	DefaultPort = "9180"
	DefaultAddr = "127.0.0.1:" + DefaultPort
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
	return DefaultPort
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
	remote := clientIP(req)
	// Read + identify the caller BEFORE the auth check, so even a rejected webhook is
	// recorded against the right connection (e.g. "Sonarr reached us but token wrong").
	body, _ := io.ReadAll(req.Body)
	var meta struct {
		EventType      string   `json:"eventType"`
		Paths          []string `json:"paths"` // generic caller
		InstanceName   string   `json:"instanceName"`
		ApplicationURL string   `json:"applicationUrl"`
	}
	_ = json.Unmarshal(body, &meta)
	scan, matched := parseArrWebhook(body)
	source := scan.Source
	if source == "" {
		if len(meta.Paths) > 0 {
			source = "generic"
		} else {
			source = "unknown"
		}
	}
	// record notes the inbound (updates last-seen indicator + connection registry).
	record := func(result string, code int, event, detail string) {
		autoscanSvc().noteInbound(inboundHook{Source: source, Instance: meta.InstanceName, AppURL: meta.ApplicationURL,
			Event: event, Result: result, Code: code, Detail: detail, Remote: remote})
	}

	if !webhookAuthorized(req, ac.WebhookToken) {
		record("unauthorized", http.StatusForbidden, meta.EventType, "token/password did not match")
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	if strings.EqualFold(meta.EventType, "Test") { // arr "Test" button — just needs a 2xx
		record("test", http.StatusOK, "Test", "test payload — connection OK")
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "test": true})
		return
	}

	// Master switch: acknowledge the webhook but don't scan while autoscan is off.
	// (The manual "Scan now" box stays usable so config can be tested.)
	if !ac.Enabled {
		record("disabled", http.StatusOK, meta.EventType, "autoscan is turned off")
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "queued": 0, "disabled": true})
		return
	}

	// Extract the folders to scan (per-app parsers in autoscan_arr.go).
	paths := append(append([]string{}, meta.Paths...), scan.Paths...)
	if len(paths) == 0 { // unknown app or an event we don't scan on — acknowledge, do nothing
		if matched && ac.LogSkipped { // debug: record what the *arr sent (e.g. a series-level rename)
			autoscanSvc().LogIgnored(scan.Source, scan.Event, scan.Ref, "")
		}
		record("ignored", http.StatusOK, meta.EventType, "no scannable path in this event")
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "queued": 0, "ignored": meta.EventType})
		return
	}
	n := autoscanSvc().Enqueue(source, scan.Event, paths...)
	record("accepted", http.StatusOK, scan.Event, strconv.Itoa(n)+" path(s) queued")
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "queued": n})
}

// clientIP returns the best-effort caller IP (honours X-Forwarded-For from Traefik).
func clientIP(req *http.Request) string {
	if xff := req.Header.Get("X-Forwarded-For"); xff != "" {
		if i := strings.IndexByte(xff, ','); i > 0 {
			return strings.TrimSpace(xff[:i])
		}
		return strings.TrimSpace(xff)
	}
	if host, _, err := net.SplitHostPort(req.RemoteAddr); err == nil {
		return host
	}
	return req.RemoteAddr
}

func autoscanStatusHandler(w http.ResponseWriter, _ *http.Request) {
	svc := autoscanSvc()
	writeJSON(w, http.StatusOK, map[string]any{
		"enabled":      loadOptions().Autoscan.Enabled,
		"paused":       svc.isPaused(),
		"queued":       svc.queueDepth(),
		"counts":       svc.counts(),
		"scans":        svc.recentScans(),
		"port":         serverPort(), // real port arr must hit (not the browser origin's)
		"last_inbound": svc.lastInbound(),
		"connections":  connReg().list(),
	})
}

// autoscanConnCheck actively probes every discovered *arr's API now and returns the
// refreshed connection list (the "test the connection / why did it drop" button).
func autoscanConnCheck(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "connections": connReg().probeAll()})
}

// autoscanManualAdd registers an *arr sb-ui can't auto-discover (scaffold — see addManual).
func autoscanManualAdd(w http.ResponseWriter, req *http.Request) {
	var b struct {
		Source string `json:"source"`
		Name   string `json:"name"`
		URL    string `json:"url"`
		APIKey string `json:"api_key"`
	}
	if json.NewDecoder(req.Body).Decode(&b) != nil || strings.TrimSpace(b.URL) == "" || strings.TrimSpace(b.APIKey) == "" {
		http.Error(w, "url and api_key are required", http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(b.Name) == "" {
		b.Name = b.Source
	}
	connReg().addManual(b.Source, b.Name, b.URL, b.APIKey)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func autoscanClear(w http.ResponseWriter, _ *http.Request) {
	autoscanSvc().clear()
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// autoscanDeleteScan removes a single history row by id.
func autoscanDeleteScan(w http.ResponseWriter, req *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(req, "id"), 10, 64)
	if err != nil {
		http.Error(w, "bad id", http.StatusBadRequest)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": autoscanSvc().deleteRecord(id)})
}

// autoscanPause / autoscanResume let other subsystems (and the UI) hold the scan
// queue — e.g. the uploader holds scans while it moves files, then releases.
func autoscanPause(w http.ResponseWriter, _ *http.Request) {
	autoscanSvc().Pause()
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "paused": true})
}

func autoscanResume(w http.ResponseWriter, _ *http.Request) {
	autoscanSvc().Resume()
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "paused": false})
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
