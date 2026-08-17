package api

import (
	"strings"
	"testing"
	"time"
)

// The plan must never show a remote uploading more than its daily cap — it has to match
// what a real run does under --cutoff-mode cautious, which stops before starting a file
// that would exceed --max-transfer. Before the fix the loop added the crossing file first,
// so the plan reported ~cap+one-file (e.g. 500.3G against a 500G cap).
func TestPlanNeverExceedsCap(t *testing.T) {
	const giB = int64(1) << 30
	cap500 := int64(500) * giB // 536870912000, the --max-transfer the run uses

	// A backlog of 1.4 GiB files, enough to fill two capped remotes and spill to a third.
	files := make([]planFile, 900)
	var total int64
	for i := range files {
		files[i] = planFile{Path: "/src/ep" + itoaPad(i), Size: 1400 * giB / 1000} // 1.4G
		total += files[i].Size
	}

	cfg := uploaderConfig{
		Source: "/mnt/local/Media", Threshold: "1G", CapPerDay: "500", // 500 => GB
		Remotes: []uploaderRemote{{Name: "a"}, {Name: "b"}, {Name: "c"}},
	}
	pl := buildUploadPlan(cfg, total, files, map[string]*ledgerRemote{}, map[string]int64{}, 0, "", time.Now())

	if len(pl.Remotes) == 0 {
		t.Fatal("plan produced no remotes")
	}
	seen := map[string]bool{}
	for _, r := range pl.Remotes {
		if r.Bytes > cap500 {
			t.Errorf("%s planned %d bytes > cap %d (%.1fG over)", r.Remote, r.Bytes, cap500, float64(r.Bytes-cap500)/float64(giB))
		}
		// A remote fills at most once per round; a second sub-cap chunk in the same round is
		// the "remaining slack can't fit the next file" bug — the run would leave it unused.
		key := r.Remote + "#" + itoaPad(r.Round)
		if seen[key] {
			t.Errorf("%s appears twice in round %d — fragmented sub-cap chunk", r.Remote, r.Round)
		}
		seen[key] = true
	}
}

func itoaPad(n int) string {
	s := []byte{}
	if n == 0 {
		s = append(s, '0')
	}
	for n > 0 {
		s = append([]byte{byte('0' + n%10)}, s...)
		n /= 10
	}
	return string(s)
}

// A single file larger than every remote's daily cap can never upload (rclone cautious
// won't start a file that would exceed --max-transfer). The plan must say so clearly, not
// spin to the horizon with zero rows.
func TestPlanFlagsFileLargerThanCap(t *testing.T) {
	const g = int64(1) << 30
	files := []planFile{{Path: "/src/big1.mkv", Size: 10 * g}, {Path: "/src/big2.mkv", Size: 10 * g}}
	cfg := uploaderConfig{
		Source: "/src", Threshold: "1G", CapPerDay: "5", // 5G cap < 10G files
		Sequence: []string{"a", "b"}, Remotes: []uploaderRemote{{Name: "a"}, {Name: "b"}},
	}
	pl := buildUploadPlan(cfg, 20*g, files, map[string]*ledgerRemote{}, map[string]int64{}, 0, "", time.Now())
	if len(pl.Remotes) != 0 {
		t.Fatalf("expected 0 rows (nothing fits), got %d", len(pl.Remotes))
	}
	if !strings.Contains(pl.LeftoverWhy, "larger than the biggest daily cap") {
		t.Errorf("leftover_why should explain the oversized file, got %q", pl.LeftoverWhy)
	}
	// With a cap above the file size, it plans normally.
	cfg.CapPerDay = "15" // 15G > 10G file
	pl2 := buildUploadPlan(cfg, 20*g, files, map[string]*ledgerRemote{}, map[string]int64{}, 0, "", time.Now())
	if len(pl2.Remotes) == 0 {
		t.Fatalf("with a 15G cap the 10G files should plan, got 0 rows (why=%q)", pl2.LeftoverWhy)
	}
}

// When an upload was interrupted, the run finishes that remote first at its full remaining
// cap (resume override). The plan must show the SAME order + sizes — not list the
// sequence-first remote and demote the resumed one to leftover. Regression: the plan built
// its pickCtx without the resume field, so it showed B (seq[0]) as step 1 with A's full
// size while the real run did A first, capping at A's allowance.
func TestPlanHonorsResumeFirst(t *testing.T) {
	const g = int64(1) << 30
	// Six 1G files. Cap each remote at 4G. Sequence is B,A so without resume B would lead.
	files := make([]planFile, 6)
	for i := range files {
		files[i] = planFile{Path: "/src/f" + itoaPad(i), Size: 1 * g}
	}
	cfg := uploaderConfig{
		Source: "/src", Threshold: "1G", CapPerDay: "4", // 4G per remote
		Sequence: []string{"B", "A"}, Remotes: []uploaderRemote{{Name: "A"}, {Name: "B"}},
	}
	// resume = A → A must be the first planned remote even though the sequence points at B.
	pl := buildUploadPlan(cfg, 6*g, files, map[string]*ledgerRemote{}, map[string]int64{}, 0, "A", time.Now())
	if len(pl.Remotes) == 0 {
		t.Fatal("plan produced no remotes")
	}
	if pl.Remotes[0].Remote != "A" {
		t.Errorf("step 1 should be the resumed remote A, got %s", pl.Remotes[0].Remote)
	}
	// A fills to its 4G cap first (4 files), then the rotation continues to B for the rest.
	if pl.Remotes[0].Bytes != 4*g {
		t.Errorf("resumed remote A should take its full 4G cap first, got %d bytes", pl.Remotes[0].Bytes)
	}
	if len(pl.Remotes) < 2 || pl.Remotes[1].Remote != "B" {
		t.Errorf("step 2 should continue the sequence to B, got %+v", pl.Remotes)
	}
}
