package api

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"sb-ui/internal/config"
	dockerpkg "sb-ui/internal/docker"
	"sb-ui/internal/executor"
)

// statusItem is one indicator in the always-visible status bar. List holds an
// optional per-item breakdown (e.g. each mount + its liveness) shown on hover.
type statusItem struct {
	OK     bool        `json:"ok"`
	Label  string      `json:"label"`
	Detail string      `json:"detail,omitempty"`
	List   []mountInfo `json:"list,omitempty"`
}

// mountInfo is one FUSE mount with its real liveness status.
type mountInfo struct {
	Target string `json:"target"`
	Kind   string `json:"kind"`   // rclone | mergerfs
	OK     bool   `json:"ok"`     // mount point responds (not stale)
	Detail string `json:"detail"` // healthy | not responding
}

// systemStatus is a lightweight health summary for the status bar: whether the
// executor can reach the host (SSH/local), rclone/mergerfs mounts, and Docker.
func systemStatus(w http.ResponseWriter, _ *http.Request) {
	c := config.Get()
	e := executor.Get()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	// Connection — can we run anything on the target at all?
	rc, _, err := e.Run(ctx, []string{"true"}, "")
	connOK := err == nil && rc == 0
	conn := statusItem{OK: connOK, Label: "Local", Detail: "same host"}
	if c.IsRemote() {
		conn.Label, conn.Detail = "SSH", c.User+"@"+c.Host
	}
	if !connOK {
		conn.Detail = "unreachable"
	}

	mounts := statusItem{Label: "Mounts", Detail: "—"}
	docker := statusItem{Label: "Docker", Detail: "—"}
	if connOK {
		// List each FUSE mount and probe whether it actually responds (rclone
		// mounts can go stale). `timeout 2 ls` per target → ok | stale.
		const probe = `findmnt -rno TARGET,FSTYPE -t fuse.rclone,fuse.mergerfs,fuse.rclone-mount | ` +
			`while read -r t f; do if timeout 2 ls "$t" >/dev/null 2>&1; then echo "$t|$f|ok"; else echo "$t|$f|stale"; fi; done`
		mrc, mout, _ := e.Run(ctx, []string{"sh", "-c", probe}, "")
		if mrc == 0 {
			var list []mountInfo
			healthy := 0
			for _, l := range strings.Split(strings.TrimSpace(mout), "\n") {
				if l = strings.TrimSpace(l); l == "" {
					continue
				}
				p := strings.Split(l, "|")
				if len(p) != 3 {
					continue
				}
				ok := p[2] == "ok"
				detail := "healthy"
				if !ok {
					detail = "not responding"
				} else {
					healthy++
				}
				list = append(list, mountInfo{Target: p[0], Kind: friendlyKind(p[1]), OK: ok, Detail: detail})
			}
			switch {
			case len(list) == 0:
				mounts.Detail = "none"
			case healthy == len(list):
				mounts.OK, mounts.Detail, mounts.List = true, fmt.Sprintf("%d active", len(list)), list
			default:
				mounts.Detail, mounts.List = fmt.Sprintf("%d/%d ok", healthy, len(list)), list
			}
		}

		// Docker daemon reachable + running count — shares the cached snapshot
		// used by the container/app lists instead of a separate `docker ps`.
		if dockerpkg.Reachable() {
			docker.OK, docker.Detail = true, fmt.Sprintf("%d running", dockerpkg.RunningCount())
		} else {
			docker.Detail = "down"
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"connection": conn, "mounts": mounts, "docker": docker,
	})
}

func friendlyKind(fstype string) string {
	switch fstype {
	case "fuse.mergerfs":
		return "mergerfs"
	case "fuse.rclone", "fuse.rclone-mount":
		return "rclone"
	default:
		return strings.TrimPrefix(fstype, "fuse.")
	}
}
