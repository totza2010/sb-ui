package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http/httptest"
	"strconv"
	"strings"
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
	cfg.Sequence = normSequence(cfg)
	ucfg = cfg
	ledger = map[string]*ledgerRemote{}
	upLoaded = true
	upLastMsg = ""
	seqCursor = 0
	upMu.Unlock()

	calls := &[]run{}
	measureSource = func(string) int64 { return srcSize }
	uploadRunner = func(_, _, _ string, _ []transferItem, dst string, opts transferOpts) (int64, int, bool, bool) {
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
		return perBytes, perFiles, name == floodOn, false
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

// 1. The rotation sequence must cycle strictly in order (even sequence over all remotes).
func TestUploaderRoundRobinRotation(t *testing.T) {
	cfg := uploaderConfig{
		Enabled: true, Source: "/src", Threshold: "1G",
		Sequence: []string{"A", "B", "C"},
		Remotes:  []uploaderRemote{{Name: "A"}, {Name: "B"}, {Name: "C"}},
	}
	calls := uploadSim(t, cfg, 10*gb, gb, 1, "")
	for i := 0; i < 6; i++ {
		uploaderCheck(false)
	}
	got := remoteSeq(*calls)
	want := []string{"A", "B", "C", "A", "B", "C"}
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
		uploaderCheck(false)
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
		uploaderCheck(false)
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
		uploaderCheck(false)
	}
	if n := len(*calls); n != 2 {
		t.Fatalf("byte cap: expected 2 uploads before cap, got %d", n)
	}
	// 1st run: full 100G remaining; 2nd run: 40G remaining (100−60). The value MUST carry
	// the "B" suffix so rclone reads bytes, not KiB.
	if (*calls)[0].maxTransfer != strconv.FormatInt(100*gb, 10)+"B" {
		t.Fatalf("1st --max-transfer = %s, want %dB", (*calls)[0].maxTransfer, 100*gb)
	}
	if (*calls)[1].maxTransfer != strconv.FormatInt(40*gb, 10)+"B" {
		t.Fatalf("2nd --max-transfer = %s, want %dB", (*calls)[1].maxTransfer, 40*gb)
	}
}

// 5. A rate-limit (FLOOD_WAIT/429) must pause the remote and divert to another.
func TestUploaderFloodPauseAndDivert(t *testing.T) {
	cfg := uploaderConfig{
		Enabled: true, Source: "/src", Threshold: "1G", Strategy: "lru",
		Remotes: []uploaderRemote{{Name: "A"}, {Name: "B"}},
	}
	calls := uploadSim(t, cfg, 10*gb, gb, 1, "A") // A floods every time
	uploaderCheck(false)                          // picks A → floods → paused
	uploaderCheck(false)                          // A benched → must pick B

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
	uploaderCheck(false)
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
	uploaderCheck(false)
	if len(*calls) != 0 {
		t.Fatalf("expected no upload below threshold, got %v", remoteSeq(*calls))
	}
}

// 9. Block orchestration: the pause actions must run before the upload and the restore
// actions after it, and only when their toggle is on.
func TestUploaderBlockOrchestration(t *testing.T) {
	op, or, oa := qbitPauseFn, qbitResumeFn, arrImportsFn
	defer func() { qbitPauseFn, qbitResumeFn, arrImportsFn = op, or, oa }()
	var seq []string
	qbitPauseFn = func(qbitConfig) error { seq = append(seq, "qbit:pause"); return nil }
	qbitResumeFn = func(qbitConfig) error { seq = append(seq, "qbit:resume"); return nil }
	arrImportsFn = func(en bool) {
		if en {
			seq = append(seq, "arr:on")
		} else {
			seq = append(seq, "arr:off")
		}
	}

	cfg := uploaderConfig{
		Enabled: true, Source: "/src", Threshold: "1G",
		Remotes: []uploaderRemote{{Name: "A"}},
		Pause:   pauseConfig{ArrDisable: true, Qbit: qbitConfig{Enabled: true}},
	}
	uploadSim(t, cfg, 10*gb, gb, 1, "")
	uploadRunner = func(_, _, _ string, _ []transferItem, _ string, _ transferOpts) (int64, int, bool, bool) {
		seq = append(seq, "upload")
		return gb, 1, false, false
	}
	uploaderCheck(false)

	want := []string{"qbit:pause", "arr:off", "upload", "qbit:resume", "arr:on"}
	if len(seq) != len(want) {
		t.Fatalf("block sequence = %v, want %v", seq, want)
	}
	for i := range want {
		if seq[i] != want[i] {
			t.Fatalf("block sequence = %v, want %v", seq, want)
		}
	}
}

