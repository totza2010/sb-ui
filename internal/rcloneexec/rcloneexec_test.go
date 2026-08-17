package rcloneexec

import (
	"strconv"
	"strings"
	"testing"
)

// The reason this type exists. rclone parses a bare --max-transfer number as KiB, so
// formatting a byte count without a unit multiplies the limit by 1024 — that is how a run
// once pushed roughly a terabyte past a 500 G daily cap and got the remote rate-limited.
func TestBytesFlagAlwaysCarriesAUnit(t *testing.T) {
	const g = int64(1) << 30
	for _, n := range []int64{0, 1, 5 * g, 500 * g, 1 << 40} {
		got := Bytes(n).Flag()
		if want := strconv.FormatInt(n, 10) + "B"; got != want {
			t.Errorf("Bytes(%d).Flag() = %q, want %q", n, got, want)
		}
		// The invariant, stated independently of the implementation: a value that ends in a
		// digit is read by rclone as KiB.
		if last := got[len(got)-1]; last >= '0' && last <= '9' {
			t.Errorf("Bytes(%d).Flag() = %q ends in a digit — rclone would read it as KiB", n, got)
		}
	}
}

func TestClassifyExit(t *testing.T) {
	cases := []struct {
		code   int
		failed bool
		capped bool
		what   string
	}{
		{0, false, false, "success"},
		{ExitMaxTransfer, false, true, "--max-transfer reached — the designed stop for a capped run"},
		{ExitNoTransfer, false, false, "nothing needed transferring"},
		{1, true, false, "usage error"},
		{5, true, false, "temporary error"},
		{7, true, false, "fatal error"},
	}
	for _, c := range cases {
		failed, capped := ClassifyExit(c.code)
		if failed != c.failed || capped != c.capped {
			t.Errorf("ClassifyExit(%d) = (failed=%v capped=%v), want (failed=%v capped=%v) — %s",
				c.code, failed, capped, c.failed, c.capped, c.what)
		}
	}
}

// Items sharing a parent become ONE rclone command with per-item --filter rules, so rclone
// moves them in parallel while each keeps its own name under the destination. A bare
// `move parent dst` would merge the parent's whole contents instead.
func TestArgvGroupsItemsByParent(t *testing.T) {
	items := []Item{
		{Path: "/mnt/local/Media/TV", IsDir: true},
		{Path: "/mnt/local/Media/Movies", IsDir: true},
		{Path: "gd:Other/Docs", IsDir: true},
	}
	argvs := Argv("/etc/rclone.conf", "move", items, "rem:Media", false, Opts{})
	if len(argvs) != 2 {
		t.Fatalf("got %d commands, want 2 (two parents: the local Media dir and gd:Other)", len(argvs))
	}

	first := strings.Join(argvs[0], " ")
	// Source is the shared parent, not the individual items.
	if !strings.Contains(first, "move /mnt/local/Media rem:Media") {
		t.Errorf("first command should move the shared parent to the destination, got:\n%s", first)
	}
	// Both same-parent items are selected in that one command.
	for _, want := range []string{"--filter + /TV", "--filter + /TV/**", "--filter + /Movies", "--filter + /Movies/**"} {
		if !strings.Contains(first, want) {
			t.Errorf("first command missing %q, got:\n%s", want, first)
		}
	}
	// Everything unnamed is excluded, so the group moves exactly its own items.
	if !strings.HasSuffix(strings.TrimSpace(first), "--filter - *") {
		t.Errorf("first command must end by excluding everything else, got:\n%s", first)
	}
	// The item on another remote cannot share a command.
	if second := strings.Join(argvs[1], " "); !strings.Contains(second, "move gd:Other rem:Media") {
		t.Errorf("second command should handle the other remote's parent, got:\n%s", second)
	}
	// Group order follows the order the items were given.
	if strings.Contains(first, "gd:Other") {
		t.Error("groups came out in the wrong order")
	}
}

// A single item still names itself through a filter rather than moving its parent wholesale.
func TestArgvSingleItemStillFilters(t *testing.T) {
	argvs := Argv("/c.conf", "copy", []Item{{Path: "/srv/data/one.mkv"}}, "rem:", false, Opts{})
	if len(argvs) != 1 {
		t.Fatalf("got %d commands, want 1", len(argvs))
	}
	got := strings.Join(argvs[0], " ")
	if !strings.Contains(got, "copy /srv/data rem:") {
		t.Errorf("source should be the parent dir, got:\n%s", got)
	}
	if !strings.Contains(got, "--filter + /one.mkv") {
		t.Errorf("the item must be selected by name, got:\n%s", got)
	}
}

