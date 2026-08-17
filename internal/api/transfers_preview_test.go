package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"sb-ui/internal/jobs"
	"sb-ui/internal/rcloneexec"
)

func postPreview(t *testing.T, body string) (int, []string) {
	t.Helper()
	rec := httptest.NewRecorder()
	rclonePreview(rec, httptest.NewRequest("POST", "/api/rclone/preview", strings.NewReader(body)))
	var out struct {
		Commands []string `json:"commands"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out.Commands
}

// The whole point: the preview is produced by the same code that runs the transfer, so it
// cannot describe a command that won't happen. Previously the UI built this string from a
// TypeScript copy of the flag rules, and a drift between the copies meant the preview lied.
func TestPreviewMatchesWhatWouldActuallyRun(t *testing.T) {
	body := `{"op":"move","items":[{"path":"/mnt/local/Media/TV","is_dir":true}],
	          "dst":"rem:Media","dry_run":true,
	          "opts":{"transfers":8,"bwlimit":"40M","fast_list":true}}`

	code, cmds := postPreview(t, body)
	if code != http.StatusOK {
		t.Fatalf("preview returned %d, want 200", code)
	}
	if len(cmds) != 1 {
		t.Fatalf("got %d commands, want 1", len(cmds))
	}

	// Build the same request through the executor's own renderer and compare, so this test
	// fails if the endpoint ever starts assembling argv on its own.
	want := strings.Join(rcloneexec.Argv(rcloneConfPath(), "move",
		[]transferItem{{Path: "/mnt/local/Media/TV", IsDir: true}}, "rem:Media", true,
		transferOpts{Transfers: 8, Bwlimit: "40M", FastList: true})[0], " ")
	if cmds[0] != want {
		t.Errorf("preview does not match the real argv:\n preview: %s\n actual:  %s", cmds[0], want)
	}

	// And it really is the flags the caller asked for.
	for _, f := range []string{"--dry-run", "--transfers 8", "--bwlimit 40M", "--fast-list", "--filter + /TV"} {
		if !strings.Contains(cmds[0], f) {
			t.Errorf("preview missing %q:\n%s", f, cmds[0])
		}
	}
}

// Items from different parents run as separate commands; the preview shows all of them rather
// than pretending a multi-remote transfer is one call.
func TestPreviewShowsEveryCommand(t *testing.T) {
	_, cmds := postPreview(t, `{"op":"copy","items":[
		{"path":"/srv/a/one","is_dir":true},
		{"path":"gd:other/two","is_dir":true}],"dst":"rem:"}`)
	if len(cmds) != 2 {
		t.Fatalf("got %d commands, want 2 (two different parents)", len(cmds))
	}
}

// A preview is for input still being typed, so incomplete input is not an error — it just has
// nothing to show. Returning 400 here would make the dialog flash errors while you work.
func TestPreviewToleratesIncompleteInput(t *testing.T) {
	for _, body := range []string{
		`{"op":"copy","items":[],"dst":"rem:"}`,
		`{"op":"copy","items":[{"path":"/a/b"}],"dst":""}`,
		`{"op":"copy"}`,
	} {
		code, cmds := postPreview(t, body)
		if code != http.StatusOK {
			t.Errorf("incomplete input returned %d, want 200 (body %s)", code, body)
		}
		if len(cmds) != 0 {
			t.Errorf("incomplete input should render nothing, got %v", cmds)
		}
	}
}

// An operation outside the whitelist is refused, by the same map the run path uses.
func TestPreviewRejectsUnknownOps(t *testing.T) {
	for _, op := range []string{"delete", "purge", "", "MOVE"} {
		code, _ := postPreview(t, `{"op":"`+op+`","items":[{"path":"/a/b"}],"dst":"rem:"}`)
		if code != http.StatusBadRequest {
			t.Errorf("op %q returned %d, want 400", op, code)
		}
	}
}

// The preview must not run anything. Nothing here can start a process — the renderer is pure —
// but assert no job was created, since that is the failure that would matter.
func TestPreviewCreatesNoJob(t *testing.T) {
	before := len(jobs.ListDicts())
	postPreview(t, `{"op":"move","items":[{"path":"/a/b","is_dir":true}],"dst":"rem:x"}`)
	if after := len(jobs.ListDicts()); after != before {
		t.Errorf("preview created %d job(s); it must only describe, never run", after-before)
	}
}