// 10. With every block toggle off, no external service is touched.
func TestUploaderBlockDisabled(t *testing.T) {
	op, or, oa := qbitPauseFn, qbitResumeFn, arrImportsFn
	defer func() { qbitPauseFn, qbitResumeFn, arrImportsFn = op, or, oa }()
	touched := false
	qbitPauseFn = func(qbitConfig) error { touched = true; return nil }
	qbitResumeFn = func(qbitConfig) error { touched = true; return nil }
	arrImportsFn = func(bool) { touched = true }

	cfg := uploaderConfig{
		Enabled: true, Source: "/src", Threshold: "1G",
		Remotes: []uploaderRemote{{Name: "A"}},
		Pause:   pauseConfig{},
	}
	uploadSim(t, cfg, 10*gb, gb, 1, "")
	uploaderCheck(false)
	if touched {
		t.Fatalf("block actions ran while every toggle was off")
	}
}

// 11. Even on a rate-limit (flood) mid-upload, the restore actions still run — the
// external services must never be left paused.
func TestUploaderBlockRestoresOnFlood(t *testing.T) {
	op, or, oa := qbitPauseFn, qbitResumeFn, arrImportsFn
	defer func() { qbitPauseFn, qbitResumeFn, arrImportsFn = op, or, oa }()
	var seq []string
	qbitPauseFn = func(qbitConfig) error { return nil }
	qbitResumeFn = func(qbitConfig) error { seq = append(seq, "qbit:resume"); return nil }
	arrImportsFn = func(en bool) {
		if en {
			seq = append(seq, "arr:on")
		}
	}

	cfg := uploaderConfig{
		Enabled: true, Source: "/src", Threshold: "1G",
		Remotes: []uploaderRemote{{Name: "A"}},
		Pause:   pauseConfig{ArrDisable: true, Qbit: qbitConfig{Enabled: true}},
	}
	uploadSim(t, cfg, 10*gb, gb, 1, "A")
	uploaderCheck(false)
	if len(seq) != 2 || seq[0] != "qbit:resume" || seq[1] != "arr:on" {
		t.Fatalf("restore did not run after flood: %v", seq)
	}
}

// ════════════════════════════════════════════════════════════════════════════════
// Full-system coverage: pure helpers, eligibility/selection, timing, and an end-to-end
// simulator drain (how many files/bytes, which remote when, how long it waits).
// ════════════════════════════════════════════════════════════════════════════════

func TestParseCapBytes(t *testing.T) {
	cases := []struct {
		in   string
		want int64
	}{
		{"", 0},               // unlimited
		{"700", 700 * gb},     // bare number = GB
		{"700G", 700 * gb},    // explicit GB
		{"2T", 2 * 1024 * gb}, // unit-suffixed as-is
		{"500M", int64(parseSize("500M"))},
	}
	for _, c := range cases {
		if got := parseCapBytes(c.in); got != c.want {
			t.Errorf("parseCapBytes(%q) = %d, want %d", c.in, got, c.want)
		}
	}
}

func TestInWindow(t *testing.T) {
	at := func(h, m int) time.Time { return time.Date(2026, 1, 2, h, m, 0, 0, time.UTC) }
	if !inWindow("", "", at(3, 0)) {
		t.Error("empty window should always be in-window")
	}
	if !inWindow("01:00", "05:00", at(3, 0)) || inWindow("01:00", "05:00", at(6, 0)) {
		t.Error("daytime window 01-05 wrong")
	}
	if !inWindow("22:00", "06:00", at(23, 0)) || !inWindow("22:00", "06:00", at(2, 0)) {
		t.Error("overnight window should include 23:00 and 02:00")
	}
	if inWindow("22:00", "06:00", at(12, 0)) {
		t.Error("overnight window should exclude noon")
	}
}

