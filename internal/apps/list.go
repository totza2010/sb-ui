package apps

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"sb-ui/internal/categories"
	"sb-ui/internal/docker"
	"sb-ui/internal/inventory"
)

type state struct {
	containers map[string]bool
	active     map[string]bool
	known      map[string]string
	binaries   map[string]bool
	images     map[string]string
}

func svcKeys(bare string) []string {
	svc := strings.ReplaceAll(bare, "-", "_")
	return []string{bare, svc, "saltbox_managed_" + bare, "saltbox_managed_" + svc}
}

func pick(pool map[string]bool, prefixes []string) string {
	var fallback string
	for k := range pool {
		for _, p := range prefixes {
			if strings.HasPrefix(k, p) {
				if !strings.HasSuffix(k, "_refresh") {
					return k
				}
				if fallback == "" {
					fallback = k
				}
			}
		}
	}
	return fallback
}

func pickKnown(pool map[string]string, prefixes []string) string {
	b := map[string]bool{}
	for k := range pool {
		b[k] = true
	}
	return pick(b, prefixes)
}

func (s state) classify(bare string) (bool, string, *string, *string) {
	if s.containers[bare] {
		return true, "container", strp("running"), nil
	}
	for _, k := range svcKeys(bare) {
		if s.active[k] {
			return true, "service", strp("active"), strp(k)
		}
		if _, ok := s.known[k]; ok {
			return true, "service", strp("inactive"), strp(k)
		}
	}
	prefixes := []string{bare + "@", bare + "_"}
	if m := pick(s.active, prefixes); m != "" {
		return true, "service", strp("active"), strp(m)
	}
	if m := pickKnown(s.known, prefixes); m != "" {
		return true, "service", strp("inactive"), strp(m)
	}
	if s.binaries[bare] {
		return true, "unknown", nil, nil
	}
	return false, "unknown", nil, nil
}

func (s state) info(name string) Instance {
	inst, kind, status, unit := s.classify(name)
	var img *string
	if kind == "container" {
		if v, ok := s.images[name]; ok {
			img = strp(v)
		}
	}
	return Instance{Name: name, Installed: inst, Kind: kind, ContainerStatus: status, Image: img, Unit: unit}
}

// List builds the full app list (installed + available).
func List() []App {
	saltboxTags, sandboxTags := readCache()
	action := actionTags()
	filtered := saltboxTags[:0:0]
	for _, t := range saltboxTags {
		if !action[t] {
			filtered = append(filtered, t)
		}
	}
	saltboxTags = filtered

	st := state{
		containers: docker.RunningNames(),
		active:     docker.ActiveServices(),
		known:      docker.KnownServices(),
		binaries:   installedBinaries(),
		images:     docker.ContainerImages(),
	}
	fuseActive := docker.FuseActive()
	inv := inventory.Read()
	cat := inventory.GetCatalog()

	allContainers := map[string]bool{}
	for k := range st.containers {
		allContainers[k] = true
	}
	for k := range st.images {
		allContainers[k] = true
	}

	type spec struct{ full, bare, repo string }
	var specs []spec
	for _, t := range saltboxTags {
		specs = append(specs, spec{t, t, "saltbox"})
	}
	for _, t := range sandboxTags {
		specs = append(specs, spec{"sandbox-" + t, t, "sandbox"})
	}

	// First pass: instance names + claimed set
	instNames := map[string][]string{}
	claimed := map[string]bool{}
	for _, sp := range specs {
		names := instanceNames(inv, sp.bare)
		instNames[sp.full] = names
		for _, n := range names {
			claimed[n] = true
		}
		claimed[sp.bare] = true
	}

	apps := make([]App, 0, len(specs))
	for _, sp := range specs {
		names := instNames[sp.full]
		instances := make([]Instance, len(names))
		for i, n := range names {
			instances[i] = st.info(n)
		}
		if storageRoles[sp.bare] && fuseActive {
			p := &instances[0]
			if p.ContainerStatus == nil || (*p.ContainerStatus != "running" && *p.ContainerStatus != "active") {
				p.Installed, p.Kind, p.ContainerStatus = true, "service", strp("active")
			}
		}
		primary := instances[0]
		ccat := categories.Categorize(sp.bare)
		installed := false
		for _, i := range instances {
			if i.Installed {
				installed = true
				break
			}
		}
		apps = append(apps, App{
			Tag: sp.full, Name: titleCase(sp.bare), Repo: sp.repo,
			Installed: installed, Kind: primary.Kind,
			ContainerStatus: primary.ContainerStatus, Image: primary.Image,
			Instances:  instances,
			Companions: companions(st, cat, inv, sp.bare, names, claimed, allContainers),
			Category:   ccat, OnDemand: categories.IsOnDemand(ccat),
		})
	}
	return apps
}

