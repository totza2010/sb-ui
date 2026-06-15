package api

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"sb-ui/internal/executor"
)

// mountDetail is a FUSE mount with liveness + disk usage, for the dashboard panel.
type mountDetail struct {
	Target string `json:"target"`
	Kind   string `json:"kind"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail"`
	Size   string `json:"size,omitempty"`
	Used   string `json:"used,omitempty"`
	UsePct string `json:"use_pct,omitempty"`
}

// listMounts returns each rclone/mergerfs mount with a liveness probe and df
// usage. Heavier than /api/status (adds df), so it's its own endpoint polled
// only by the dashboard.
func listMounts(w http.ResponseWriter, _ *http.Request) {
	e := executor.Get()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	const probe = `findmnt -rno TARGET,FSTYPE -t fuse.rclone,fuse.mergerfs,fuse.rclone-mount | ` +
		`while read -r t f; do ` +
		`if timeout 2 ls "$t" >/dev/null 2>&1; then s=ok; else s=stale; fi; ` +
		`d=$(timeout 3 df -hP "$t" 2>/dev/null | tail -1 | awk '{print $2"|"$3"|"$5}'); ` +
		`echo "$t|$f|$s|$d"; done`

	rc, out, _ := e.Run(ctx, []string{"sh", "-c", probe}, "")
	list := []mountDetail{}
	if rc == 0 {
		for _, l := range strings.Split(strings.TrimSpace(out), "\n") {
			if l = strings.TrimSpace(l); l == "" {
				continue
			}
			p := strings.Split(l, "|")
			if len(p) < 3 {
				continue
			}
			m := mountDetail{Target: p[0], Kind: friendlyKind(p[1]), OK: p[2] == "ok", Detail: "healthy"}
			if !m.OK {
				m.Detail = "not responding"
			}
			if len(p) >= 5 { // size|used (df's own pcent at p[5] is unreliable for mergerfs)
				m.Size, m.Used = p[3], p[4]
				// Compute the real ratio ourselves — df reports a bogus 100% for
				// mergerfs pools.
				if sz := parseHuman(p[3]); sz > 0 {
					m.UsePct = fmt.Sprintf("%d%%", int(parseHuman(p[4])/sz*100+0.5))
				}
			}
			list = append(list, m)
		}
	}
	writeJSON(w, http.StatusOK, list)
}

// parseHuman converts a df -h value like "383T" / "4.4P" / "15G" to bytes
// (1024-based, matching df -h). Returns 0 on "-" or parse failure.
func parseHuman(s string) float64 {
	s = strings.TrimSpace(s)
	if s == "" || s == "-" {
		return 0
	}
	mult := 1.0
	switch s[len(s)-1] {
	case 'K', 'k':
		mult = 1 << 10
	case 'M', 'm':
		mult = 1 << 20
	case 'G', 'g':
		mult = 1 << 30
	case 'T', 't':
		mult = 1 << 40
	case 'P', 'p':
		mult = 1 << 50
	case 'E', 'e':
		mult = 1 << 60
	}
	if mult != 1.0 {
		s = s[:len(s)-1]
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0
	}
	return v * mult
}
