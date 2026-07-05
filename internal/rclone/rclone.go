// Package rclone reads rclone.conf remotes, mount templates, and the live
// mount/refresh status (rclone@/saltbox_managed_*/mergerfs units + FUSE mounts).
// Ports the rclone bits of config.py + rclone_conf.py.
package rclone

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"sb-ui/internal/executor"
)

func run(cmd ...string) (int, string) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	rc, out, err := executor.Get().Run(ctx, cmd, "")
	if err != nil {
		return -1, ""
	}
	return rc, out
}

// ── rclone.conf remotes ──────────────────────────────────────────────────────

var sectionRE = regexp.MustCompile(`^\[(.+)\]$`)

// Remotes parses ~/.config/rclone/rclone.conf into name → {key: val}.
func Remotes(confPath string) (map[string]map[string]string, string) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	e := executor.Get()
	if ok, _ := e.FileExists(ctx, confPath); !ok {
		return map[string]map[string]string{}, confPath
	}
	content, err := e.ReadFile(ctx, confPath)
	if err != nil {
		return map[string]map[string]string{}, confPath
	}
	out := map[string]map[string]string{}
	var cur string
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		if m := sectionRE.FindStringSubmatch(line); m != nil {
			cur = m[1]
			out[cur] = map[string]string{}
			continue
		}
		if cur != "" {
			if k, v, ok := strings.Cut(line, "="); ok {
				out[cur][strings.TrimSpace(k)] = strings.TrimSpace(v)
			}
		}
	}
	return out, confPath
}

var badField = regexp.MustCompile(`[\r\n]`)

