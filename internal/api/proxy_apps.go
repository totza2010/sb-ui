package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"sb-ui/internal/ansible"
	"sb-ui/internal/apps"
	"sb-ui/internal/inventory"
	"sb-ui/internal/jobs"
)

// Per-app "Expose on Tailscale" for containerized Saltbox apps. We don't use the
// list provider for these — instead we write tsdproxy Docker labels into the app's
// Saltbox inventory override (`<role>_role_docker_labels_custom`) and reinstall, so
// tsdproxy's Docker provider picks the container up. This keeps 1 app = 1 provider.

// roleFromTag converts an app tag (e.g. "sandbox-foo") to its Ansible role name.
func roleFromTag(tag string) string {
	bare := strings.TrimPrefix(strings.TrimPrefix(tag, "sandbox-"), "mod-")
	return strings.ReplaceAll(bare, "-", "_")
}

func tagBare(tag string) string {
	return strings.TrimPrefix(strings.TrimPrefix(tag, "sandbox-"), "mod-")
}

// toStrMap coerces a YAML-decoded map (any value types) to map[string]string.
func toStrMap(v any) map[string]string {
	out := map[string]string{}
	switch m := v.(type) {
	case map[string]any:
		for k, val := range m {
			out[k] = fmt.Sprintf("%v", val)
		}
	case map[any]any:
		for k, val := range m {
			out[fmt.Sprintf("%v", k)] = fmt.Sprintf("%v", val)
		}
	}
	return out
}

// appInstances returns the configured instance names for a multi-instance app, or
// nil for a single-instance app.
func appInstances(role string, inv map[string]any) []string {
	if v, ok := inv[role+"_instances"]; ok {
		if list, ok := v.([]any); ok && len(list) > 0 {
			var out []string
			for _, it := range list {
				if s := strings.TrimSpace(fmt.Sprintf("%v", it)); s != "" {
					out = append(out, s)
				}
			}
			return out
		}
	}
	return nil
}

// appWebPort returns the app's container web port (inventory override → role default).
func appWebPort(role string, inv map[string]any) string {
	key := role + "_role_web_port"
	if v, ok := inv[key]; ok {
		return fmt.Sprintf("%v", v)
	}
	if r := inventory.GetCatalog().Roles[role]; r != nil {
		if v, ok := r.Variables[key]; ok {
			return fmt.Sprintf("%v", v)
		}
	}
	return ""
}

// portFromLabel extracts the container port from a "443/https:8989/http" port label.
func portFromLabel(s string) string {
	parts := strings.SplitN(s, ":", 2)
	if len(parts) < 2 {
		return ""
	}
	p := parts[1]
	if i := strings.Index(p, "/"); i >= 0 {
		p = p[:i]
	}
	return p
}

type appTSState struct {
	Tag         string   `json:"tag"`
	App         string   `json:"app"`     // display name
	Enabled     bool     `json:"enabled"` // tsdproxy.enable label present
	Name        string   `json:"name"`    // tailnet hostname (single-instance only)
	Port        string   `json:"port"`    // container port
	DefaultPort string   `json:"default_port"`
	Label       string   `json:"label"`
	Icon        string   `json:"icon"`
	Hidden      bool     `json:"hidden"`
	Instances   []string `json:"instances"` // >1 ⇒ multi-instance (names are auto, per instance)
}

func appTSStateFrom(tag string, inv map[string]any) appTSState {
	role := roleFromTag(tag)
	custom := toStrMap(inv[role+"_role_docker_labels_custom"])
	st := appTSState{
		Tag:         tag,
		App:         tagBare(tag),
		Enabled:     custom["tsdproxy.enable"] == "true",
		Name:        custom["tsdproxy.name"],
		Port:        portFromLabel(custom["tsdproxy.port.1"]),
		DefaultPort: appWebPort(role, inv),
		Label:       custom["tsdproxy.dash.label"],
		Icon:        custom["tsdproxy.dash.icon"],
		Hidden:      custom["tsdproxy.dash.visible"] == "false",
		Instances:   appInstances(role, inv),
	}
	// Multi-instance uses a {{ <role>_name }} template — don't surface it as an
	// editable literal name.
	if len(st.Instances) > 1 || strings.Contains(st.Name, "{{") {
		st.Name = ""
	}
	if st.Name == "" && len(st.Instances) <= 1 {
		st.Name = tagBare(tag)
	}
	if st.Port == "" {
		st.Port = st.DefaultPort
	}
	return st
}

