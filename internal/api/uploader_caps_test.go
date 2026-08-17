package api

import (
	"testing"
	"time"
)

// Hitting --max-transfer is how a capped uploader run is SUPPOSED to end: rclone stops at
// a file boundary once the remote's daily allowance is spent and exits 8. Treating that as
// a failure made every successful capped upload show up red in Activity (and made the
// whole rotation look broken) even though the bytes moved and were recorded.
func TestClassifyExit(t *testing.T) {
	cases := []struct {
		code   int
		failed bool
		capped bool
		what   string
	}{
		{0, false, false, "success"},
		{rcExitMaxTransfer, false, true, "daily cap reached — the designed stop"},
		{rcExitNoTransfer, false, false, "nothing to transfer"},
		{1, true, false, "usage error"},
		{5, true, false, "temporary error"},
		{7, true, false, "fatal error"},
	}
	for _, c := range cases {
		failed, capped := classifyExit(c.code)
		if failed != c.failed || capped != c.capped {
			t.Errorf("classifyExit(%d) = (failed=%v capped=%v), want (failed=%v capped=%v) — %s",
				c.code, failed, capped, c.failed, c.capped, c.what)
		}
	}
}

// remoteFree is the single definition of "bytes this remote may still take today". The
// picker, the plan, the free map and the command preview all read it, so they can't drift.
func TestRemoteFree(t *testing.T) {
	now := time.Date(2026, 1, 2, 12, 0, 0, 0, time.UTC)
	led := map[string]*ledgerRemote{}
	ledgerAdd(led, "A", 4*gb, 1, now.Add(-time.Hour))      // 4G used inside the window
	ledgerAdd(led, "A", 100*gb, 1, now.Add(-30*time.Hour)) // outside → must not count

	if got := remoteFree(uploaderRemote{Name: "A", CapPerDay: "10"}, led, now); got != 6*gb {
		t.Errorf("free = %d, want %d (10G cap − 4G used in window)", got, 6*gb)
	}
	// Cap spent → 0, never negative (a negative would become a bogus --max-transfer).
	if got := remoteFree(uploaderRemote{Name: "A", CapPerDay: "2"}, led, now); got != 0 {
		t.Errorf("over-cap free = %d, want 0", got)
	}
	// No cap configured → unlimited, signalled as -1 (uploaderRemoteJob then omits the flag).
	if got := remoteFree(uploaderRemote{Name: "A"}, led, now); got != -1 {
		t.Errorf("uncapped free = %d, want -1", got)
	}
	// The free map agrees with the picker for the same remote/ledger.
	cfg := uploaderConfig{Remotes: []uploaderRemote{{Name: "A", CapPerDay: "10"}}}
	if ft := freeToday(cfg, led, now)["A"]; ft != 6*gb {
		t.Errorf("freeToday = %d, want %d — must match remoteFree", ft, 6*gb)
	}
}

// Resetting the daily caps must make a spent/benched remote immediately eligible again,
// while keeping the lifetime Uploaded tally (that's the balancer's fill proxy, not a quota).
func TestResetCaps(t *testing.T) {
	now := time.Now()
	upMu.Lock()
	ledger = map[string]*ledgerRemote{}
	ledgerAdd(ledger, "A", 700*gb, 5, now)
	ledgerAdd(ledger, "B", 700*gb, 5, now)
	ledger["A"].PausedUntil = now.Add(time.Hour) // flood-benched
	upMu.Unlock()

	remotes := []uploaderRemote{{Name: "A", CapPerDay: "700"}, {Name: "B", CapPerDay: "700"}}
	if cands, _ := eligibleCands(remotes, ledger, now); len(cands) != 0 {
		t.Fatalf("precondition: both remotes should be capped, got %d eligible", len(cands))
	}

	// Reset just A: A becomes eligible, B stays capped.
	if cleared := resetCaps("A"); len(cleared) != 1 || cleared[0] != "A" {
		t.Errorf("resetCaps(A) cleared = %v, want [A]", cleared)
	}
	cands, _ := eligibleCands(remotes, ledger, now)
	if len(cands) != 1 || cands[0].r.Name != "A" {
		t.Fatalf("after reset of A, eligible = %+v, want just A", cands)
	}
	if cands[0].free != 700*gb {
		t.Errorf("A's free = %d, want the full %d cap back", cands[0].free, 700*gb)
	}
	if ledger["A"].Uploaded != 700*gb {
		t.Errorf("lifetime Uploaded = %d, want it kept at %d", ledger["A"].Uploaded, 700*gb)
	}

	// Reset everything.
	if cleared := resetCaps(""); len(cleared) != 2 {
		t.Errorf("resetCaps(all) cleared = %v, want both", cleared)
	}
	if cands, _ := eligibleCands(remotes, ledger, now); len(cands) != 2 {
		t.Errorf("after full reset, eligible = %d, want 2", len(cands))
	}
}
