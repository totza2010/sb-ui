package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"sb-ui/internal/auth"
)

// reached wraps RequireAuth around a handler that records whether it ran, so each case asks
// the only question that matters: did this request get through to the API?
func reached(t *testing.T, req *http.Request) (bool, int) {
	t.Helper()
	got := false
	h := RequireAuth(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		got = true
		w.WriteHeader(http.StatusOK)
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return got, rec.Code
}

// remote builds a request that looks like it came from another machine — the case the deployed
// unit is actually exposed to, since it binds :9180 on every interface.
func remote(method, path string) *http.Request {
	req := httptest.NewRequest(method, path, nil)
	req.RemoteAddr = "192.168.1.50:54321"
	return req
}

func withLoopbackTrust(t *testing.T, v bool) {
	t.Helper()
	prev := trustLoopback
	trustLoopback = v
	t.Cleanup(func() { trustLoopback = prev })
}

// The gap this all exists to close: before this, a stranger on the LAN could POST a transfer
// and move or delete files without authenticating.
func TestUnauthenticatedRequestsAreRefused(t *testing.T) {
	auth.UseMemoryStore()
	t.Setenv(auth.TokenEnv, "")
	withLoopbackTrust(t, true)

	for _, path := range []string{
		"/api/rclone/transfer", "/api/tasks", "/api/uploader/status",
		"/api/apps", "/api/config/settings", "/ws/jobs/abc",
	} {
		ok, code := reached(t, remote("POST", path))
		if ok {
			t.Errorf("%s was reachable with no credentials", path)
		}
		if code != http.StatusUnauthorized {
			t.Errorf("%s returned %d, want 401", path, code)
		}
	}
}

// A fresh install with nothing configured must refuse strangers rather than admit them — that
// is what makes deny-by-default safe to ship, and the owner can still get in over loopback.
func TestFreshInstallRefusesStrangersButAllowsTheHost(t *testing.T) {
	auth.UseMemoryStore()
	t.Setenv(auth.TokenEnv, "")
	withLoopbackTrust(t, true)

	if ok, _ := reached(t, remote("GET", "/api/tasks")); ok {
		t.Error("an unconfigured install must not be open to the network")
	}
	local := httptest.NewRequest("GET", "/api/tasks", nil)
	local.RemoteAddr = "127.0.0.1:40000"
	if ok, _ := reached(t, local); !ok {
		t.Error("loopback should still work, or the owner cannot set a password")
	}
}

func TestAPITokenLetsScriptsIn(t *testing.T) {
	auth.UseMemoryStore()
	t.Setenv(auth.TokenEnv, "tok-abcdef123456")
	withLoopbackTrust(t, true)

	// Both header shapes callers reach for.
	for _, set := range []func(*http.Request){
		func(r *http.Request) { r.Header.Set("Authorization", "Bearer tok-abcdef123456") },
		func(r *http.Request) { r.Header.Set("X-API-Key", "tok-abcdef123456") },
	} {
		req := remote("GET", "/api/tasks")
		set(req)
		if ok, code := reached(t, req); !ok {
			t.Errorf("a valid token was refused (%d)", code)
		}
	}

	// Anything else is not the token.
	for _, bad := range []string{"", "tok-abcdef12345", "Bearer", "bearer tok-abcdef123456"} {
		req := remote("GET", "/api/tasks")
		req.Header.Set("Authorization", bad)
		if ok, _ := reached(t, req); ok {
			t.Errorf("Authorization %q was accepted", bad)
		}
	}
}

func TestSessionCookieLetsTheBrowserIn(t *testing.T) {
	auth.UseMemoryStore()
	t.Setenv(auth.TokenEnv, "")
	withLoopbackTrust(t, true)

	req := remote("GET", "/api/tasks")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: auth.IssueSession()})
	if ok, code := reached(t, req); !ok {
		t.Errorf("a valid session cookie was refused (%d)", code)
	}

	// A cookie this server never signed must not work.
	forged := remote("GET", "/api/tasks")
	forged.AddCookie(&http.Cookie{Name: sessionCookie, Value: "99999999999.deadbeef"})
	if ok, _ := reached(t, forged); ok {
		t.Error("a forged session cookie was accepted")
	}
}

// arr posts to the autoscan webhook and has no way to hold our credentials, so that path stays
// open — it authenticates with its own token instead. Health checks likewise.
func TestExemptPathsStayReachable(t *testing.T) {
	auth.UseMemoryStore()
	t.Setenv(auth.TokenEnv, "")
	withLoopbackTrust(t, false) // even with loopback trust off

	for _, path := range []string{
		"/api/health",
		"/api/autoscan/webhook/sometoken",
		"/api/autoscan/webhook",
		"/api/auth/status",
		"/api/auth/login",
	} {
		if ok, code := reached(t, remote("POST", path)); !ok {
			t.Errorf("%s must be reachable without credentials, got %d", path, code)
		}
	}
}

