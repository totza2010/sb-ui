package rcloneexec

import (
	"context"
	"encoding/json"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Probing what rclone can actually do, rather than assuming.
//
// The host does not run upstream rclone. It runs a fork (tgdrive/rclone) built on v1.75.0 that
// adds the teldrive backend, and the fork does not announce itself: its version string is just
// the upstream tag, so there is nothing to parse. The only honest way to know what we are
// talking to is to ask it — which is also the only approach that keeps working when the fork
// rebases, when someone runs an older rclone, or when a published API spec disagrees with the
// binary in front of us.
//
// This also replaces a silent failure. The flag catalog called options/info, returned nil when
// the call failed, and the UI showed an empty list with no explanation. Every probe here
// records why it failed instead.

// Runner executes an rclone command. Injected rather than global, so a test can drive the probe
// without a host and the caller decides whether that means locally or over SSH.
type Runner func(ctx context.Context, argv []string) (code int, out string, err error)

// Capabilities is what the rclone in front of us turned out to be.
type Capabilities struct {
	Version     string    `json:"version"`      // "v1.75.0", parsed from the version banner
	VersionLine string    `json:"version_line"` // the banner as printed, for display
	Backends    []string  `json:"backends"`     // backend prefixes compiled into this binary
	HasTeldrive bool      `json:"has_teldrive"` // the fork's marker: upstream has no teldrive
	RC          bool      `json:"rc"`           // the rc interface answered
	RCOptions   int       `json:"rc_options"`   // option groups it reported, as a sanity signal
	Problems    []string  `json:"problems"`     // what could not be determined, and why
	ProbedAt    time.Time `json:"probed_at"`
}

// Fork reports whether this looks like the teldrive fork rather than upstream rclone. It is
// inferred from a capability, not a name, because the fork's version string is indistinguishable
// from upstream's.
func (c Capabilities) Fork() bool { return c.HasTeldrive }

// Supports reports whether a named backend is compiled into this binary. A remote configured
// for a backend that isn't here will fail every operation, and this is how to say so before
// trying.
func (c Capabilities) Supports(backend string) bool {
	for _, b := range c.Backends {
		if strings.EqualFold(b, backend) {
			return true
		}
	}
	return false
}

var versionRE = regexp.MustCompile(`v?\d+\.\d+(\.\d+)?`)

// Probe asks the binary three questions: what version it is, which backends it was built with,
// and whether the rc interface works. Every answer is best-effort; a failure becomes an entry in
// Problems rather than an error, because a partial answer is still worth having — knowing the
// version is useful even when rc is unavailable.
func Probe(ctx context.Context, run Runner) Capabilities {
	// Problems starts empty rather than nil so it serialises as [] — a caller reading JSON
	// should see "nothing wrong", not null, which every consumer then has to special-case.
	c := Capabilities{ProbedAt: time.Now(), Problems: []string{}, Backends: []string{}}

	// 1. Version.
	if code, out, err := run(ctx, []string{"rclone", "version"}); err != nil {
		c.Problems = append(c.Problems, "could not run rclone: "+err.Error())
		return c // nothing else will work either
	} else if code != 0 {
		c.Problems = append(c.Problems, "rclone version exited "+strconv.Itoa(code))
	} else if line := firstLine(out); line != "" {
		c.VersionLine = line
		c.Version = versionRE.FindString(line)
	}

	// 2. Compiled-in backends. This is what tells us whether teldrive exists here, and it is
	// also the authoritative list of option names — a published spec cannot know about a fork's
	// backend.
	if code, out, err := run(ctx, []string{"rclone", "config", "providers"}); err != nil || code != 0 {
		c.Problems = append(c.Problems, "could not list backends (rclone config providers)")
	} else {
		var provs []struct {
			Name   string `json:"Name"`
			Prefix string `json:"Prefix"`
		}
		if json.Unmarshal([]byte(out), &provs) != nil {
			c.Problems = append(c.Problems, "backend list was not valid JSON")
		} else {
			for _, p := range provs {
				name := p.Prefix
				if name == "" {
					name = p.Name
				}
				if name != "" {
					c.Backends = append(c.Backends, name)
				}
			}
			c.HasTeldrive = c.Supports("teldrive")
		}
	}

	// 3. The rc interface, via the in-process loopback so no daemon is needed. This is the
	// gate for everything the rc migration depends on: if it doesn't answer here, it won't
	// answer as a daemon either.
	if code, out, err := run(ctx, []string{"rclone", "rc", "--loopback", "options/info"}); err != nil || code != 0 {
		c.Problems = append(c.Problems, "rc is not available (rclone rc --loopback options/info failed) — the flag catalog and any daemon-backed transfer will not work")
	} else {
		var groups map[string]json.RawMessage
		if json.Unmarshal([]byte(out), &groups) != nil {
			c.Problems = append(c.Problems, "rc answered but options/info was not valid JSON")
		} else {
			c.RC, c.RCOptions = true, len(groups)
		}
	}

	return c
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSpace(s)
}
