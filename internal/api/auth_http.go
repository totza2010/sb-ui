package api

import (
	"encoding/json"
	"net"
	"net/http"
	"strings"

	"sb-ui/internal/auth"
)

// Authentication for the API.
//
// The gap this closes: the deployed unit binds :9180 on every interface and Traefik fronts it
// with Authelia, so the browser path was protected but the raw port was not. Anything on the
// host or the LAN could POST /api/rclone/transfer and move or delete files without
// authenticating at all. Borrowing safety from a reverse proxy only works while the network is
// arranged exactly right; the server has to be able to say no on its own.
//
// Deny by default on /api and /ws, with four deliberate exceptions:
//
//   - /api/health, so an uptime check needs no credentials
//   - /api/auth/*, or logging in would require being logged in
//   - the autoscan webhook, which arr calls and which carries its own token
//   - loopback, unless switched off: reaching 127.0.0.1 already means shell access on the
//     host, and exempting it keeps local scripts and the on-host CLI working
//
// Anything else needs the session cookie (a person) or the API token (a script).

const sessionCookie = "sb_ui_session"

// TrustLoopbackEnv can be set to "false" to require credentials even from 127.0.0.1.
const TrustLoopbackEnv = "SB_UI_TRUST_LOOPBACK"

var trustLoopback = true // set from the environment in SetTrustLoopback

// SetTrustLoopback configures whether loopback callers skip authentication.
func SetTrustLoopback(v bool) { trustLoopback = v }

// authExempt reports whether a path is reachable without credentials.
func authExempt(p string) bool {
	switch {
	case p == "/api/health":
		return true
	case strings.HasPrefix(p, "/api/auth/"):
		return true
	case strings.HasPrefix(p, "/api/autoscan/webhook"):
		// Carries its own token; arr has no way to hold ours.
		return true
	}
	return false
}

// guarded reports whether a path is behind authentication at all. Everything that is not the
// API or a websocket is the SPA itself, which must load so the login page can be shown.
func guarded(p string) bool {
	return strings.HasPrefix(p, "/api/") || strings.HasPrefix(p, "/ws/")
}

func isLoopback(remoteAddr string) bool {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = remoteAddr
	}
	ip := net.ParseIP(strings.Trim(host, "[]"))
	return ip != nil && ip.IsLoopback()
}

// presentedToken pulls an API token out of the request, accepting the two shapes callers
// reach for: a bearer header and the X-API-Key header arr-style tools already use.
func presentedToken(req *http.Request) string {
	if h := req.Header.Get("Authorization"); h != "" {
		if v, ok := strings.CutPrefix(h, "Bearer "); ok {
			return strings.TrimSpace(v)
		}
	}
	return strings.TrimSpace(req.Header.Get("X-API-Key"))
}

// authed reports whether this request carries a valid credential.
func authed(req *http.Request) bool {
	if c, err := req.Cookie(sessionCookie); err == nil && auth.ValidSession(c.Value) {
		return true
	}
	if t := presentedToken(req); t != "" && auth.CheckToken(t) {
		return true
	}
	return false
}

// RequireAuth is the middleware that enforces the rules above.
func RequireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		p := req.URL.Path
		if !guarded(p) || authExempt(p) || (trustLoopback && isLoopback(req.RemoteAddr)) {
			next.ServeHTTP(w, req)
			return
		}
		if authed(req) {
			next.ServeHTTP(w, req)
			return
		}
		// 401 rather than a redirect: every caller here is either fetch() — which handles this
		// itself — or a script, and a redirect to HTML would just confuse both.
		writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "authentication required"})
	})
}

// ── endpoints ─────────────────────────────────────────────────────────────────

// authStatus tells the UI what it needs to decide between showing the app and showing a login
// form, without revealing anything a caller couldn't already infer.
func authStatus(w http.ResponseWriter, req *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"password_set":    auth.HasPassword(),
		"token_set":       auth.TokenConfigured(),
		"authenticated":   authed(req) || (trustLoopback && isLoopback(req.RemoteAddr)),
		"trust_loopback":  trustLoopback,
		"loopback_client": isLoopback(req.RemoteAddr),
	})
}

func authLogin(w http.ResponseWriter, req *http.Request) {
	var b struct {
		Password string `json:"password"`
	}
	_ = json.NewDecoder(req.Body).Decode(&b)
	if !auth.HasPassword() {
		http.Error(w, "no password is set — run: sb-ui --set-password", http.StatusPreconditionFailed)
		return
	}
	if !auth.CheckPassword(b.Password) {
		// One message for a wrong password whether or not anything else is configured, so the
		// response can't be used to probe the setup.
		writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "incorrect password"})
		return
	}
	setSessionCookie(w, req, auth.IssueSession())
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func authLogout(w http.ResponseWriter, req *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Value: "", Path: "/", MaxAge: -1,
		HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: isHTTPS(req),
	})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// authRotate signs every session out, including this one.
func authRotate(w http.ResponseWriter, req *http.Request) {
	auth.RotateSecret()
	authLogout(w, req)
}

func setSessionCookie(w http.ResponseWriter, req *http.Request, v string) {
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Value: v, Path: "/",
		MaxAge:   int(auth.SessionTTL.Seconds()),
		HttpOnly: true, // never readable from JS, so an XSS can't lift the session
		SameSite: http.SameSiteLaxMode,
		Secure:   isHTTPS(req),
	})
}

// isHTTPS reports whether the client's connection is encrypted, honouring the forwarded
// header Traefik sets — the hop to us is plain HTTP even when the browser is on HTTPS, and
// marking the cookie Secure on a plain-HTTP LAN visit would stop it being sent at all.
func isHTTPS(req *http.Request) bool {
	if req.TLS != nil {
		return true
	}
	return strings.EqualFold(req.Header.Get("X-Forwarded-Proto"), "https")
}
