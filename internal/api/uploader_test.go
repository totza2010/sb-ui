package api

import (
	"context"
	"io"
	"strconv"
	"testing"
	"time"

	"sb-ui/internal/executor"
)

// memExec is a no-op executor so the uploader's ledger/config persistence (via the
// store package) stays in-memory during tests instead of touching the disk.
type memExec struct{}

func (memExec) Run(context.Context, []string, string) (int, string, error)       { return 0, "", nil }
func (memExec) RunStdout(context.Context, []string, string) (int, string, error) { return 0, "", nil }
func (memExec) RunStream(context.Context, []string, string, bool) (*executor.Stream, error) {
	return nil, nil
}
func (memExec) ReadFile(context.Context, string) (string, error)        { return "", nil }
func (memExec) WriteFile(context.Context, string, string) error         { return nil }
func (memExec) FileExists(context.Context, string) (bool, error)        { return false, nil }
func (memExec) MakeDirs(context.Context, string) error                  { return nil }
func (memExec) Upload(context.Context, string, io.Reader) error         { return nil }
func (memExec) Download(context.Context, string) (io.ReadCloser, error) { return nil, nil }
func (memExec) Close() error                                            { return nil }

const gb = int64(1) << 30

// run records what each simulated upload moved + the flags it was handed.
type run struct {
	remote      string // destination remote name (before the ':')
	maxTransfer string // value of --max-transfer, "" if absent
}

// uploadSim installs stubs that report the source as always over threshold and
// "move" the given bytes/files per call. floodOn names a remote that trips a
// rate-limit on every call (to exercise auto-pause). Returns the call log.
func uploadSim(t *testing.T, cfg uploaderConfig, srcSize, perBytes int64, perFiles int, floodOn string) *[]run {
	t.Helper()
	executor.Set(memExec{})

	upMu.Lock()
	if cfg.Strategy == "" {
		cfg.Strategy = "lru"
	}
	ucfg = cfg
	ledger = map[string]*ledgerRemote{}
	upLoaded = true
	upLastMsg = ""
	rrIndex = 0
	upMu.Unlock()

	calls := &[]run{}
	measureSource = func(string) int64 { return srcSize }
	uploadRunner = func(_, _, _ string, _ []transferItem, dst string, opts transferOpts) (int64, int, bool) {
		name := dst
		for i := 0; i < len(dst); i++ {
			if dst[i] == ':' {
				name = dst[:i]
				break
			}
		}
		mt := ""
		for _, e := range opts.Extra {
			if e.Flag == "--max-transfer" {
				mt = e.Value
			}
		}
		*calls = append(*calls, run{remote: name, maxTransfer: mt})
		return perBytes, perFiles, name == floodOn
	}
	return calls
}

func remoteSeq(calls []run) []string {
	out := make([]string, len(calls))
	for i, c := range calls {
		out[i] = c.remote
	}
	return out
}

// 1. Round-robin must cycle strictly across remotes.
func TestUploaderRoundRobinRotation(t *testing.T) {
	cfg := uploaderConfig{
		Enabled: true, Source: "/src", Threshold: "1G", Strategy: "round_robin",
		Remotes: []uploaderRemote{{Name: "A"}, {Name: "B"}, {Name: "C"}},
	}
	calls := uploadSim(t, cfg, 10*gb, gb, 1, "")
	for i := 0; i < 6; i++ {
		uploaderCheck()
	}
	got := remoteSeq(*calls)
	// rrIndex increments before pick, so the first pick is B (index 1).
	want := []string{"B", "C", "A", "B", "C", "A"}
	if len(got) != 6 {
		t.Fatalf("expected 6 uploads, got %d (%v)", len(got), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("round-robin order = %v, want %v", got, want)
		}
	}
}

// 2. LRU must spread across every remote (no starving one) before repeating.
func TestUploaderLRUSpreads(t *testing.T) {
	cfg := uploaderConfig{
		Enabled: true, Source: "/src", Threshold: "1G", Strategy: "lru",
		Remotes: []uploaderRemote{{Name: "A"}, {Name: "B"}, {Name: "C"}},
	}
	calls := uploadSim(t, cfg, 10*gb, gb, 1, "")
	for i := 0; i < 3; i++ {
		uploaderCheck()
		time.Sleep(2 * time.Millisecond) // distinct LastUpload timestamps
	}
	seen := map[string]bool{}
	for _, c := range *calls {
		seen[c.remote] = true
	}
	if len(seen) != 3 {
		t.Fatalf("LRU did not spread across all remotes in 3 cycles: %v", remoteSeq(*calls))
	}
}

// 3. Per-day FILE cap must bench a remote once its file budget is spent.
func TestUploaderFileCapPerDay(t *testing.T) {
	cfg := uploaderConfig{
		Enabled: true, Source: "/src", Threshold: "1G", Strategy: "lru",
		Remotes: []uploaderRemote{{Name: "A", CapFiles: 10}},
	}
	// each run moves 5 files → 2 runs reach the cap, the 3rd must be skipped.
	calls := uploadSim(t, cfg, 100*gb, gb, 5, "")
	for i := 0; i < 3; i++ {
		uploaderCheck()
	}
	if n := len(*calls); n != 2 {
		t.Fatalf("file cap: expected 2 uploads before cap, got %d (%v)", n, remoteSeq(*calls))
	}
	upMu.Lock()
	msg := upLastMsg
	upMu.Unlock()
	if msg != "no eligible remote (caps/cooldowns)" {
		t.Fatalf("expected skip message after file cap, got %q", msg)
	}
}