func instanceNames(inv map[string]any, bare string) []string {
	role := strings.ReplaceAll(bare, "-", "_")
	if v, ok := inv[role+"_instances"]; ok {
		if list, ok := v.([]any); ok && len(list) > 0 {
			out := []string{}
			for _, item := range list {
				if s := fmt.Sprintf("%v", item); s != "" {
					out = append(out, s)
				}
			}
			if len(out) > 0 {
				return out
			}
		}
	}
	return []string{bare}
}

// ── companions ───────────────────────────────────────────────────────────────

func companions(st state, cat *inventory.Catalog, inv map[string]any, bare string,
	names []string, claimed, allContainers map[string]bool) []Instance {

	own := map[string]bool{}
	for _, n := range names {
		own[n] = true
	}
	cand := map[string]bool{}
	for n := range declaredNames(cat, inv, bare, names) {
		cand[n] = true
	}
	for n := range dependsNames(cat, inv, bare) {
		cand[n] = true
	}
	var keys []string
	for n := range cand {
		if !claimed[n] && !own[n] && allContainers[n] {
			keys = append(keys, n)
		}
	}
	sort.Strings(keys)
	out := make([]Instance, 0, len(keys))
	for _, n := range keys {
		out = append(out, st.info(n))
	}
	return out
}

func roleVars(cat *inventory.Catalog, role, bare string) map[string]any {
	if r := cat.Roles[role]; r != nil {
		return r.Variables
	}
	if r := cat.Roles[bare]; r != nil {
		return r.Variables
	}
	return nil
}

func truthy(v any) bool {
	switch x := v.(type) {
	case bool:
		return x
	case string:
		s := strings.ToLower(strings.TrimSpace(x))
		return s == "true" || s == "yes" || s == "1"
	}
	return false
}

func declaredNames(cat *inventory.Catalog, inv map[string]any, bare string, names []string) map[string]bool {
	role := strings.ReplaceAll(bare, "-", "_")
	vars := roleVars(cat, role, bare)
	out := map[string]bool{}
	if vars == nil {
		return out
	}
	prefix := role + "_role_"
	nameRE := regexp.MustCompile(`\{\{\s*` + regexp.QuoteMeta(role) + `_name\s*\}\}`)
	for key, val := range vars {
		if !strings.HasPrefix(key, prefix) || !strings.HasSuffix(key, "_name") {
			continue
		}
		dep := key[len(prefix) : len(key)-len("_name")]
		if dep == "" {
			continue
		}
		deployKey := prefix + dep + "_deploy"
		dv, inVars := vars[deployKey]
		iv, inInv := inv[deployKey]
		if inVars || inInv {
			use := dv
			if inInv {
				use = iv
			}
			if !truthy(use) {
				continue
			}
		}
		var tmpl string
		if s, ok := inv[key].(string); ok {
			tmpl = s
		} else if s, ok := val.(string); ok {
			tmpl = s
		}
		if strings.TrimSpace(tmpl) == "" {
			continue
		}
		for _, inst := range names {
			nm := strings.TrimSpace(nameRE.ReplaceAllString(tmpl, inst))
			if nm != "" && !strings.Contains(nm, "{{") {
				out[nm] = true
			}
		}
	}
	return out
}

var quotedRE = regexp.MustCompile(`['"]([^'"]+)['"]`)

func dependsNames(cat *inventory.Catalog, inv map[string]any, bare string) map[string]bool {
	role := strings.ReplaceAll(bare, "-", "_")
	vars := roleVars(cat, role, bare)
	key := role + "_role_depends_on"
	var val string
	if s, ok := inv[key].(string); ok {
		val = s
	} else if vars != nil {
		if s, ok := vars[key].(string); ok {
			val = s
		}
	}
	out := map[string]bool{}
	if val == "" {
		return out
	}
	chunks := [][]string{}
	for _, m := range quotedRE.FindAllStringSubmatch(val, -1) {
		chunks = append(chunks, []string{m[1]})
	}
	if len(chunks) == 0 {
		chunks = append(chunks, []string{val})
	}
	for _, c := range chunks {
		for _, tok := range strings.Split(c[0], ",") {
			tok = strings.TrimSpace(tok)
			if tok != "" && !strings.Contains(tok, "{{") && !strings.Contains(tok, "/") && !strings.Contains(tok, " ") {
				out[tok] = true
			}
		}
	}
	return out
}

func titleCase(s string) string {
	parts := strings.Split(strings.ReplaceAll(s, "-", " "), " ")
	for i, p := range parts {
		if p != "" {
			parts[i] = strings.ToUpper(p[:1]) + p[1:]
		}
	}
	return strings.Join(parts, " ")
}
