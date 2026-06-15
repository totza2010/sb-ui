// Package inventory reads the Saltbox inventory (host_vars/localhost.yml) and
// builds a catalog of role defaults (defaults/main.yml). Port of the read +
// catalog parts of inventory.py (write/sections come in a later phase).
package inventory

import (
	"context"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/goccy/go-yaml"

	"sb-ui/internal/config"
	"sb-ui/internal/executor"
)

func invPath() string {
	return config.Get().SaltboxRepo + "/inventories/host_vars/localhost.yml"
}

// Read returns the inventory as a plain map (empty if absent/unreadable).
func Read() map[string]any {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	e := executor.Get()
	p := invPath()
	if ok, _ := e.FileExists(ctx, p); !ok {
		return map[string]any{}
	}
	content, err := e.ReadFile(ctx, p)
	if err != nil {
		return map[string]any{}
	}
	var m map[string]any
	if yaml.Unmarshal([]byte(content), &m) != nil || m == nil {
		return map[string]any{}
	}
	return m
}

// ── catalog ──────────────────────────────────────────────────────────────────

type Role struct {
	Role      string         `json:"role"`
	Repo      string         `json:"repo"`
	Variables map[string]any `json:"variables"`
	Sections  map[string]string `json:"sections"`
}

type Catalog struct {
	Roles map[string]*Role `json:"roles"`
}

var (
	catMu  sync.Mutex
	catVal *Catalog
	catTS  time.Time
)

const catTTL = 5 * time.Minute

func roleSources() [][2]string {
	c := config.Get()
	return [][2]string{
		{c.SaltboxRepo + "/roles", "saltbox"},
		{c.SandboxRepo + "/roles", "sandbox"},
	}
}

// GetCatalog scans every role's defaults/main.yml (cached 5 min).
func GetCatalog() *Catalog {
	catMu.Lock()
	if catVal != nil && time.Since(catTS) < catTTL {
		c := catVal
		catMu.Unlock()
		return c
	}
	catMu.Unlock()

	cat := buildCatalog()

	catMu.Lock()
	catVal = cat
	catTS = time.Now()
	catMu.Unlock()
	return cat
}

func InvalidateCatalog() {
	catMu.Lock()
	catVal = nil
	catMu.Unlock()
}

func buildCatalog() *Catalog {
	cat := &Catalog{Roles: map[string]*Role{}}
	e := executor.Get()
	for _, src := range roleSources() {
		dir, repo := src[0], src[1]
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		rc, out, err := e.Run(ctx, []string{
			"find", dir, "-maxdepth", "4", "-name", "main.yml",
			"-path", "*/defaults/main.yml", "-type", "f",
		}, "")
		cancel()
		if err != nil || rc != 0 {
			continue
		}
		paths := []string{}
		for _, l := range strings.Split(out, "\n") {
			if l = strings.TrimSpace(l); l != "" {
				paths = append(paths, l)
			}
		}
		readDefaults(e, paths, repo, cat)
	}
	return cat
}

// readDefaults reads role defaults concurrently (bounded) and fills cat.
func readDefaults(e executor.Executor, paths []string, repo string, cat *Catalog) {
	sem := make(chan struct{}, 8)
	var wg sync.WaitGroup
	var mu sync.Mutex
	for _, p := range paths {
		wg.Add(1)
		go func(p string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			content, err := e.ReadFile(ctx, p)
			cancel()
			if err != nil {
				return
			}
			role := roleNameFromPath(p)
			if role == "" {
				return
			}
			var vars map[string]any
			if yaml.Unmarshal([]byte(content), &vars) != nil || vars == nil {
				return
			}
			r := &Role{Role: role, Repo: repo, Variables: vars, Sections: parseSections(content)}
			mu.Lock()
			cat.Roles[role] = r
			mu.Unlock()
		}(p)
	}
	wg.Wait()
}

func roleNameFromPath(p string) string {
	parts := strings.Split(p, "/")
	for i, seg := range parts {
		if seg == "roles" && i+1 < len(parts) {
			return parts[i+1]
		}
	}
	return ""
}

// ── section banners ──────────────────────────────────────────────────────────

func isBanner(line string) bool {
	s := strings.TrimSpace(line)
	if len(s) < 6 {
		return false
	}
	for _, r := range s {
		if r != '#' {
			return false
		}
	}
	return true
}

var keyRE = regexp.MustCompile(`^([A-Za-z][A-Za-z0-9_]*)\s*:`)

// parseSections maps each top-level var → its `# Section` banner.
func parseSections(content string) map[string]string {
	lines := strings.Split(content, "\n")
	out := map[string]string{}
	current := ""
	for i, line := range lines {
		if isBanner(line) && i+2 < len(lines) && isBanner(lines[i+2]) {
			title := strings.TrimSpace(lines[i+1])
			if strings.HasPrefix(title, "#") {
				name := strings.TrimSpace(strings.TrimLeft(title, "#"))
				if name != "" && !strings.HasSuffix(name, "#") {
					current = name
				}
			}
			continue
		}
		if m := keyRE.FindStringSubmatch(line); m != nil && current != "" {
			if _, ok := out[m[1]]; !ok {
				out[m[1]] = current
			}
		}
	}
	return out
}
