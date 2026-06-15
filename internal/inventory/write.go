package inventory

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/goccy/go-yaml"

	"sb-ui/internal/executor"
)

// Write persists the inventory map to host_vars/localhost.yml.
func Write(data map[string]any) error {
	out, err := yaml.Marshal(data)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	e := executor.Get()
	dir := strings.TrimSuffix(invPath(), "/localhost.yml")
	_ = e.MakeDirs(ctx, dir)
	return e.WriteFile(ctx, invPath(), string(out))
}

// ── appdata path resolution (Jinja-lite) ─────────────────────────────────────

var tmplRE = regexp.MustCompile(
	`\{\{\s*([A-Za-z0-9_]+)\s*(?:\|\s*replace\(\s*['"]([^'"]*)['"]\s*,\s*['"]([^'"]*)['"]\s*\))?\s*\}\}`)

// resolveTemplate does best-effort {{ var }} / {{ var | replace('a','b') }}.
func resolveTemplate(tmpl string, ctx map[string]any, depth int) string {
	if !strings.Contains(tmpl, "{{") || depth > 12 {
		return tmpl
	}
	out := tmplRE.ReplaceAllStringFunc(tmpl, func(m string) string {
		sub := tmplRE.FindStringSubmatch(m)
		v, ok := ctx[sub[1]]
		if !ok {
			return m
		}
		val := fmt.Sprintf("%v", v)
		if sub[2] != "" {
			val = strings.ReplaceAll(val, sub[2], sub[3])
		}
		return val
	})
	if out == tmpl {
		return out
	}
	return resolveTemplate(out, ctx, depth+1)
}

type AppdataPath struct {
	Instance string `json:"instance"`
	Path     string `json:"path"`
}

// ResolveAppdata returns each instance's real /opt appdata folder, honouring
// inventory path overrides (e.g. sonarr_role_paths_folder). Port of app_appdata.
func ResolveAppdata(tag string) []AppdataPath {
	bare := strings.TrimPrefix(strings.TrimPrefix(tag, "sandbox-"), "mod-")
	role := strings.ReplaceAll(bare, "-", "_")
	cat := GetCatalog()
	inv := Read()
	var vars map[string]any
	if r := cat.Roles[role]; r != nil {
		vars = r.Variables
	}

	instances := []string{bare}
	if v, ok := inv[role+"_instances"]; ok {
		if list, ok := v.([]any); ok && len(list) > 0 {
			instances = nil
			for _, it := range list {
				if s := fmt.Sprintf("%v", it); s != "" {
					instances = append(instances, s)
				}
			}
		}
	}

	locKey := role + "_role_paths_location"
	var locTmpl string
	if s, ok := inv[locKey].(string); ok {
		locTmpl = s
	} else if vars != nil {
		if s, ok := vars[locKey].(string); ok {
			locTmpl = s
		}
	}

	var out []AppdataPath
	for _, inst := range instances {
		ctx := map[string]any{}
		for k, v := range vars {
			ctx[k] = v
		}
		for k, v := range inv {
			ctx[k] = v
		}
		ctx[role+"_name"] = inst
		if _, ok := ctx["server_appdata_path"]; !ok {
			ctx["server_appdata_path"] = "/opt"
		}
		path := "/opt/" + inst
		if locTmpl != "" {
			r := resolveTemplate(locTmpl, ctx, 0)
			if r != "" && !strings.Contains(r, "{{") && strings.HasPrefix(r, "/") {
				path = r
			}
		}
		out = append(out, AppdataPath{Instance: inst, Path: path})
	}
	return out
}
