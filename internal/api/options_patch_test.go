package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// putOptions used to replace the stored config with whatever the request carried, so any field
// the form didn't send was reset to its zero value. Two fields had already been lost that way
// and were rescued by hand. This is the same defect that, in the task handler, turned a dry-run
// into a real transfer.
func TestOptionsPatchKeepsUnmentionedFields(t *testing.T) {
	optMu.Lock()
	optCfg = optionsConfig{
		Plex:     plexConfig{URL: "http://plex:32400", Token: "secret-token"},
		Seerr:    seerrConfig{URL: "http://seerr:5055", APIKey: "seerr-key"},
		Tmdb:     tmdbConfig{APIKey: "tmdb-key"},
		Qbit:     qbitConn{URL: "http://qbit:8080", User: "admin", Pass: "pw"},
		Autoscan: autoscanConfig{Enabled: true, WebhookToken: "keep-this-token"},
		Teldrive: teldriveConfig{DSN: "postgres://kept"},
	}
	optLoaded = true
	optMu.Unlock()

	// A request that only changes the Plex URL.
	rec := httptest.NewRecorder()
	putOptions(rec, httptest.NewRequest("PUT", "/api/options",
		strings.NewReader(`{"plex":{"url":"http://plex-new:32400"}}`)))
	if rec.Code != http.StatusOK {
		t.Fatalf("putOptions returned %d, want 200", rec.Code)
	}

	got := loadOptions()
	if got.Plex.URL != "http://plex-new:32400" {
		t.Errorf("plex url = %q, want the new one", got.Plex.URL)
	}
	// Everything the request never mentioned survives — including the token inside the same
	// nested object, which a wholesale replace would have blanked.
	if got.Plex.Token != "secret-token" {
		t.Error("the Plex token was lost by an edit that never mentioned it")
	}
	for _, c := range []struct {
		name, got, want string
	}{
		{"seerr url", got.Seerr.URL, "http://seerr:5055"},
		{"seerr key", got.Seerr.APIKey, "seerr-key"},
		{"tmdb key", got.Tmdb.APIKey, "tmdb-key"},
		{"qbit url", got.Qbit.URL, "http://qbit:8080"},
		{"qbit pass", got.Qbit.Pass, "pw"},
	} {
		if c.got != c.want {
			t.Errorf("%s = %q, want %q kept", c.name, c.got, c.want)
		}
	}
}

// autoscan and teldrive are owned by their own endpoints. GET /options hands them to the UI,
// so the UI echoes them back on save; this endpoint must ignore them however they arrive, or a
// stale copy in a browser tab would overwrite what the owning endpoint has since stored.
func TestOptionsIgnoresFieldsItDoesNotOwn(t *testing.T) {
	optMu.Lock()
	optCfg = optionsConfig{
		Autoscan: autoscanConfig{Enabled: true, WebhookToken: "owned-by-autoscan"},
		Teldrive: teldriveConfig{DSN: "postgres://owned-by-teldrive"},
	}
	optLoaded = true
	optMu.Unlock()

	rec := httptest.NewRecorder()
	putOptions(rec, httptest.NewRequest("PUT", "/api/options", strings.NewReader(
		`{"autoscan":{"enabled":false,"webhook_token":"hijacked"},"teldrive":{"dsn":"postgres://hijacked"}}`)))

	got := loadOptions()
	if !got.Autoscan.Enabled || got.Autoscan.WebhookToken != "owned-by-autoscan" {
		t.Errorf("autoscan must not be settable here, got %+v", got.Autoscan)
	}
	if got.Teldrive.DSN != "postgres://owned-by-teldrive" {
		t.Errorf("teldrive must not be settable here, got %q", got.Teldrive.DSN)
	}
}

func TestOptionsRejectsMalformedBody(t *testing.T) {
	optMu.Lock()
	optCfg = optionsConfig{Plex: plexConfig{URL: "http://keep"}}
	optLoaded = true
	optMu.Unlock()

	rec := httptest.NewRecorder()
	putOptions(rec, httptest.NewRequest("PUT", "/api/options", strings.NewReader(`{"plex":`)))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("malformed body returned %d, want 400", rec.Code)
	}
	if loadOptions().Plex.URL != "http://keep" {
		t.Error("a refused request must leave the stored config untouched")
	}
}

// The autoscan handler had the same defect, and its scar is the webhook token it had to
// regenerate. Patching removes the cause; the token invariant stays because arr posts to a URL
// containing it.
func TestAutoscanPatchKeepsUnmentionedFieldsAndNeverBlanksTheToken(t *testing.T) {
	optMu.Lock()
	optCfg = optionsConfig{Autoscan: autoscanConfig{
		Enabled: true, OnUpload: true, WebhookToken: "existing-token",
	}}
	optLoaded = true
	optMu.Unlock()

	// Change one flag; everything else must survive.
	rec := httptest.NewRecorder()
	autoscanPutConfig(rec, httptest.NewRequest("PUT", "/api/autoscan/config",
		strings.NewReader(`{"on_upload":false}`)))
	if rec.Code != http.StatusOK {
		t.Fatalf("autoscanPutConfig returned %d, want 200", rec.Code)
	}

	got := loadOptions().Autoscan
	if got.OnUpload {
		t.Error("on_upload should have been turned off")
	}
	if !got.Enabled {
		t.Error("enabled was lost by an edit that never mentioned it")
	}
	if got.WebhookToken != "existing-token" {
		t.Errorf("webhook token = %q, want the existing one kept", got.WebhookToken)
	}

	// Explicitly clearing the token is not allowed to leave it empty — arr's webhook URL
	// contains it.
	rec2 := httptest.NewRecorder()
	autoscanPutConfig(rec2, httptest.NewRequest("PUT", "/api/autoscan/config",
		strings.NewReader(`{"webhook_token":""}`)))
	if after := loadOptions().Autoscan.WebhookToken; after == "" {
		t.Error("the webhook token must never end up empty")
	} else if after == "existing-token" {
		t.Error("an explicit clear should have produced a new token, not kept the old one")
	}
}
