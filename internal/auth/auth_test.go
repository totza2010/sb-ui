package auth

import (
	"strings"
	"testing"
	"time"
)

func fresh(t *testing.T) {
	t.Helper()
	UseMemoryStore()
}

// The password is never stored, only an argon2id hash of it — the panel holding a recoverable
// password would be a credential to steal for no benefit.
func TestPasswordIsStoredOnlyAsAHash(t *testing.T) {
	fresh(t)
	const pw = "correct horse battery"
	if err := SetPassword(pw); err != nil {
		t.Fatalf("SetPassword: %v", err)
	}
	mu.Lock()
	stored := st.PasswordHash
	mu.Unlock()

	if strings.Contains(stored, pw) {
		t.Fatal("the stored value contains the password itself")
	}
	if !strings.HasPrefix(stored, "argon2id$") {
		t.Errorf("stored value is not an argon2id hash: %q", stored)
	}
	if !CheckPassword(pw) {
		t.Error("the correct password should verify")
	}
	if CheckPassword(pw+"x") || CheckPassword("") || CheckPassword(strings.ToUpper(pw)) {
		t.Error("a wrong password must not verify")
	}
}

// Two accounts with the same password must not share a hash: the salt has to be per-password.
func TestPasswordHashIsSalted(t *testing.T) {
	fresh(t)
	_ = SetPassword("same password here")
	mu.Lock()
	first := st.PasswordHash
	mu.Unlock()

	fresh(t)
	_ = SetPassword("same password here")
	mu.Lock()
	second := st.PasswordHash
	mu.Unlock()

	if first == second {
		t.Error("the same password produced the same hash — the salt is not random")
	}
}

// "No password configured" must never authenticate anyone. This is what makes the
// deny-by-default posture safe: a fresh install refuses rather than admits.
func TestUnsetPasswordNeverVerifies(t *testing.T) {
	fresh(t)
	if HasPassword() {
		t.Fatal("a fresh install should have no password")
	}
	for _, attempt := range []string{"", " ", "admin", "password"} {
		if CheckPassword(attempt) {
			t.Errorf("CheckPassword(%q) accepted a login with no password configured", attempt)
		}
	}
}

func TestSetPasswordRefusesWeakInput(t *testing.T) {
	fresh(t)
	for _, bad := range []string{"", "   ", "short"} {
		if err := SetPassword(bad); err == nil {
			t.Errorf("SetPassword(%q) should have been refused", bad)
		}
	}
	if HasPassword() {
		t.Error("a refused password must not be stored")
	}
}

// A session is only valid if this server signed it and it has not expired. Tampering with the
// expiry to extend a session has to break the signature.
func TestSessionMustBeSignedAndUnexpired(t *testing.T) {
	fresh(t)
	good := IssueSession()
	if !ValidSession(good) {
		t.Fatal("a freshly issued session should be valid")
	}

	expired := issueSessionAt(time.Now().Add(-time.Minute))
	if ValidSession(expired) {
		t.Error("an expired session must be refused")
	}

	// Rewriting the expiry keeps the old signature, which no longer matches.
	_, sig, _ := strings.Cut(good, ".")
	forged := "99999999999." + sig
	if ValidSession(forged) {
		t.Error("extending the expiry must invalidate the signature")
	}

	for _, junk := range []string{"", ".", "abc", "abc.def", "123.zz", good + "x"} {
		if ValidSession(junk) {
			t.Errorf("ValidSession(%q) accepted a value this server never signed", junk)
		}
	}
}

// Sessions survive a restart — the signing key is persisted, not minted per boot. The dev loop
// restarts the backend on every edit, so a per-boot key would mean logging in constantly.
func TestSessionSurvivesRestart(t *testing.T) {
	fresh(t)
	tok := IssueSession()

	// Simulate a restart: drop the in-memory state, keep what was written.
	mu.Lock()
	loaded = false
	st = state{}
	mu.Unlock()

	if !ValidSession(tok) {
		t.Error("a session issued before a restart should still be valid after it")
	}
}

// Rotating is how "sign out everywhere" works, since signed sessions can't be revoked singly.
func TestRotateSecretInvalidatesEverySession(t *testing.T) {
	fresh(t)
	tok := IssueSession()
	RotateSecret()
	if ValidSession(tok) {
		t.Error("rotating the signing key must invalidate existing sessions")
	}
	if !ValidSession(IssueSession()) {
		t.Error("sessions issued after rotating should be valid")
	}
}

// An unset token must never let a caller in — the same rule as the password.
func TestTokenNeverMatchesWhenUnset(t *testing.T) {
	t.Setenv(TokenEnv, "")
	if TokenConfigured() {
		t.Error("an empty environment variable is not a configured token")
	}
	for _, attempt := range []string{"", "anything", "null"} {
		if CheckToken(attempt) {
			t.Errorf("CheckToken(%q) accepted a token with none configured", attempt)
		}
	}
}

func TestTokenMatchesExactly(t *testing.T) {
	t.Setenv(TokenEnv, "s3cret-token-value")
	if !TokenConfigured() {
		t.Fatal("token should be reported as configured")
	}
	if !CheckToken("s3cret-token-value") {
		t.Error("the correct token should be accepted")
	}
	for _, bad := range []string{"", "s3cret-token-valu", "s3cret-token-value ", "S3CRET-TOKEN-VALUE"} {
		if CheckToken(bad) {
			t.Errorf("CheckToken(%q) should not match", bad)
		}
	}
}

func TestNewTokenIsRandomAndLongEnough(t *testing.T) {
	a, b := NewToken(), NewToken()
	if a == b {
		t.Error("tokens must not repeat")
	}
	if len(a) < 32 {
		t.Errorf("token %q is too short to resist guessing", a)
	}
}

// A write that silently fails must not be reported as success: the owner would believe the
// panel is protected while it is still open to anyone. Discovered for real — the store layer
// reports no errors, and a concurrent process holding the file made the write a no-op while
// the CLI printed "Password set".
func TestSetPasswordFailsLoudlyWhenItCannotPersist(t *testing.T) {
	fresh(t)
	mu.Lock()
	writeState = func(state) {} // accept the write, keep nothing
	mu.Unlock()

	err := SetPassword("a-good-password")
	if err == nil {
		t.Fatal("SetPassword reported success although nothing was stored")
	}
	if !strings.Contains(err.Error(), "could not be saved") {
		t.Errorf("the error should say the password was not saved, got %v", err)
	}
	if CheckPassword("a-good-password") {
		t.Error("a password that failed to persist must not authenticate")
	}
}