// 4. Per-day BYTE cap must shrink --max-transfer as the day fills, then bench.
func TestUploaderByteCapCutoff(t *testing.T) {
	cfg := uploaderConfig{
		Enabled: true, Source: "/src", Threshold: "1G", Strategy: "lru",
		Remotes: []uploaderRemote{{Name: "A", CapPerDay: "100G"}},
	}
	calls := uploadSim(t, cfg, 500*gb, 60*gb, 3, "") // moves 60G per run
	for i := 0; i < 3; i++ {
		uploaderCheck()
	}
	if n := len(*calls); n != 2 {
		t.Fatalf("byte cap: expected 2 uploads before cap, got %d", n)
	}
	// 1st run: full 100G remaining; 2nd run: 40G remaining (100−60).
	if (*calls)[0].maxTransfer != strconv.FormatInt(100*gb, 10) {
		t.Fatalf("1st --max-transfer = %s, want %d", (*calls)[0].maxTransfer, 100*gb)
	}
	if (*calls)[1].maxTransfer != strconv.FormatInt(40*gb, 10) {
		t.Fatalf("2nd --max-transfer = %s, want %d", (*calls)[1].maxTransfer, 40*gb)
	}
}

// 5. A rate-limit (FLOOD_WAIT/429) must pause the remote and divert to another.
func TestUploaderFloodPauseAndDivert(t *testing.T) {
	cfg := uploaderConfig{
		Enabled: true, Source: "/src", Threshold: "1G", Strategy: "lru",
		Remotes: []uploaderRemote{{Name: "A"}, {Name: "B"}},
	}
	calls := uploadSim(t, cfg, 10*gb, gb, 1, "A") // A floods every time
	uploaderCheck()                               // picks A → floods → paused
	uploaderCheck()                               // A benched → must pick B

	got := remoteSeq(*calls)
	if len(got) != 2 || got[0] != "A" || got[1] != "B" {
		t.Fatalf("flood divert: got %v, want [A B]", got)
	}
	upMu.Lock()
	lr := ledger["A"]
	upMu.Unlock()
	if lr == nil || !lr.PausedUntil.After(time.Now()) {
		t.Fatalf("remote A should be paused into the future after a flood hit")
	}
}

// 6. Uploads outside the allowed window must not run.
func TestUploaderWindowGate(t *testing.T) {
	now := time.Now()
	from := now.Add(2 * time.Hour).Format("15:04")
	until := now.Add(3 * time.Hour).Format("15:04")
	cfg := uploaderConfig{
		Enabled: true, Source: "/src", Threshold: "1G", Strategy: "lru",
		AllowedFrom: from, AllowedUntil: until,
		Remotes: []uploaderRemote{{Name: "A"}},
	}
	calls := uploadSim(t, cfg, 10*gb, gb, 1, "")
	uploaderCheck()
	if len(*calls) != 0 {
		t.Fatalf("expected no upload outside window, got %v", remoteSeq(*calls))
	}
	upMu.Lock()
	msg := upLastMsg
	upMu.Unlock()
	if msg != "outside upload window" {
		t.Fatalf("expected window-gate message, got %q", msg)
	}
}

// 8. The rate-limit detector must catch real provider errors without false hits.
func TestIsFloodMsg(t *testing.T) {
	hit := []string{
		"Failed to copy: FLOOD_WAIT (420): A wait of 30 seconds is required",
		`googleapi: Error 403: User rate limit exceeded, userRateLimitExceeded`,
		"pacer: low level retry 1/10 (error Too Many Requests)",
		"HTTP error 429 (429 Too Many Requests) returned body",
	}
	for _, m := range hit {
		if !isFloodMsg(m) {
			t.Errorf("expected flood match for %q", m)
		}
	}
	miss := []string{
		"Transferred: 12 GiB / 40 GiB, 30%",
		"Failed to copy: file not found (404)",
		"INFO  : movie.mkv: Copied (new)",
	}
	for _, m := range miss {
		if isFloodMsg(m) {
			t.Errorf("unexpected flood match for %q", m)
		}
	}
}

// 7. Below the size threshold, nothing uploads.
func TestUploaderBelowThreshold(t *testing.T) {
	cfg := uploaderConfig{
		Enabled: true, Source: "/src", Threshold: "500G", Strategy: "lru",
		Remotes: []uploaderRemote{{Name: "A"}},
	}
	calls := uploadSim(t, cfg, 10*gb, gb, 1, "") // 10G < 500G
	uploaderCheck()
	if len(*calls) != 0 {
		t.Fatalf("expected no upload below threshold, got %v", remoteSeq(*calls))
	}
}
