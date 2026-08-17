package api

import (
	"strconv"
	"strings"
	"testing"
)

// rclone parses a bare --max-transfer number as KiB, so passing the raw byte count makes
// the cap 1024× larger than intended. That is not theoretical: it let a run push ~1 TB past
// a 500 G daily cap and get the remote rate-limited. The value must always carry a unit.
func TestMaxTransferCarriesByteSuffix(t *testing.T) {
	for _, free := range []int64{1, 5 * gb, 500 * gb, 1 << 40} {
		got := maxTransferValue(free)
		if want := strconv.FormatInt(free, 10) + "B"; got != want {
			t.Errorf("maxTransferValue(%d) = %q, want %q", free, got, want)
		}
		// The invariant that matters: a value ending in a digit is read as KiB by rclone.
		if last := got[len(got)-1]; last >= '0' && last <= '9' {
			t.Errorf("maxTransferValue(%d) = %q ends in a digit — rclone would read it as KiB, making the cap 1024x too large", free, got)
		}
		if !strings.HasSuffix(got, "B") {
			t.Errorf("maxTransferValue(%d) = %q must carry a byte unit", free, got)
		}
	}
}

// Hitting --max-transfer is how a capped run is SUPPOSED to end: rclone stops at a file
// boundary once the allowance is spent and exits 8. Treating that as a failure made every
// successful capped upload show up red, which made the whole rotation look broken.
func TestClassifyExit(t *testing.T) {
	cases := []struct {
		code   int
		failed bool
		capped bool
		what   string
	}{
		{0, false, false, "success"},
		{rcExitMaxTransfer, false, true, "daily cap reached — the designed stop"},
		{rcExitNoTransfer, false, false, "nothing needed transferring"},
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
