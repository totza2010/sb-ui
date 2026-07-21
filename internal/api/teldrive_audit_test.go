package api

import (
	"net/url"
	"testing"
)

// The proven test is the one claim the audit makes without knowing part sizes, so its
// arithmetic has to be exact: a file is short only when even maximum-size parts cannot
// cover it.
func TestCeilDivMatchesPartsRequired(t *testing.T) {
	for _, tc := range []struct{ size, part, want int64 }{
		{0, 512, 0},
		{1, 512, 1},
		{512, 512, 1},
		{513, 512, 2},
		{1 << 30, 512 << 20, 2},
		{100, 0, 0}, // guard: never divide by zero
	} {
		if got := ceilDiv(tc.size, tc.part); got != tc.want {
			t.Errorf("ceilDiv(%d, %d) = %d, want %d", tc.size, tc.part, got, tc.want)
		}
	}
}

// Files from the real incident. The bound is only as sharp as the ceiling it is given:
// at Telegram's 2 GiB limit almost nothing is provable, because teldrive's real chunk
// size is far smaller. Set the ceiling to the largest chunk this teldrive was ever
// configured with and the same files become provable — still without guessing, since no
// part can exceed the configured chunk size.
func TestProvenShortSharpensWithTheCeiling(t *testing.T) {
	const miB, giB int64 = 1 << 20, 1 << 30
	short := func(size, parts, ceiling int64) bool { return size > parts*ceiling }

	// Telegram's ceiling: too loose to catch any of them.
	for _, c := range []struct {
		size, parts int64
	}{{1_368_260_488, 1}, {651_569_664, 1}, {1_469_037_669, 2}} {
		if short(c.size, c.parts, 2*giB) {
			t.Errorf("%d/%d unexpectedly provable at the 2 GiB Telegram ceiling", c.size, c.parts)
		}
	}

	// The real 512 MiB chunk size: every one of them is provably short.
	for _, c := range []struct {
		name        string
		size, parts int64
	}{
		{"CK_Robin_Moore", 1_368_260_488, 1},
		{"Clair", 651_569_664, 1},
		{"S01E09 - Miso Soup", 1_469_037_669, 2},
		{"Scream.7", 21_896_483_385, 40},
	} {
		if !short(c.size, c.parts, 512*miB) {
			t.Errorf("%s: should be provably short at a 512 MiB ceiling", c.name)
		}
	}
}

// A complete file must never be flagged, whatever ceiling is configured.
func TestCompleteFileIsNeverProvenShort(t *testing.T) {
	const size = 21_896_483_385
	for _, ceiling := range []int64{512 << 20, 1 << 30, 2 << 30} {
		parts := ceilDiv(size, 512<<20) // exactly the parts a 512 MiB chunking produces
		if covered := parts * ceiling; size > covered {
			t.Errorf("ceiling %d: complete file flagged short (%d parts)", ceiling, parts)
		}
	}
}

func TestTeldriveMaxPartFallsBackToDefault(t *testing.T) {
	if got := teldriveMaxPart(teldriveDB{}); got != defaultMaxPartBytes {
		t.Errorf("unset = %d, want the default %d", got, defaultMaxPartBytes)
	}
	if got := teldriveMaxPart(teldriveDB{MaxPartBytes: 777}); got != 777 {
		t.Errorf("explicit value ignored: %d", got)
	}
}

// Each instance carries its own ceiling: a coarse setting on one must not weaken the
// test applied to another.
func TestEachInstanceKeepsItsOwnCeiling(t *testing.T) {
	cfg := teldriveConfig{DBs: []teldriveDB{
		{Remote: "main_01", DSN: "x", MaxPartBytes: 512 << 20},
		{Remote: "main_02", DSN: "x"}, // unset → default
	}}
	dbs := cfg.teldriveDBs()
	if len(dbs) != 2 {
		t.Fatalf("got %d instances, want 2", len(dbs))
	}
	if got := teldriveMaxPart(dbs[0]); got != 512<<20 {
		t.Errorf("main_01 ceiling = %d, want 512 MiB", got)
	}
	if got := teldriveMaxPart(dbs[1]); got != defaultMaxPartBytes {
		t.Errorf("main_02 ceiling = %d, want the default", got)
	}
}

