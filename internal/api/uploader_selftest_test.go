package api

import (
	"context"
	"os"
	"strings"
	"testing"
)

// find returns the check for a given name, or a zero check.
func findCheck(checks []stCheck, name string) stCheck {
	for _, c := range checks {
		if c.Name == name {
			return c
		}
	}
	return stCheck{}
}

// The config group must not touch the network, so it is fully testable. A readable source,
// a parseable threshold/age, and no window should all pass; the mirror cases must fail/warn
// so the panel actually flags a broken configuration instead of going green regardless.
func TestSelfTestConfigGroup(t *testing.T) {
	// The executor fallback only fires when os.Stat can't see the path (the missing case);
	// pin it to a plain os.Stat so the test never depends on a shell being present.
	orig := execPathKind
	execPathKind = func(_ context.Context, p string) string {
		if fi, err := os.Stat(p); err == nil {
			if fi.IsDir() {
				return "dir"
			}
			return "file"
		}
		return "none"
	}
	t.Cleanup(func() { execPathKind = orig })

	dir := t.TempDir()
	cfg := uploaderConfig{
		Source: dir, Threshold: "500G", IntervalMinutes: 15,
		MinAge: "15m", AllowedFrom: "", AllowedUntil: "",
	}
	checks := uploaderSelfTest(context.Background(), cfg, "config")

	for name, want := range map[string]string{
		"Source folder":   "ok",
		"Upload threshold": "ok",
		"Check interval":  "ok",
		"Min file age":    "ok",
		"Upload window":   "ok",
	} {
		if got := findCheck(checks, name).Status; got != want {
			t.Errorf("%s: status %q, want %q", name, got, want)
		}
	}

	// A missing source is the single most important failure to surface.
	bad := uploaderSelfTest(context.Background(), uploaderConfig{Source: "", Threshold: "500G"}, "config")
	if got := findCheck(bad, "Source folder").Status; got != "fail" {
		t.Errorf("empty source: status %q, want fail", got)
	}

	// A non-existent path must fail, not silently pass.
	missing := uploaderSelfTest(context.Background(), uploaderConfig{Source: dir + "/nope", Threshold: "500G"}, "config")
	if got := findCheck(missing, "Source folder").Status; got != "fail" {
		t.Errorf("missing source: status %q, want fail", got)
	}

	// An unparseable min-age is a hard fail (rclone would reject the flag).
	badAge := uploaderSelfTest(context.Background(), uploaderConfig{Source: dir, Threshold: "500G", MinAge: "banana"}, "config")
	if got := findCheck(badAge, "Min file age").Status; got != "fail" {
		t.Errorf("bad min-age: status %q, want fail", got)
	}

	// An invalid upload window must fail rather than being treated as "anytime".
	badWin := uploaderSelfTest(context.Background(), uploaderConfig{Source: dir, Threshold: "500G", AllowedFrom: "25:99", AllowedUntil: "08:00"}, "config")
	if got := findCheck(badWin, "Upload window").Status; got != "fail" {
		t.Errorf("bad window: status %q, want fail", got)
	}
}

// group filtering must keep the panel and the per-section Verify cheap: asking for "config"
// must not emit destination/pause checks.
func TestSelfTestGroupFilter(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(dir+"/x", []byte("y"), 0o644)
	checks := uploaderSelfTest(context.Background(), uploaderConfig{Source: dir, Threshold: "1G"}, "config")
	for _, c := range checks {
		if c.Group != "config" {
			t.Fatalf("group filter leaked a %q check", c.Group)
		}
	}
}

// The command preview must match a real run: same op, source, remote:subpath, and the
// uploader's layered flags (min-age, delete-empty, cutoff-mode). It shares transferArgv
// with runTransfer, so this guards against the two drifting.
func TestUploaderCommandPreview(t *testing.T) {
	cfg := uploaderConfig{
		Source: "/mnt/local/Media", Op: "move", Subpath: "Media",
		MinAge: "15m", DeleteEmptySrc: true,
		Opts:    transferOpts{Transfers: 4, Tpslimit: 8},
		Remotes: []uploaderRemote{{Name: "tgdrive_main_01"}},
	}
	cmds := uploaderCommands(cfg)
	if len(cmds) != 1 {
		t.Fatalf("got %d commands, want 1", len(cmds))
	}
	c := cmds[0]
	if c.Remote != "tgdrive_main_01" {
		t.Errorf("remote = %q", c.Remote)
	}
	for _, want := range []string{
		"rclone", " move ", "tgdrive_main_01:Media",
		"--transfers 4", "--tpslimit 8",
		"--cutoff-mode cautious", "--min-age 15m", "--delete-empty-src-dirs",
	} {
		if !strings.Contains(c.Cmd, want) {
			t.Errorf("command missing %q\n got: %s", want, c.Cmd)
		}
	}
	// The uploader moves the source folder's CONTENTS straight into remote:subpath — the
	// exact path, no parent, and no name-appending --filter (which would nest Media/Media).
	if !strings.Contains(c.Cmd, "move /mnt/local/Media tgdrive_main_01:Media") {
		t.Errorf("expected a direct 'move <source> <dest>'\n got: %s", c.Cmd)
	}
	if strings.Contains(c.Cmd, "--filter") {
		t.Errorf("uploader command should not carry name-append filters\n got: %s", c.Cmd)
	}

	// Copy mode must surface as a copy command.
	cfg.Op = "copy"
	if cc := uploaderCommands(cfg); !strings.Contains(cc[0].Cmd, " copy ") {
		t.Errorf("copy mode not reflected: %s", cc[0].Cmd)
	}
}