func TestNextWindowOpen(t *testing.T) {
	loc := time.UTC
	now := time.Date(2026, 1, 2, 8, 0, 0, 0, loc)
	if open := nextWindowOpen("22:00", "23:59", now); open.Hour() != 22 || open.Day() != 2 {
		t.Fatalf("nextWindowOpen before window = %v, want today 22:00", open)
	}
	if got := nextWindowOpen("06:00", "23:00", now); !got.Equal(now) {
		t.Fatalf("nextWindowOpen inside window = %v, want now", got)
	}
	late := time.Date(2026, 1, 2, 23, 30, 0, 0, loc)
	if got := nextWindowOpen("22:00", "23:00", late); got.Day() != 3 || got.Hour() != 22 {
		t.Fatalf("nextWindowOpen after window = %v, want tomorrow 22:00", got)
	}
}

func TestLedgerWindowing(t *testing.T) {
	led := map[string]*ledgerRemote{}
	now := time.Date(2026, 1, 2, 12, 0, 0, 0, time.UTC)
	ledgerAdd(led, "A", 100*gb, 10, now.Add(-30*time.Hour)) // outside 24h → pruned
	ledgerAdd(led, "A", 200*gb, 20, now.Add(-2*time.Hour))
	ledgerAdd(led, "A", 50*gb, 5, now)
	if b := usedInWindow(led, "A", now); b != 250*gb {
		t.Errorf("usedInWindow = %d, want %d (old event pruned)", b, 250*gb)
	}
	if f := usedFilesInWindow(led, "A", now); f != 25 {
		t.Errorf("usedFilesInWindow = %d, want 25", f)
	}
	if !led["A"].LastUpload.Equal(now) {
		t.Error("LastUpload should be the most recent add")
	}
}

func TestEligibleCands(t *testing.T) {
	now := time.Date(2026, 1, 2, 12, 0, 0, 0, time.UTC)
	led := map[string]*ledgerRemote{}
	ledgerAdd(led, "A", gb, 1, now.Add(-5*time.Minute))              // within gap cooldown
	ledgerAdd(led, "B", 700*gb, 1, now)                              // byte cap hit
	ledgerAdd(led, "C", gb, 50, now)                                 // file cap hit
	led["D"] = &ledgerRemote{PausedUntil: now.Add(30 * time.Minute)} // flood-paused
	remotes := []uploaderRemote{
		{Name: "A", GapMin: 30},
		{Name: "B", CapPerDay: "700"},
		{Name: "C", CapFiles: 50},
		{Name: "D"},
		{Name: "E"},
	}
	cands, _ := eligibleCands(remotes, led, now)
	if len(cands) != 1 || cands[0].r.Name != "E" {
		names := make([]string, len(cands))
		for i, c := range cands {
			names[i] = c.r.Name
		}
		t.Fatalf("eligibleCands = %v, want only [E]", names)
	}
	if r, _, reason := selectRemote(remotes, led, pickCtx{cursor: new(int)}, now); r == nil || r.Name != "E" {
		t.Fatalf("selectRemote = %v (%s), want E", r, reason)
	}
}

func TestNextEligible(t *testing.T) {
	now := time.Date(2026, 1, 2, 12, 0, 0, 0, time.UTC)
	fresh := uploaderConfig{Remotes: []uploaderRemote{{Name: "A"}}}
	if got := nextEligible(fresh.Remotes, map[string]*ledgerRemote{}, now); !got.Equal(now) {
		t.Fatalf("nextEligible fresh = %v, want now", got)
	}
	led := map[string]*ledgerRemote{}
	ledgerAdd(led, "A", 700*gb, 1, now.Add(-10*time.Hour)) // oldest in-window event 10h ago
	cfg := uploaderConfig{Remotes: []uploaderRemote{{Name: "A", CapPerDay: "700"}}}
	want := now.Add(-10 * time.Hour).Add(uploaderWindow) // now + 14h
	if got := nextEligible(cfg.Remotes, led, now); !got.Equal(want) {
		t.Fatalf("nextEligible capped = %v, want %v (oldest+24h)", got, want)
	}
}