// The old single-DSN config must keep working after the move to a per-instance list.
func TestLegacySingleDSNMigrates(t *testing.T) {
	dbs := teldriveConfig{DSN: "postgres://x", MaxPartBytes: 999}.teldriveDBs()
	if len(dbs) != 1 || dbs[0].DSN != "postgres://x" || dbs[0].MaxPartBytes != 999 {
		t.Fatalf("legacy config not migrated: %+v", dbs)
	}
	// An explicit list wins over the legacy fields.
	dbs = teldriveConfig{DSN: "old", DBs: []teldriveDB{{Remote: "a", DSN: "new"}}}.teldriveDBs()
	if len(dbs) != 1 || dbs[0].DSN != "new" {
		t.Fatalf("legacy DSN shadowed the configured list: %+v", dbs)
	}
}

// An empty DSN must fail before any connection attempt, with a message that says what to
// fix rather than a driver-level parse error.
func TestTeldrivePoolRejectsEmptyDSN(t *testing.T) {
	if _, err := teldrivePool(t.Context(), "   "); err == nil {
		t.Fatal("empty DSN accepted")
	} else if err.Error() != "teldrive database URL not set" {
		t.Errorf("unhelpful error: %v", err)
	}
}

// A password is free-form text; concatenating it into a URL is how a connection ends up
// silently pointing at the wrong host. Every reserved character must survive a round trip.
func TestServerDSNEscapesCredentials(t *testing.T) {
	srv := teldriveServer{Host: "teldrive-db.tail1f0818.ts.net", Port: 5432, User: "postgres", Password: "p@ss:w/rd?#1"}
	raw := srv.dsn("postgres")

	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("produced an unparseable DSN %q: %v", raw, err)
	}
	if u.Host != "teldrive-db.tail1f0818.ts.net:5432" {
		t.Errorf("host = %q — the password leaked into the authority", u.Host)
	}
	pw, _ := u.User.Password()
	if pw != "p@ss:w/rd?#1" {
		t.Errorf("password round-trip = %q, want the original", pw)
	}
	if u.Path != "/postgres" {
		t.Errorf("database = %q, want /postgres", u.Path)
	}
	if u.Query().Get("sslmode") != "disable" {
		t.Errorf("sslmode = %q, want the disable default", u.Query().Get("sslmode"))
	}
}

func TestServerDSNDefaultsPort(t *testing.T) {
	u, err := url.Parse(teldriveServer{Host: "db", User: "u"}.dsn("teldrive"))
	if err != nil {
		t.Fatal(err)
	}
	if u.Port() != "5432" {
		t.Errorf("port = %q, want the 5432 default", u.Port())
	}
}

// The common setup: one server, one database per instance.
func TestSharedServerGivesEachInstanceItsOwnDatabase(t *testing.T) {
	cfg := teldriveConfig{
		Shared: teldriveServer{Host: "db.local", Port: 5432, User: "postgres", Password: "pw"},
		DBs: []teldriveDB{
			{Remote: "tgdrive_main_01", Database: "teldrive_01"},
			{Remote: "tgdrive_main_02", Database: "teldrive_02"},
		},
	}
	dbs := cfg.teldriveDBs()
	for i, want := range []string{"/teldrive_01", "/teldrive_02"} {
		u, err := url.Parse(dbs[i].DSN)
		if err != nil {
			t.Fatalf("%s: %v", dbs[i].Remote, err)
		}
		if u.Path != want {
			t.Errorf("%s database = %q, want %q", dbs[i].Remote, u.Path, want)
		}
		if u.Host != "db.local:5432" {
			t.Errorf("%s did not inherit the shared host: %q", dbs[i].Remote, u.Host)
		}
	}
}

// An instance on a different machine overrides the shared block entirely.
func TestOwnServerOverridesShared(t *testing.T) {
	cfg := teldriveConfig{
		Shared: teldriveServer{Host: "shared.local", User: "postgres"},
		DBs: []teldriveDB{{
			Remote: "tgdrive_adult_01", Database: "teldrive", OwnServer: true,
			Server: teldriveServer{Host: "other.local", Port: 6543, User: "audit"},
		}},
	}
	u, err := url.Parse(cfg.teldriveDBs()[0].DSN)
	if err != nil {
		t.Fatal(err)
	}
	if u.Host != "other.local:6543" {
		t.Errorf("host = %q, want the instance's own server", u.Host)
	}
	if u.User.Username() != "audit" {
		t.Errorf("user = %q, want the instance's own user", u.User.Username())
	}
}