// The SPA itself has to load, or there is nowhere to show a login form.
func TestTheAppItselfIsNotBehindTheGate(t *testing.T) {
	auth.UseMemoryStore()
	t.Setenv(auth.TokenEnv, "")
	withLoopbackTrust(t, false)

	for _, path := range []string{"/", "/transfers", "/assets/index.js", "/favicon.svg"} {
		if ok, code := reached(t, remote("GET", path)); !ok {
			t.Errorf("%s must be served without credentials, got %d", path, code)
		}
	}
}

// Loopback trust is a deliberate convenience — reaching 127.0.0.1 already implies shell access
// — but it must be switchable off for anyone who wants credentials even locally.
func TestLoopbackTrustCanBeTurnedOff(t *testing.T) {
	auth.UseMemoryStore()
	t.Setenv(auth.TokenEnv, "")

	local := func() *http.Request {
		r := httptest.NewRequest("GET", "/api/tasks", nil)
		r.RemoteAddr = "127.0.0.1:40000"
		return r
	}

	withLoopbackTrust(t, true)
	if ok, _ := reached(t, local()); !ok {
		t.Error("loopback should be allowed when trusted")
	}

	withLoopbackTrust(t, false)
	if ok, code := reached(t, local()); ok {
		t.Errorf("loopback should be refused when untrusted, got %d", code)
	}
	// IPv6 loopback counts too.
	withLoopbackTrust(t, true)
	v6 := httptest.NewRequest("GET", "/api/tasks", nil)
	v6.RemoteAddr = "[::1]:40000"
	if ok, _ := reached(t, v6); !ok {
		t.Error("::1 is loopback and should be allowed when trusted")
	}
}

// Logging in issues a cookie the browser can actually keep: not readable from JS, and not
// marked Secure on a plain-HTTP LAN visit (which would stop it being sent at all).
func TestLoginIssuesAUsableCookie(t *testing.T) {
	auth.UseMemoryStore()
	if err := auth.SetPassword("a-good-password"); err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	authLogin(rec, requestWithJSON("POST", "/api/auth/login", `{"password":"a-good-password"}`))

	if rec.Code != http.StatusOK {
		t.Fatalf("login returned %d, want 200", rec.Code)
	}
	cookies := rec.Result().Cookies()
	var c *http.Cookie
	for _, k := range cookies {
		if k.Name == sessionCookie {
			c = k
		}
	}
	if c == nil {
		t.Fatal("login did not set a session cookie")
	}
	if !c.HttpOnly {
		t.Error("the session cookie must be HttpOnly so an XSS cannot lift it")
	}
	if c.Secure {
		t.Error("plain-HTTP request must not get a Secure cookie, or it is never sent back")
	}
	if !auth.ValidSession(c.Value) {
		t.Error("the cookie value is not a session this server would accept")
	}

	// ...and the wrong password gets nothing.
	rec2 := httptest.NewRecorder()
	authLogin(rec2, requestWithJSON("POST", "/api/auth/login", `{"password":"wrong"}`))
	if rec2.Code != http.StatusUnauthorized {
		t.Errorf("a wrong password returned %d, want 401", rec2.Code)
	}
	for _, k := range rec2.Result().Cookies() {
		if k.Name == sessionCookie && k.Value != "" {
			t.Error("a failed login must not set a session")
		}
	}
}

// Behind Traefik the hop to us is plain HTTP while the browser is on HTTPS, so the forwarded
// header is what decides whether the cookie may be marked Secure.
func TestForwardedHTTPSMarksTheCookieSecure(t *testing.T) {
	auth.UseMemoryStore()
	_ = auth.SetPassword("a-good-password")

	req := requestWithJSON("POST", "/api/auth/login", `{"password":"a-good-password"}`)
	req.Header.Set("X-Forwarded-Proto", "https")
	rec := httptest.NewRecorder()
	authLogin(rec, req)

	for _, c := range rec.Result().Cookies() {
		if c.Name == sessionCookie && !c.Secure {
			t.Error("a request forwarded as https should get a Secure cookie")
		}
	}
}

func requestWithJSON(method, path, body string) *http.Request {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.RemoteAddr = "192.168.1.50:54321"
	req.Header.Set("Content-Type", "application/json")
	return req
}
