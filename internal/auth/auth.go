// Package auth owns who is allowed to call the API.
//
// Two independent credentials, because two different kinds of caller need in:
//
//   - a PASSWORD, for a person in a browser. Stored only as an argon2id hash, and it is
//     sb-ui's own password — deliberately NOT a system account. The panel must never handle
//     the host's credentials: it would put an OS password in this process's memory and turn
//     the web port into an oracle for guessing it.
//   - a TOKEN, for scripts and the CLI, supplied through the environment.
//
// A signed cookie carries the browser session. It is signed rather than stored so a restart
// doesn't log everyone out — which matters because the dev loop restarts the backend on every
// edit — and because a single-user panel gains little from a server-side session table. The
// cost is that individual sessions can't be revoked; RotateSecret invalidates all of them at
// once, which is what "sign out everywhere" needs anyway.
//
// Nothing here speaks HTTP. The rules live here; the wiring lives in internal/api.
package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/argon2"
)

// TokenEnv is the environment variable holding the API token for non-browser callers. The
// deployed unit already reads an EnvironmentFile, so this is set there rather than on disk
// with the rest of the state.
const TokenEnv = "SB_UI_TOKEN"

// SessionTTL is how long a browser session stays valid.
const SessionTTL = 30 * 24 * time.Hour

const storeRel = "cache/auth.json"

// state is what persists: never the password itself.
type state struct {
	PasswordHash string `json:"password_hash,omitempty"` // argon2id, "$argon2id$..."-ish encoding
	SessionKey   string `json:"session_key,omitempty"`   // hex, signs session cookies
	UpdatedAt    string `json:"updated_at,omitempty"`
}

var (
	mu     sync.Mutex
	st     state
	loaded bool

	// Seams, so tests drive the package without touching a real host.
	readState  = defaultRead
	writeState = defaultWrite
)

func ensure() { // caller holds mu
	if loaded {
		return
	}
	st = readState()
	if st.SessionKey == "" {
		// First run: mint the signing key so sessions survive restarts from now on.
		st.SessionKey = hex.EncodeToString(randBytes(32))
		writeState(st)
	}
	loaded = true
}

// ── password ──────────────────────────────────────────────────────────────────

// argon2id parameters. Deliberately on the slow side: a login happens rarely, and this is
// the only thing standing between a guessed password and control of the host's media tooling.
const (
	argonTime    = 3
	argonMemory  = 64 * 1024 // KiB
	argonThreads = 4
	argonKeyLen  = 32
	saltLen      = 16
)

// SetPassword replaces the stored password with the argon2id hash of plain. An empty or
// too-short password is refused rather than stored — "no password configured" is a state you
// arrive at by never setting one, not by setting a blank.
func SetPassword(plain string) error {
	if strings.TrimSpace(plain) == "" {
		return errors.New("password must not be empty")
	}
	if len([]rune(plain)) < 8 {
		return errors.New("password must be at least 8 characters")
	}
	mu.Lock()
	defer mu.Unlock()
	ensure()
	hash := hashPassword(plain, randBytes(saltLen))
	next := st
	next.PasswordHash = hash
	next.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	writeState(next)

	// Read it back before reporting success. The state layer reports no errors — a write can
	// fail because the path is unwritable, or because another process holds the file — and a
	// credential is the last thing that may fail silently: the owner would be told the panel
	// is protected while it is still open, or would lose a password they think they set.
	if got := readState(); got.PasswordHash != hash {
		return errors.New("password could not be saved — check that " + storeRel + " is writable and that another sb-ui is not running")
	}
	st = next
	return nil
}

// HasPassword reports whether a browser password has been configured at all.
func HasPassword() bool {
	mu.Lock()
	defer mu.Unlock()
	ensure()
	return st.PasswordHash != ""
}

// CheckPassword reports whether plain matches the stored password. It is false whenever no
// password is configured — an unset password never authenticates anyone.
func CheckPassword(plain string) bool {
	mu.Lock()
	ensure()
	stored := st.PasswordHash
	mu.Unlock()
	if stored == "" || plain == "" {
		return false
	}
	salt, want, ok := splitHash(stored)
	if !ok {
		return false
	}
	got := argon2.IDKey([]byte(plain), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	return subtle.ConstantTimeCompare(got, want) == 1
}

// hashPassword encodes as "argon2id$<b64 salt>$<b64 key>".
func hashPassword(plain string, salt []byte) string {
	key := argon2.IDKey([]byte(plain), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	return "argon2id$" + b64(salt) + "$" + b64(key)
}

func splitHash(s string) (salt, key []byte, ok bool) {
	parts := strings.Split(s, "$")
	if len(parts) != 3 || parts[0] != "argon2id" {
		return nil, nil, false
	}
	salt, err1 := unb64(parts[1])
	key, err2 := unb64(parts[2])
	if err1 != nil || err2 != nil {
		return nil, nil, false
	}
	return salt, key, true
}

// ── API token ─────────────────────────────────────────────────────────────────

// TokenConfigured reports whether an API token is set in the environment.
func TokenConfigured() bool { return os.Getenv(TokenEnv) != "" }

// CheckToken compares a presented token against the configured one in constant time. It is
// false when no token is configured, so an unset variable can never authenticate a caller.
func CheckToken(presented string) bool {
	want := os.Getenv(TokenEnv)
	if want == "" || presented == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(presented), []byte(want)) == 1
}

// ── sessions ──────────────────────────────────────────────────────────────────

// IssueSession returns a signed session value valid for SessionTTL.
//
// Format: "<expiry unix>.<hex hmac>". There is no user identity in it because the panel has
// exactly one account; adding one later means adding a field, not changing the scheme.
func IssueSession() string { return issueSessionAt(time.Now().Add(SessionTTL)) }

func issueSessionAt(exp time.Time) string {
	payload := strconv.FormatInt(exp.Unix(), 10)
	return payload + "." + hex.EncodeToString(signPayload(payload))
}

// ValidSession reports whether a cookie value is a session this server signed and that has
// not expired. Any tampering with the expiry breaks the signature.
func ValidSession(v string) bool {
	payload, sig, ok := strings.Cut(v, ".")
	if !ok {
		return false
	}
	got, err := hex.DecodeString(sig)
	if err != nil {
		return false
	}
	if !hmac.Equal(got, signPayload(payload)) {
		return false
	}
	exp, err := strconv.ParseInt(payload, 10, 64)
	if err != nil {
		return false
	}
	return time.Now().Before(time.Unix(exp, 0))
}

// RotateSecret invalidates every existing session by re-minting the signing key.
func RotateSecret() {
	mu.Lock()
	defer mu.Unlock()
	ensure()
	st.SessionKey = hex.EncodeToString(randBytes(32))
	writeState(st)
}

func signPayload(payload string) []byte {
	mu.Lock()
	ensure()
	key := st.SessionKey
	mu.Unlock()
	raw, _ := hex.DecodeString(key)
	m := hmac.New(sha256.New, raw)
	m.Write([]byte(payload))
	return m.Sum(nil)
}

// ── helpers ───────────────────────────────────────────────────────────────────

func randBytes(n int) []byte {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		// crypto/rand failing is not recoverable: refusing to run beats signing with
		// predictable bytes.
		panic("auth: crypto/rand unavailable: " + err.Error())
	}
	return b
}

// NewToken returns a fresh random token, for generating SB_UI_TOKEN on first deploy.
func NewToken() string { return hex.EncodeToString(randBytes(24)) }

func b64(b []byte) string            { return base64.RawStdEncoding.EncodeToString(b) }
func unb64(s string) ([]byte, error) { return base64.RawStdEncoding.DecodeString(s) }