// proxyAppsList returns every installed container app with its current expose state.
func proxyAppsList(w http.ResponseWriter, _ *http.Request) {
	inv := inventory.Read()
	var out []appTSState
	for _, a := range apps.List() {
		if !a.Installed || a.Kind != "container" {
			continue
		}
		s := appTSStateFrom(a.Tag, inv)
		s.App = a.Name
		out = append(out, s)
	}
	writeJSON(w, http.StatusOK, map[string]any{"apps": out})
}

// appTailscalePut writes (or clears) the tsdproxy Docker labels in the app's
// inventory override and reinstalls it so the labels take effect.
func appTailscalePut(w http.ResponseWriter, req *http.Request) {
	tag := chi.URLParam(req, "tag")
	role := roleFromTag(tag)
	var b struct {
		Enabled bool   `json:"enabled"`
		Name    string `json:"name"`
		Port    string `json:"port"`
		Label   string `json:"label"`
		Icon    string `json:"icon"`
		Hidden  bool   `json:"hidden"`
	}
	_ = json.NewDecoder(req.Body).Decode(&b)
	b.Name = strings.TrimSpace(b.Name)
	b.Port = strings.TrimSpace(b.Port)

	inv := inventory.Read()
	multi := len(appInstances(role, inv)) > 1
	// Multi-instance: one label dict is shared by every instance, so the name must
	// be a per-instance template; otherwise all instances collide on one node.
	nameLabel := b.Name
	if multi {
		nameLabel = "{{ " + role + "_name }}"
	} else {
		if nameLabel == "" {
			nameLabel = tagBare(tag)
		}
		if strings.ContainsAny(nameLabel, " \t\n/:") {
			http.Error(w, "name must be a bare tailnet hostname", http.StatusBadRequest)
			return
		}
	}

	key := role + "_role_docker_labels_custom"
	custom := toStrMap(inv[key]) // preserve any non-tsdproxy custom labels
	for k := range custom {       // drop our previous tsdproxy.* keys
		if strings.HasPrefix(k, "tsdproxy.") {
			delete(custom, k)
		}
	}
	if b.Enabled {
		if b.Port == "" {
			b.Port = appWebPort(role, inv)
		}
		if b.Port == "" {
			http.Error(w, "container port required (couldn't auto-detect)", http.StatusBadRequest)
			return
		}
		custom["tsdproxy.enable"] = "true"
		custom["tsdproxy.name"] = nameLabel
		custom["tsdproxy.port.1"] = "443/https:" + b.Port + "/http"
		if b.Label != "" {
			custom["tsdproxy.dash.label"] = strings.TrimSpace(b.Label)
		}
		if b.Icon != "" {
			custom["tsdproxy.dash.icon"] = strings.TrimSpace(b.Icon)
		}
		if b.Hidden {
			custom["tsdproxy.dash.visible"] = "false"
		}
	}
	if len(custom) == 0 {
		delete(inv, key)
	} else {
		inv[key] = custom
	}
	if err := inventory.Write(inv); err != nil {
		http.Error(w, "write inventory failed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	inventory.InvalidateCatalog()

	// Reinstall so the container is recreated with the new labels, then restart
	// tsdproxy — during the reinstall the container cycles, which can trip a proxy's
	// health check and leave its tsnet node stuck; a restart re-establishes all
	// nodes cleanly now that the container is back up.
	j := jobs.Create(tag, "reinstall")
	go func() {
		ansible.RunPlaybook(context.Background(), j.ID, tag)
		if hostHas(tsdBin) {
			sudoRun("systemctl", "restart", "tsdproxy")
		}
	}()
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "job_id": j.ID})
}
