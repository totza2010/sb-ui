package api

import (
	"context"
	"net/http"
	"sync"
	"time"

	"sb-ui/internal/executor"
	"sb-ui/internal/rcloneexec"
)

// What rclone is, asked rather than assumed.
//
// The host runs tgdrive/rclone — a fork on v1.75.0 that adds the teldrive backend — and the
// fork's version string is identical to upstream's, so there is nothing in a name to go on.
// Published API specs are worse than useless here: rclone-openapi is generated from upstream
// and has no teldrive at all, so a client typed from it would be missing the one backend
// everything on this host depends on. The binary in front of us is the only authority.

var (
	capsMu  sync.Mutex
	capsVal rcloneexec.Capabilities
	capsOK  bool
	capsTTL = 10 * time.Minute
)

// execRunner adapts the executor to the probe's Runner, so the probe works the same whether
// commands run locally or over SSH.
func execRunner(ctx context.Context, argv []string) (int, string, error) {
	return executor.Get().Run(ctx, argv, "")
}

// rcloneCaps returns the cached capabilities, probing when the cache is cold or stale. Probing
// runs three short rclone commands, so it is not something to do per request.
func rcloneCaps() rcloneexec.Capabilities {
	capsMu.Lock()
	defer capsMu.Unlock()
	if capsOK && time.Since(capsVal.ProbedAt) < capsTTL {
		return capsVal
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	capsVal, capsOK = rcloneexec.Probe(ctx, execRunner), true
	return capsVal
}

// invalidateRcloneCaps forces the next read to re-probe — after a self-update, say, which may
// have replaced the rclone binary underneath us.
func invalidateRcloneCaps() {
	capsMu.Lock()
	capsOK = false
	capsMu.Unlock()
}

// rcloneCapabilities reports what this rclone can do, including what could not be determined.
// The problems list is the point: an empty flag catalog used to look like "no flags" instead of
// "the call failed".
func rcloneCapabilities(w http.ResponseWriter, req *http.Request) {
	if req.URL.Query().Get("refresh") == "1" {
		invalidateRcloneCaps()
	}
	c := rcloneCaps()
	writeJSON(w, http.StatusOK, map[string]any{
		"version":      c.Version,
		"version_line": c.VersionLine,
		"fork":         c.Fork(),
		"has_teldrive": c.HasTeldrive,
		"rc":           c.RC,
		"rc_options":   c.RCOptions,
		"backends":     c.Backends,
		"problems":     c.Problems,
		"probed_at":    c.ProbedAt,
	})
}