// With no server configured there is nothing to connect to — resolving must yield an
// empty DSN rather than a URL pointing at nowhere.
func TestUnconfiguredInstanceResolvesToNothing(t *testing.T) {
	cfg := teldriveConfig{DBs: []teldriveDB{{Remote: "x", Database: "teldrive"}}}
	if got := cfg.teldriveDBs()[0].DSN; got != "" {
		t.Errorf("DSN = %q, want empty", got)
	}
}

// The raw-DSN escape hatch still wins over the structured fields.
func TestExplicitDSNWins(t *testing.T) {
	cfg := teldriveConfig{
		Shared: teldriveServer{Host: "shared.local", User: "postgres"},
		DBs:    []teldriveDB{{Remote: "x", Database: "ignored", DSN: "postgres://custom/db"}},
	}
	if got := cfg.teldriveDBs()[0].DSN; got != "postgres://custom/db" {
		t.Errorf("DSN = %q, want the explicit override", got)
	}
}

// teldrive soft-deletes, so without the status filter the audit reports files the user
// already removed. The filter must also disappear on schemas that lack the column, rather
// than producing SQL that fails the whole scan.
func TestFilePredicateSkipsDeletedFiles(t *testing.T) {
	for _, tc := range []struct {
		activeOnly, hasStatus bool
		want                  string
	}{
		{true, true, "type = 'file' AND status = 'active'"},
		{false, true, "type = 'file'"}, // explicitly asked for everything
		{true, false, "type = 'file'"}, // old schema: no column to filter on
		{false, false, "type = 'file'"},
	} {
		if got := filePredicate(tc.activeOnly, tc.hasStatus); got != tc.want {
			t.Errorf("filePredicate(active=%v, hasStatus=%v) = %q, want %q", tc.activeOnly, tc.hasStatus, got, tc.want)
		}
	}
}

// A path must never be invented. When the parent can't be resolved the file is exactly
// the case the orphan check reports, so rendering a bare name there would hide it.
func TestFullPathMarksUnresolvableParents(t *testing.T) {
	paths := map[string]string{
		"f1": "/root/Media", "f2": "/root/Media/TV",
		"f3": "?/Detached/Season 01", // folder exists but its chain never reaches a root
	}
	for _, tc := range []struct{ parent, name, want string }{
		{"", "at-root.mkv", "/at-root.mkv"},
		{"f1", "movie.mkv", "/root/Media/movie.mkv"},
		{"f2", "ep.mkv", "/root/Media/TV/ep.mkv"},
		{"gone", "orphan.mkv", "?/orphan.mkv"}, // parent not a known folder at all
		// A detached parent still carries the names it has: far more useful than "?/name",
		// while the "?" prefix keeps it honest about not being browsable.
		{"f3", "ep.mkv", "?/Detached/Season 01/ep.mkv"},
	} {
		if got := fullPath(paths, tc.parent, tc.name); got != tc.want {
			t.Errorf("fullPath(%q, %q) = %q, want %q", tc.parent, tc.name, got, tc.want)
		}
	}
}

func TestNewFilesSinceOnlyCountsGrowth(t *testing.T) {
	for _, tc := range []struct {
		name          string
		before, after map[string]int64
		want          int64
	}{
		{"nothing changed", map[string]int64{"a": 100}, map[string]int64{"a": 100}, 0},
		{"files added", map[string]int64{"a": 100}, map[string]int64{"a": 137}, 37},
		{"added across instances", map[string]int64{"a": 100, "b": 50}, map[string]int64{"a": 101, "b": 55}, 6},
		// Deletions must not read as growth, and must not cancel out a real addition
		// elsewhere — that would hide new files behind an unrelated cleanup.
		{"files deleted", map[string]int64{"a": 100}, map[string]int64{"a": 60}, 0},
		{"one grew while another shrank", map[string]int64{"a": 100, "b": 100}, map[string]int64{"a": 110, "b": 40}, 10},
		// A brand-new instance has no baseline; counting it as all-new would rescan forever.
		{"instance not in baseline", map[string]int64{}, map[string]int64{"a": 9000}, 0},
		{"instance went missing", map[string]int64{"a": 100, "b": 5}, map[string]int64{"a": 100}, 0},
	} {
		if got := newFilesSince(tc.before, tc.after); got != tc.want {
			t.Errorf("%s: newFilesSince = %d, want %d", tc.name, got, tc.want)
		}
	}
}