func TestSimRate(t *testing.T) {
	perConn := int64(5 << 20)
	if rate, src, _ := simRate(uploaderRemote{Name: "A", Bwlimit: "40M"}, nil, nil, perConn); rate != int64(parseSize("40M")) || src != "bwlimit" {
		t.Fatalf("simRate bwlimit = %d/%s, want 40M", rate, src)
	}
	if rate, _, _ := simRate(uploaderRemote{Name: "B"}, nil, nil, perConn); rate != 4*4*perConn {
		t.Fatalf("simRate default = %d, want %d", rate, 4*4*perConn)
	}
	if rate, src, _ := simRate(uploaderRemote{Name: "C"}, nil, map[string]int64{"C": 123 << 20}, perConn); rate != 123<<20 || src != "measured" {
		t.Fatalf("simRate measured = %d/%s, want 123M measured", rate, src)
	}
}

type simStep struct {
	Kind   string `json:"kind"`
	Remote string `json:"remote"`
	Note   string `json:"note"`
	Files  int    `json:"files"`
	Bytes  string `json:"bytes"`
}
type simResp struct {
	Steps   []simStep `json:"steps"`
	Summary []struct {
		Name  string `json:"name"`
		Files int    `json:"files"`
	} `json:"summary"`
	Total      string `json:"total"`
	Moved      string `json:"moved"`
	Done       bool   `json:"done"`
	ElapsedMin int    `json:"elapsed_min"`
}

func runSim(t *testing.T, cfg uploaderConfig, total, avg, perConn string) simResp {
	t.Helper()
	executor.Set(memExec{})
	body, _ := json.Marshal(map[string]any{"total": total, "avg_file": avg, "per_conn": perConn, "config": cfg})
	req := httptest.NewRequest("POST", "/api/uploader/simulate", strings.NewReader(string(body)))
	w := httptest.NewRecorder()
	uploaderSimulate(w, req)
	var out simResp
	if err := json.NewDecoder(w.Result().Body).Decode(&out); err != nil {
		t.Fatalf("decode sim response: %v", err)
	}
	return out
}

func TestSimulatorDrainWithCaps(t *testing.T) {
	cfg := uploaderConfig{
		Enabled: true, Source: "/src", Threshold: "1G", Strategy: "round_robin",
		Remotes: []uploaderRemote{{Name: "A", CapPerDay: "700"}, {Name: "B", CapPerDay: "700"}},
	}
	out := runSim(t, cfg, "2000G", "5G", "10G")

	if !out.Done {
		t.Fatalf("drain should finish; done=false (moved %s of %s)", out.Moved, out.Total)
	}
	waits, usedA, usedB := 0, false, false
	for _, s := range out.Steps {
		switch s.Kind {
		case "move":
			if s.Files > 140 {
				t.Errorf("move exceeded the 700G/140-file cap: %d files (%s)", s.Files, s.Bytes)
			}
			usedA = usedA || s.Remote == "A"
			usedB = usedB || s.Remote == "B"
		case "wait":
			waits++
			if !strings.Contains(s.Note, "cap") {
				t.Errorf("unexpected wait note: %q", s.Note)
			}
		}
	}
	if !usedA || !usedB {
		t.Errorf("both remotes should be used (A=%v B=%v)", usedA, usedB)
	}
	if waits == 0 {
		t.Error("expected a wait step for the daily-cap reset (2000G > 1400G/day)")
	}
	if len(out.Steps) == 0 || out.Steps[0].Kind != "move" || out.Steps[0].Files != 140 {
		t.Errorf("first move should be cap-bounded to 140 files (700G/5G), got %+v", out.Steps)
	}
}

// Dry-run still runs the (real) rclone command so the log shows what would move, but must
// carry --dry-run and touch no state: no ledger recording, no cursor advance.
func TestUploaderDryRunTouchesNothing(t *testing.T) {
	cfg := uploaderConfig{
		Enabled: true, Source: "/src", Threshold: "1G", DryRun: true,
		Sequence: []string{"A", "B"},
		Remotes:  []uploaderRemote{{Name: "A"}, {Name: "B"}},
	}
	calls := uploadSim(t, cfg, 10*gb, gb, 1, "")
	for i := 0; i < 3; i++ {
		uploaderCheck(false)
	}
	if len(*calls) != 3 {
		t.Fatalf("dry-run should still run the command each check, got %d", len(*calls))
	}
	upMu.Lock()
	defer upMu.Unlock()
	if ledger["A"] != nil || ledger["B"] != nil {
		t.Errorf("dry-run must not record to the ledger: %+v", ledger)
	}
	if seqCursor != 0 {
		t.Errorf("dry-run must not advance the rotation cursor, got %d", seqCursor)
	}
	// The command itself must carry --dry-run.
	_, _, opts := uploaderRemoteJob(cfg, uploaderRemote{Name: "A"}, 0)
	found := false
	for _, e := range opts.Extra {
		if e.Flag == "--dry-run" {
			found = true
		}
	}
	if !found {
		t.Error("uploaderRemoteJob should add --dry-run when cfg.DryRun")
	}
}

