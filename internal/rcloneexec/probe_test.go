package rcloneexec

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// fakeRunner answers each rclone invocation from a table keyed by the subcommand, so a probe can
// be driven without a host.
func fakeRunner(answers map[string]struct {
	code int
	out  string
	err  error
}) Runner {
	return func(_ context.Context, argv []string) (int, string, error) {
		key := strings.Join(argv[1:], " ")
		if a, ok := answers[key]; ok {
			return a.code, a.out, a.err
		}
		return 1, "", errors.New("unexpected command: " + key)
	}
}

type answer = struct {
	code int
	out  string
	err  error
}

const providersJSON = `[
  {"Name":"drive","Prefix":"drive"},
  {"Name":"teldrive","Prefix":"teldrive"},
  {"Name":"s3","Prefix":"s3"}
]`

// The host runs a fork whose version string is identical to upstream's, so the only way to know
// teldrive is available is to ask which backends were compiled in.
func TestProbeDetectsTheForkByCapabilityNotName(t *testing.T) {
	c := Probe(context.Background(), fakeRunner(map[string]answer{
		"version":                    {0, "rclone v1.75.0\n- os/version: ubuntu 24.04\n", nil},
		"config providers":           {0, providersJSON, nil},
		"rc --loopback options/info": {0, `{"main":[],"filter":[],"vfs":[]}`, nil},
	}))

	if c.Version != "v1.75.0" {
		t.Errorf("version = %q, want v1.75.0", c.Version)
	}
	if !strings.Contains(c.VersionLine, "rclone v1.75.0") {
		t.Errorf("version line = %q, want the banner kept for display", c.VersionLine)
	}
	if !c.HasTeldrive || !c.Fork() {
		t.Error("teldrive is in the backend list, so this must be reported as the fork")
	}
	if !c.Supports("s3") || !c.Supports("TELDRIVE") {
		t.Error("Supports should match compiled-in backends, case-insensitively")
	}
	if c.Supports("onedrive") {
		t.Error("Supports must not claim a backend that wasn't listed")
	}
	if !c.RC || c.RCOptions != 3 {
		t.Errorf("rc should be available with 3 option groups, got rc=%v n=%d", c.RC, c.RCOptions)
	}
	if len(c.Problems) != 0 {
		t.Errorf("a healthy probe should report no problems, got %v", c.Problems)
	}
}

// Upstream rclone has no teldrive, so a remote configured for it would fail everything. Saying
// so is the whole point of probing.
func TestProbeUpstreamHasNoTeldrive(t *testing.T) {
	c := Probe(context.Background(), fakeRunner(map[string]answer{
		"version":                    {0, "rclone v1.74.2\n", nil},
		"config providers":           {0, `[{"Name":"drive","Prefix":"drive"}]`, nil},
		"rc --loopback options/info": {0, `{"main":[]}`, nil},
	}))
	if c.HasTeldrive || c.Fork() {
		t.Error("upstream rclone must not be reported as the fork")
	}
	if c.Supports("teldrive") {
		t.Error("teldrive must not be claimed on a binary that lacks it")
	}
}

// The failure this replaces: the flag catalog called options/info, got an error, returned nil,
// and the UI showed an empty list as if rclone simply had no flags.
func TestProbeSaysWhyRCIsUnavailableInsteadOfGoingQuiet(t *testing.T) {
	c := Probe(context.Background(), fakeRunner(map[string]answer{
		"version":                    {0, "rclone v1.60.0\n", nil},
		"config providers":           {0, providersJSON, nil},
		"rc --loopback options/info": {1, "unknown command", nil},
	}))

	if c.RC {
		t.Error("rc must not be reported as available when the call failed")
	}
	if len(c.Problems) == 0 {
		t.Fatal("a failed probe must explain itself")
	}
	joined := strings.Join(c.Problems, " | ")
	if !strings.Contains(joined, "rc is not available") {
		t.Errorf("the problem should name rc, got %q", joined)
	}
	// The rest of the answer still arrived — a partial probe beats no probe.
	if c.Version != "v1.60.0" || !c.HasTeldrive {
		t.Errorf("version and backends should survive an rc failure, got %+v", c)
	}
}

// rclone missing entirely is the one case where nothing else can be learned, so it stops early
// rather than reporting three separate failures for the same cause.
func TestProbeStopsWhenRcloneCannotRun(t *testing.T) {
	c := Probe(context.Background(), fakeRunner(map[string]answer{
		"version": {0, "", errors.New("exec: \"rclone\": executable file not found in $PATH")},
	}))
	if len(c.Problems) != 1 {
		t.Fatalf("want a single problem explaining the cause, got %v", c.Problems)
	}
	if !strings.Contains(c.Problems[0], "could not run rclone") {
		t.Errorf("problem should say rclone could not run, got %q", c.Problems[0])
	}
	if c.RC || c.HasTeldrive || len(c.Backends) != 0 {
		t.Error("nothing should be claimed when rclone could not be run at all")
	}
}

// A binary that answers but returns nonsense must be reported as a problem, not silently treated
// as having no backends.
func TestProbeReportsUnparseableOutput(t *testing.T) {
	c := Probe(context.Background(), fakeRunner(map[string]answer{
		"version":                    {0, "rclone v1.75.0\n", nil},
		"config providers":           {0, "not json at all", nil},
		"rc --loopback options/info": {0, "also not json", nil},
	}))
	if len(c.Problems) != 2 {
		t.Errorf("both unparseable answers should be reported, got %v", c.Problems)
	}
	if c.RC {
		t.Error("rc must not count as working when its answer could not be read")
	}
}

// The probe records when it ran, so a caller can cache it and know how stale it is.
func TestProbeStampsWhenItRan(t *testing.T) {
	c := Probe(context.Background(), fakeRunner(map[string]answer{
		"version": {0, "rclone v1.75.0\n", nil},
	}))
	if c.ProbedAt.IsZero() {
		t.Error("ProbedAt should be set even when the probe found problems")
	}
}

// Empty means empty, not null. A consumer reading this over JSON should not have to special-case
// a missing list to find out that nothing went wrong.
func TestProbeReportsEmptyListsNotNull(t *testing.T) {
	c := Probe(context.Background(), fakeRunner(map[string]answer{
		"version":                    {0, "rclone v1.73.1\n", nil},
		"config providers":           {0, `[{"Name":"drive","Prefix":"drive"}]`, nil},
		"rc --loopback options/info": {0, `{"main":[]}`, nil},
	}))
	if c.Problems == nil {
		t.Error("Problems should be an empty slice, not nil, so it serialises as []")
	}
	if c.Backends == nil {
		t.Error("Backends should never be nil")
	}
}