// SaveRemotes writes name → {key: val} back to rclone.conf as INI, sorted for a
// stable file, then tightens perms (the file holds tokens / obscured secrets).
// Note: comments and original ordering are not preserved.
func SaveRemotes(confPath string, remotes map[string]map[string]string) error {
	names := make([]string, 0, len(remotes))
	for n := range remotes {
		if n == "" || strings.ContainsAny(n, "[]\r\n") {
			return fmt.Errorf("invalid remote name %q", n)
		}
		names = append(names, n)
	}
	sort.Strings(names)

	var b strings.Builder
	for i, n := range names {
		if i > 0 {
			b.WriteString("\n")
		}
		fmt.Fprintf(&b, "[%s]\n", n)
		keys := make([]string, 0, len(remotes[n]))
		for k := range remotes[n] {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			v := remotes[n][k]
			if badField.MatchString(k) || badField.MatchString(v) {
				return fmt.Errorf("invalid characters in %s.%s", n, k)
			}
			fmt.Fprintf(&b, "%s = %s\n", k, v)
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := executor.Get().WriteFile(ctx, confPath, b.String()); err != nil {
		return err
	}
	run("chmod", "600", confPath) // best-effort: keep secrets owner-only
	return nil
}

// ── status ───────────────────────────────────────────────────────────────────

type Unit struct {
	Unit   string `json:"unit"`
	Load   string `json:"load"`
	Active string `json:"active"`
	Sub    string `json:"sub"`
}

type Timer struct {
	Unit      string  `json:"unit"`
	Active    string  `json:"active"`
	Sub       string  `json:"sub"`
	Activates string  `json:"activates"`
	Next      *string `json:"next"`
}

type Mount struct {
	Target  string  `json:"target"`
	Source  string  `json:"source"`
	Fstype  string  `json:"fstype"`
	Size    *string `json:"size"`
	Used    *string `json:"used"`
	UsePct  *string `json:"use_pct"`
}

type Status struct {
	Version *string  `json:"version"`
	Units   []Unit   `json:"units"`
	Timers  []Timer  `json:"timers"`
	Mounts  []Mount  `json:"mounts"`
	Remotes []string `json:"remotes"`
}

func sp(s string) *string { return &s }

// GetStatus gathers the deep rclone/mounts status (port of get_rclone_status).
func GetStatus(remoteNames []string) Status {
	st := Status{Units: []Unit{}, Timers: []Timer{}, Mounts: []Mount{}, Remotes: remoteNames}

	if rc, out := run("rclone", "version"); rc == 0 && strings.TrimSpace(out) != "" {
		st.Version = sp(strings.TrimSpace(strings.Split(out, "\n")[0]))
	}

	// Live FUSE mounts → also derive source tokens for unit matching.
	tokens := map[string]bool{}
	_, mout := run("findmnt", "-rno", "TARGET,SOURCE,FSTYPE,SIZE,USED,USE%",
		"-t", "fuse.rclone,fuse.mergerfs,fuse.rclone-mount")
	for _, line := range strings.Split(mout, "\n") {
		f := strings.Fields(line)
		if len(f) < 3 {
			continue
		}
		m := Mount{Target: f[0], Source: f[1], Fstype: f[2]}
		if len(f) > 3 {
			m.Size = sp(f[3])
		}
		if len(f) > 4 {
			m.Used = sp(f[4])
		}
		if len(f) > 5 {
			m.UsePct = sp(f[5])
		}
		st.Mounts = append(st.Mounts, m)
		if tok := strings.Split(f[1], ":")[0]; len(tok) > 2 && !strings.Contains(tok, "/") {
			tokens[tok] = true
		}
		if i := strings.LastIndex(strings.TrimRight(f[0], "/"), "/"); i >= 0 {
			if tok := strings.TrimRight(f[0], "/")[i+1:]; len(tok) > 2 {
				tokens[tok] = true
			}
		}
	}

	// Units matching rclone/mergerfs/unionfs OR a mount source token.
	_, uout := run("systemctl", "list-units", "--all", "--plain", "--no-legend",
		"--no-pager", "--type=service", "--type=mount", "--type=timer")
	kw := []string{"rclone", "mergerfs", "unionfs"}
	for _, line := range strings.Split(uout, "\n") {
		f := strings.Fields(line)
		if len(f) < 4 {
			continue
		}
		name, low := f[0], strings.ToLower(f[0])
		match := false
		for _, k := range kw {
			if strings.Contains(low, k) {
				match = true
			}
		}
		if !match {
			for t := range tokens {
				if strings.Contains(name, t) {
					match = true
					break
				}
			}
		}
		if !match {
			continue
		}
		switch {
		case strings.HasSuffix(name, ".timer"):
			t := Timer{Unit: name, Active: f[2], Sub: f[3], Activates: strings.TrimSuffix(name, ".timer") + ".service"}
			if rc, nxt := run("systemctl", "show", name, "--property=NextElapseUSecRealtime", "--value"); rc == 0 {
				if n, err := strconv.ParseInt(strings.TrimSpace(nxt), 10, 64); err == nil && n > 0 {
					iso := time.Unix(n/1_000_000, 0).UTC().Format(time.RFC3339)
					t.Next = &iso
				}
			}
			st.Timers = append(st.Timers, t)
		case strings.HasSuffix(name, ".mount"):
			// shown under Active mounts (findmnt)
		case strings.HasSuffix(name, "_refresh.service"), strings.HasSuffix(name, "-refresh.service"):
			// shown via its timer
		default:
			st.Units = append(st.Units, Unit{Unit: name, Load: f[1], Active: f[2], Sub: f[3]})
		}
	}
	return st
}

// Logs returns journalctl output for a unit (falls back to sudo).
func Logs(unit string, lines int) string {
	n := strconv.Itoa(clamp(lines, 1, 2000))
	rc, out := run("journalctl", "-u", unit, "-n", n, "--no-pager", "--output=short-iso")
	if rc != 0 && strings.TrimSpace(out) == "" {
		_, out = run("sudo", "-n", "journalctl", "-u", unit, "-n", n, "--no-pager", "--output=short-iso")
	}
	return out
}

func clamp(n, lo, hi int) int {
	if n < lo {
		return lo
	}
	if n > hi {
		return hi
	}
	return n
}
