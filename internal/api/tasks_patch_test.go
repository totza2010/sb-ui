package api

import (
	"strings"
	"testing"
)

// The server owns the task record; a request says what to change, not what the whole task is.
// Editing used to decode into an empty Task and replace the stored one, which made the
// browser's form the system of record: any field the client didn't send was reset to its zero
// value. That is how a dry-run task became a real one — a hot reload cleared the form's
// dry_run, the save sent false, and the next scheduled run copied files for real.
func TestPatchKeepsFieldsTheRequestDoesNotMention(t *testing.T) {
	stored := Task{
		ID: "t1", CreatedAt: "2026-01-01T00:00:00Z", Name: "Nightly",
		Op: "copy", Items: []transferItem{{Path: "/src/movie", IsDir: true}}, Dst: "rem:Media",
		DryRun: true, Schedule: "15 20 * * *", RunMode: "queue",
		Opts: transferOpts{Bwlimit: "1k", Transfers: 4},
	}

	// A request that only changes the schedule — exactly the edit that lost dry_run.
	got, ok := applyTaskPatch(stored, []byte(`{"schedule":"21 20 * * *"}`))
	if !ok {
		t.Fatal("patch rejected a valid schedule change")
	}
	if got.Schedule != "21 20 * * *" {
		t.Errorf("schedule = %q, want the new one", got.Schedule)
	}
	if !got.DryRun {
		t.Error("dry_run was cleared by an edit that never mentioned it — this is the bug that copied files for real")
	}
	for _, c := range []struct {
		name        string
		got, stored any
	}{
		{"name", got.Name, stored.Name},
		{"op", got.Op, stored.Op},
		{"dst", got.Dst, stored.Dst},
		{"run_mode", got.RunMode, stored.RunMode},
		{"opts.bwlimit", got.Opts.Bwlimit, stored.Opts.Bwlimit},
		{"opts.transfers", got.Opts.Transfers, stored.Opts.Transfers},
	} {
		if c.got != c.stored {
			t.Errorf("%s = %v, want %v kept", c.name, c.got, c.stored)
		}
	}
	if len(got.Items) != 1 || got.Items[0].Path != "/src/movie" {
		t.Errorf("items were not kept: %+v", got.Items)
	}
}

// Turning dry-run off has to be said explicitly. That is the point: it becomes a deliberate
// act rather than something an incomplete request can do by omission.
func TestPatchAppliesExplicitFalse(t *testing.T) {
	stored := Task{ID: "t1", Op: "copy", Items: []transferItem{{Path: "/src/a"}}, Dst: "rem:", DryRun: true}
	got, ok := applyTaskPatch(stored, []byte(`{"dry_run":false}`))
	if !ok {
		t.Fatal("patch rejected an explicit dry_run change")
	}
	if got.DryRun {
		t.Error("an explicit dry_run:false must switch the task to a real transfer")
	}
}

// Clearing a schedule is an explicit "" — the frontend sends every field, empty ones included,
// precisely so that removing a schedule still works under patch semantics.
func TestPatchClearsScheduleWhenSentEmpty(t *testing.T) {
	stored := Task{ID: "t1", Op: "move", Items: []transferItem{{Path: "/src/a"}}, Dst: "rem:", Schedule: "0 3 * * *"}
	got, ok := applyTaskPatch(stored, []byte(`{"schedule":""}`))
	if !ok {
		t.Fatal("patch rejected clearing the schedule")
	}
	if got.Schedule != "" {
		t.Errorf("schedule = %q, want it cleared", got.Schedule)
	}
}

// opts is replaced as a unit when mentioned: a half-sent options object must not silently
// inherit the other half of the old one.
func TestPatchReplacesOptsWholesaleWhenMentioned(t *testing.T) {
	stored := Task{
		ID: "t1", Op: "copy", Items: []transferItem{{Path: "/src/a"}}, Dst: "rem:",
		Opts: transferOpts{Bwlimit: "1k", Transfers: 4, FastList: true},
	}
	got, _ := applyTaskPatch(stored, []byte(`{"opts":{"transfers":8}}`))
	if got.Opts.Transfers != 8 {
		t.Errorf("transfers = %d, want 8", got.Opts.Transfers)
	}
	if got.Opts.Bwlimit != "" || got.Opts.FastList {
		t.Errorf("opts should be replaced wholesale, got %+v", got.Opts)
	}
	// ...but leaving opts out entirely keeps it untouched.
	kept, _ := applyTaskPatch(stored, []byte(`{"dst":"rem:Other"}`))
	if kept.Opts.Bwlimit != "1k" || kept.Opts.Transfers != 4 || !kept.Opts.FastList {
		t.Errorf("opts should be kept when unmentioned, got %+v", kept.Opts)
	}
}

// Identity is the server's, not the client's.
func TestPatchIgnoresClientIdentity(t *testing.T) {
	stored := Task{ID: "real", CreatedAt: "2026-01-01T00:00:00Z", Op: "copy", Items: []transferItem{{Path: "/a"}}, Dst: "rem:"}
	got, _ := applyTaskPatch(stored, []byte(`{"id":"spoofed","created_at":"1999-01-01T00:00:00Z"}`))
	if got.ID != "real" || got.CreatedAt != "2026-01-01T00:00:00Z" {
		t.Errorf("client must not set id/created_at, got id=%q created=%q", got.ID, got.CreatedAt)
	}
}

// A patch that would leave the task unrunnable is refused, and the stored task comes back
// unchanged rather than half-applied.
func TestPatchRejectsInvalidResult(t *testing.T) {
	stored := Task{ID: "t1", Op: "copy", Items: []transferItem{{Path: "/a"}}, Dst: "rem:", DryRun: true}
	for _, body := range []string{
		`{"op":"delete"}`,         // not a transfer op
		`{"dst":"--flag"}`,        // not an endpoint
		`{"schedule":"nonsense"}`, // not a cron
		`{"items":[]}`,            // nothing to transfer
		`{`,                       // malformed
	} {
		got, ok := applyTaskPatch(stored, []byte(body))
		if ok {
			t.Errorf("patch %s should have been refused", body)
		}
		if got.Op != stored.Op || got.Dst != stored.Dst || !got.DryRun {
			t.Errorf("refused patch %s must leave the task untouched, got %+v", body, got)
		}
	}
}

// A dry run has to be recognisable as one at a glance, in Activity and in history forever —
// a preview and a real transfer used to be labelled identically.
func TestTransferLabelMarksDryRuns(t *testing.T) {
	items := []transferItem{{Path: "/mnt/local/Media/TV", IsDir: true}}

	real := transferLabel("move", items, "rem:Media", false)
	if strings.Contains(strings.ToUpper(real), "DRY") {
		t.Errorf("a real transfer must not be labelled as a dry run: %q", real)
	}
	if !strings.Contains(real, "move") || !strings.Contains(real, "rem:Media") {
		t.Errorf("label should say what moves where, got %q", real)
	}

	dry := transferLabel("move", items, "rem:Media", true)
	if !strings.Contains(dry, "DRY-RUN") {
		t.Errorf("a dry run must say so in its label: %q", dry)
	}
	if !strings.Contains(dry, real) {
		t.Errorf("the dry-run label should still describe the transfer: %q", dry)
	}
}
