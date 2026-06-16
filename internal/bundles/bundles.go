// Package bundles computes Saltbox meta/bundle tags (core/mounts/profiles/…) and
// their member roles from the live repo: static play tags + dynamic include_role
// edges (gated on ansible_run_tags).
package bundles

import (
	"context"
	"regexp"
	"strings"
	"sync"
	"time"

	"sb-ui/internal/config"
	"sb-ui/internal/executor"
)

type Pull struct {
	Role        string `json:"role"`
	Via         string `json:"via"`
	Conditional bool   `json:"conditional"`
}

type Bundle struct {
	Tag         string   `json:"tag"`
	Label       string   `json:"label"`
	Kind        string   `json:"kind"`
	Description string   `json:"description"`
	Roles       []string `json:"roles"`
	Pulls       []Pull   `json:"pulls"`
	Computed    bool     `json:"computed"`
}

type meta struct{ tag, label, kind, desc string }

var curated = []meta{
	{"saltbox", "Saltbox (full)", "profile", "Full media-server profile — core plus the standard app set."},
	{"mediabox", "Mediabox", "profile", "Media server without the download automation stack."},
	{"feederbox", "Feederbox", "profile", "Downloader / automation box without a media server."},
	{"core", "Core", "bundle", "Base system — preinstall + mounts + docker plus core service roles."},
	{"preinstall", "Pre-install", "bundle", "First-run base: user, shell, rclone and mount scaffolding."},
	{"mounts", "Mounts", "bundle", "Rclone mount stack. The remote role include_role's rclone, so this re-installs rclone too."},
	{"docker", "Docker", "bundle", "Docker engine (plus the NVIDIA runtime when enabled)."},
	{"media-server", "Media server", "dynamic", "Installs the media server chosen in settings (Plex / Emby / Jellyfin)."},
	{"download-clients", "Download clients", "dynamic", "Installs the download clients chosen in settings."},
	{"download-indexers", "Download indexers", "dynamic", "Installs the indexers chosen in settings."},
}

var (
	mu    sync.Mutex
	cache []Bundle
)

func ClearCache() {
	mu.Lock()
	cache = nil
	mu.Unlock()
}

var roleTagRE = regexp.MustCompile(`role:\s*([A-Za-z0-9_]+).*?tags:\s*\[([^\]]*)\]`)

func roleTags() map[string]map[string]bool {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	content, err := executor.Get().ReadFile(ctx, config.Get().SaltboxPlaybook())
	if err != nil {
		return nil
	}
	out := map[string]map[string]bool{}
	for _, m := range roleTagRE.FindAllStringSubmatch(content, -1) {
		role := m[1]
		if out[role] == nil {
			out[role] = map[string]bool{}
		}
		for _, t := range strings.Split(m[2], ",") {
			t = strings.TrimSpace(strings.Trim(strings.TrimSpace(t), `'"`))
			if t != "" {
				out[role][t] = true
			}
		}
	}
	return out
}

type edge struct {
	parent, child string
	gate          map[string]bool // run-tags the include is restricted to, nil = always
	conditional   bool
}

var (
	incLine  = regexp.MustCompile(`(?:include_role|import_role)\s*:\s*$`)
	nameLine = regexp.MustCompile(`^\s*name\s*:\s*["']?([A-Za-z0-9_]+)["']?\s*$`)
	gateRE   = regexp.MustCompile(`["']([A-Za-z0-9_-]+)["']\s+in\s+ansible_run_tags`)
	grepPfx  = regexp.MustCompile(`^(.+?)[:-]\d+[:-](.*)$`)
)

func includeEdges() []edge {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	rc, out, err := executor.Get().Run(ctx, []string{
		"grep", "-rnE", "-A4", `(include_role|import_role)[[:space:]]*:`, "roles", "--include=*.yml",
	}, config.Get().SaltboxRepo)
	if err != nil || (rc != 0 && rc != 1) || strings.TrimSpace(out) == "" {
		return nil
	}
	var edges []edge
	var pend *edge
	flush := func() {
		if pend != nil && pend.parent != "" && pend.child != "" {
			edges = append(edges, *pend)
		}
		pend = nil
	}
	for _, raw := range strings.Split(out, "\n") {
		if raw == "--" {
			flush()
			continue
		}
		m := grepPfx.FindStringSubmatch(raw)
		if m == nil {
			continue
		}
		path, body := m[1], m[2]
		parts := strings.Split(path, "/")
		parent := ""
		if len(parts) > 2 && parts[0] == "roles" {
			parent = parts[1]
		}
		if pend != nil {
			for _, g := range gateRE.FindAllStringSubmatch(body, -1) {
				pend.gate[g[1]] = true
			}
			if strings.Contains(body, "when:") {
				pend.conditional = true
			}
			if nm := nameLine.FindStringSubmatch(body); nm != nil && pend.child == "" {
				pend.child = nm[1]
			}
		}
		if incLine.MatchString(strings.TrimSpace(body)) {
			flush()
			pend = &edge{parent: parent, gate: map[string]bool{}}
		}
	}
	flush()
	return edges
}

func rolesForTag(tag string, rt map[string]map[string]bool, edges []edge) ([]string, []Pull) {
	direct := map[string]bool{}
	for r, tags := range rt {
		if tags[tag] {
			direct[r] = true
		}
	}
	running := map[string]bool{}
	for r := range direct {
		running[r] = true
	}
	pulls := map[string]Pull{}
	for changed := true; changed; {
		changed = false
		for _, e := range edges {
			if !running[e.parent] {
				continue
			}
			if len(e.gate) > 0 && !e.gate[tag] {
				continue
			}
			if !running[e.child] {
				running[e.child] = true
				changed = true
			}
			if !direct[e.child] {
				if _, ok := pulls[e.child]; !ok {
					pulls[e.child] = Pull{Role: e.child, Via: e.parent, Conditional: e.conditional || len(e.gate) > 0}
				}
			}
		}
	}
	dr := sortedKeys(direct)
	pl := make([]Pull, 0, len(pulls))
	for _, k := range sortedMapKeys(pulls) {
		pl = append(pl, pulls[k])
	}
	return dr, pl
}

func GetBundles(refresh bool) []Bundle {
	mu.Lock()
	if cache != nil && !refresh {
		c := cache
		mu.Unlock()
		return c
	}
	mu.Unlock()

	rt := roleTags()
	if rt == nil {
		return fallback()
	}
	edges := includeEdges()
	out := make([]Bundle, 0, len(curated))
	for _, b := range curated {
		direct, pulls := rolesForTag(b.tag, rt, edges)
		roles := direct
		if b.kind == "profile" {
			roles = []string{"core", "+ " + itoa(len(direct)) + " roles total"}
		} else if len(roles) == 0 {
			roles = []string{"from settings"}
		}
		out = append(out, Bundle{
			Tag: b.tag, Label: b.label, Kind: b.kind, Description: b.desc,
			Roles: roles, Pulls: pulls, Computed: true,
		})
	}
	mu.Lock()
	cache = out
	mu.Unlock()
	return out
}

func fallback() []Bundle {
	out := make([]Bundle, 0, len(curated))
	for _, b := range curated {
		out = append(out, Bundle{Tag: b.tag, Label: b.label, Kind: b.kind, Description: b.desc,
			Roles: []string{"from settings"}, Pulls: []Pull{}, Computed: false})
	}
	return out
}
