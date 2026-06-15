package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"sb-ui/internal/executor"
)

// remoteInfo is one rclone remote (with its backend type) checked directly via
// `rclone about`.
type remoteInfo struct {
	Name  string `json:"name"`
	Type  string `json:"type"`
	OK    bool   `json:"ok"`
	Used  string `json:"used,omitempty"`
	Total string `json:"total,omitempty"`
}

type storageResp struct {
	Remotes []remoteInfo `json:"remotes"`
	Local   *mountDetail `json:"local"`
}

// rclone about results are cached — they hit the cloud API and change slowly.
var (
	remotesMu     sync.Mutex
	remotesCache  []remoteInfo
	remotesCached time.Time
)

const remotesTTL = 5 * time.Minute

func storageInfo(w http.ResponseWriter, _ *http.Request) {
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	writeJSON(w, http.StatusOK, storageResp{
		Remotes: cloudRemotes(ctx),
		Local:   localDisk(ctx),
	})
}

// cloudRemotes lists rclone remotes and checks each directly with `rclone about`
// (cached). ok = about succeeded; used/total from its JSON when available.
func cloudRemotes(ctx context.Context) []remoteInfo {
	remotesMu.Lock()
	if time.Since(remotesCached) < remotesTTL && remotesCache != nil {
		defer remotesMu.Unlock()
		return remotesCache
	}
	remotesMu.Unlock()

	e := executor.Get()
	conf := rcloneConfPath()
	// `listremotes --long` gives "name: type"; one `rclone about` per remote in
	// parallel, each guarded by `timeout`.
	probe := `conf=` + shArg(conf) + `; rclone --config "$conf" listremotes --long 2>/dev/null | ` +
		`while read -r name typ; do ( j=$(timeout 8 rclone --config "$conf" about "$name" --json 2>/dev/null); ` +
		`if [ -n "$j" ]; then echo "$name|$typ|ok|$j"; else echo "$name|$typ|down|"; fi ) & done; wait`

	rc, out, _ := e.Run(ctx, []string{"sh", "-c", probe}, "")
	list := []remoteInfo{}
	if rc == 0 {
		for _, l := range strings.Split(strings.TrimSpace(out), "\n") {
			if l = strings.TrimSpace(l); l == "" {
				continue
			}
			p := strings.SplitN(l, "|", 4)
			if len(p) < 3 {
				continue
			}
			r := remoteInfo{Name: strings.TrimSuffix(p[0], ":"), Type: strings.TrimSpace(p[1]), OK: p[2] == "ok"}
			if len(p) == 4 && p[3] != "" {
				var a struct {
					Total int64 `json:"total"`
					Used  int64 `json:"used"`
				}
				if json.Unmarshal([]byte(p[3]), &a) == nil {
					if a.Used > 0 {
						r.Used = humanBytes(a.Used)
					}
					if a.Total > 0 {
						r.Total = humanBytes(a.Total)
					}
				}
			}
			list = append(list, r)
		}
	}

	remotesMu.Lock()
	remotesCache, remotesCached = list, time.Now()
	remotesMu.Unlock()
	return list
}

// localDisk probes /mnt/local (the external HDD) directly: liveness + df.
func localDisk(ctx context.Context) *mountDetail {
	e := executor.Get()
	const probe = `if timeout 2 ls /mnt/local >/dev/null 2>&1; then s=ok; else s=stale; fi; ` +
		`d=$(timeout 3 df -hP /mnt/local 2>/dev/null | tail -1 | awk '{print $2"|"$3}'); echo "$s|$d"`
	rc, out, _ := e.Run(ctx, []string{"sh", "-c", probe}, "")
	if rc != 0 {
		return nil
	}
	p := strings.Split(strings.TrimSpace(out), "|")
	if len(p) < 1 || p[0] == "" {
		return nil
	}
	m := &mountDetail{Target: "/mnt/local", Kind: "disk", OK: p[0] == "ok", Detail: "healthy"}
	if !m.OK {
		m.Detail = "not responding"
	}
	if len(p) >= 3 {
		m.Size, m.Used = p[1], p[2]
		if sz := parseHuman(p[1]); sz > 0 {
			m.UsePct = fmt.Sprintf("%d%%", int(parseHuman(p[2])/sz*100+0.5))
		}
	}
	return m
}

// shArg single-quotes a value for safe embedding in an sh -c script.
func shArg(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// humanBytes formats bytes as a compact 1024-based string (e.g. 1.5T).
func humanBytes(b int64) string {
	const u = 1024.0
	v := float64(b)
	for _, s := range []string{"B", "K", "M", "G", "T", "P"} {
		if v < u || s == "P" {
			if s == "B" {
				return fmt.Sprintf("%d%s", int(v), s)
			}
			return fmt.Sprintf("%.1f%s", v, s)
		}
		v /= u
	}
	return fmt.Sprintf("%.1fP", v)
}