// The enable toggle governs only the periodic scheduler. A manual "Run now" (uploaderCheck
// with manual=true) must upload even while auto-upload is disabled.
func TestUploaderRunNowUploadsWhileDisabled(t *testing.T) {
	cfg := uploaderConfig{
		Enabled: false, Source: "/src", Threshold: "1G",
		Sequence: []string{"A"}, Remotes: []uploaderRemote{{Name: "A"}},
	}
	calls := uploadSim(t, cfg, 10*gb, gb, 1, "")

	uploaderCheck(false) // periodic while disabled → must do nothing
	if len(*calls) != 0 {
		t.Fatalf("periodic check while disabled uploaded %d (want 0)", len(*calls))
	}
	uploaderCheck(true) // Run now → uploads despite being disabled
	if len(*calls) != 1 {
		t.Fatalf("manual Run now while disabled uploaded %d (want 1)", len(*calls))
	}
}

// A user Stop must not advance the rotation: the same remote is re-picked next cycle to
// finish its partial (which teldrive keeps), and a clean finish then rotates on.
func TestUploaderResumesStoppedRemote(t *testing.T) {
	cfg := uploaderConfig{
		Enabled: true, Source: "/src", Threshold: "1G",
		Sequence: []string{"A", "B"}, Remotes: []uploaderRemote{{Name: "A"}, {Name: "B"}},
	}
	calls := uploadSim(t, cfg, 100*gb, gb, 1, "")
	upMu.Lock()
	setResume("") // start clean
	upMu.Unlock()

	// First run picks A (seq[0]) and gets STOPPED mid-way.
	stopNext := true
	uploadRunner = func(_, _, _ string, _ []transferItem, dst string, _ transferOpts) (int64, int, bool, bool) {
		name := dst[:strings.IndexByte(dst, ':')]
		*calls = append(*calls, run{remote: name})
		s := stopNext
		stopNext = false // only the first run stops
		return gb, 1, false, s
	}
	uploaderCheck(false) // A → stopped
	uploaderCheck(false) // must RESUME A (not advance to B)
	uploaderCheck(false) // A finished cleanly → now rotate to B

	got := remoteSeq(*calls)
	if len(got) != 3 || got[0] != "A" || got[1] != "A" || got[2] != "B" {
		t.Fatalf("resume order = %v, want [A A B]", got)
	}
	upMu.Lock()
	rr := resumeRemote
	upMu.Unlock()
	if rr != "" {
		t.Errorf("resume should be cleared after A finished, still %q", rr)
	}
}

// The size threshold and off-peak window gate only the automatic scheduler. A manual
// "Run now" (manual=true) must upload even when below threshold and outside the window.
func TestUploaderManualBypassesThresholdAndWindow(t *testing.T) {
	now := time.Now()
	// A window that's currently closed, and a threshold well above the source.
	cfg := uploaderConfig{
		Enabled: true, Source: "/src", Threshold: "500G",
		AllowedFrom: now.Add(2 * time.Hour).Format("15:04"), AllowedUntil: now.Add(3 * time.Hour).Format("15:04"),
		Sequence: []string{"A"}, Remotes: []uploaderRemote{{Name: "A"}},
	}
	calls := uploadSim(t, cfg, 10*gb, gb, 1, "") // 10G << 500G threshold, outside window

	uploaderCheck(false) // periodic → blocked (window/threshold)
	if len(*calls) != 0 {
		t.Fatalf("periodic below threshold / outside window uploaded %d (want 0)", len(*calls))
	}
	uploaderCheck(true) // Run now → uploads regardless
	if len(*calls) != 1 {
		t.Fatalf("manual Run now should upload despite threshold+window, got %d", len(*calls))
	}
}
