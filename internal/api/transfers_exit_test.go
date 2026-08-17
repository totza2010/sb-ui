package api

import "testing"

// rclone signals "I stopped because --max-transfer was reached" with exit code 8. That is a
// designed stop at a whole-file boundary (under --cutoff-mode cautious), not an error, and
// treating any non-zero code as failure made every correct capped transfer report as failed.
// Exit 9 means "worked, nothing needed transferring", which is likewise not a failure.
func TestClassifyExit(t *testing.T) {
	cases := []struct {
		code   int
		failed bool
		capped bool
		what   string
	}{
		{0, false, false, "success"},
		{rcExitMaxTransfer, false, true, "--max-transfer limit reached — the designed stop"},
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