// Every run carries JSON logging and 1 s stats — progress parsing depends on it.
func TestArgvAlwaysRequestsJSONStats(t *testing.T) {
	argvs := Argv("/c.conf", "sync", []Item{{Path: "/a/b"}}, "rem:", false, Opts{})
	got := strings.Join(argvs[0], " ")
	for _, want := range []string{"--use-json-log", "--stats 1s"} {
		if !strings.Contains(got, want) {
			t.Errorf("argv missing %q — progress could not be parsed. got:\n%s", want, got)
		}
	}
}

func TestArgvNoItemsNoCommands(t *testing.T) {
	if got := Argv("/c.conf", "copy", nil, "rem:", false, Opts{}); len(got) != 0 {
		t.Errorf("no items should produce no commands, got %d", len(got))
	}
}

func TestFlagsRendersWhitelistedOpts(t *testing.T) {
	got := strings.Join(Flags("sync", Opts{
		Transfers: 8, Checkers: 4, Tpslimit: 10, Retries: 3, Bwlimit: "40M",
		IgnoreExisting: true, FastList: true, Compare: "checksum", SyncDelete: "after",
		Include: []string{"*.mkv"}, Exclude: []string{"**partial~"},
		Extra: []ExtraFlag{{Flag: "--max-transfer", Value: Bytes(1 << 30).Flag()}, {Flag: "--fast-list"}},
	}, true), " ")

	for _, want := range []string{
		"--dry-run", "--transfers 8", "--checkers 4", "--tpslimit 10", "--retries 3",
		"--bwlimit 40M", "--ignore-existing", "--fast-list", "--checksum", "--delete-after",
		"--include *.mkv", "--exclude **partial~", "--max-transfer 1073741824B",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("flags missing %q, got:\n%s", want, got)
		}
	}
}

// Out-of-range numbers, malformed bandwidth and non-flag names are dropped rather than
// forwarded — a value must not be able to inject an argument.
func TestFlagsRejectsBadInput(t *testing.T) {
	got := strings.Join(Flags("copy", Opts{
		Transfers: 999,             // over the cap
		Checkers:  -1,              // negative
		Bwlimit:   "40M; rm -rf /", // malformed
		Extra: []ExtraFlag{
			{Flag: "notaflag", Value: "x"},        // not a --flag
			{Flag: "--ok", Value: "line1\nline2"}, // newline in value
			{Flag: "--Bad-Case"},                  // uppercase
		},
	}, false), " ")

	for _, bad := range []string{"999", "-1", "rm -rf", "notaflag", "line2", "--Bad-Case"} {
		if strings.Contains(got, bad) {
			t.Errorf("flags should have dropped %q, got:\n%s", bad, got)
		}
	}
}

// delete-* only applies to sync; a copy or move must not be given it.
func TestFlagsSyncDeleteOnlyForSync(t *testing.T) {
	for _, op := range []string{"copy", "move"} {
		if got := strings.Join(Flags(op, Opts{SyncDelete: "during"}, false), " "); strings.Contains(got, "--delete-during") {
			t.Errorf("%s should not carry --delete-during, got %q", op, got)
		}
	}
	if got := strings.Join(Flags("sync", Opts{SyncDelete: "during"}, false), " "); !strings.Contains(got, "--delete-during") {
		t.Errorf("sync should carry --delete-during, got %q", got)
	}
}

func TestParentAndBase(t *testing.T) {
	cases := []struct{ in, parent, base string }{
		{"/mnt/local/Media/TV", "/mnt/local/Media", "TV"},
		{"/one", "/", "one"},
		{"rem:Media/TV", "rem:Media", "TV"},
		{"rem:TV", "rem:", "TV"},
		{"rem:", "rem:", "."}, // path.Base("") is "."
	}
	for _, c := range cases {
		if got := Parent(c.in); got != c.parent {
			t.Errorf("Parent(%q) = %q, want %q", c.in, got, c.parent)
		}
		if got := Base(c.in); got != c.base {
			t.Errorf("Base(%q) = %q, want %q", c.in, got, c.base)
		}
	}
}
