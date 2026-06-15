package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"sb-ui/internal/ansible"
	"sb-ui/internal/config"
	"sb-ui/internal/configfiles"
	"sb-ui/internal/executor"
	"sb-ui/internal/inventory"
	"sb-ui/internal/jobs"
	"sb-ui/internal/rclone"
)

// ── config files ─────────────────────────────────────────────────────────────

func getConfig(w http.ResponseWriter, req *http.Request) {
	name := chi.URLParam(req, "filename")
	if !configfiles.Allowed[name] {
		http.Error(w, "Unknown config file", http.StatusBadRequest)
		return
	}
	data, err := configfiles.Read(name)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"filename": name, "data": data})
}

func putConfig(w http.ResponseWriter, req *http.Request) {
	name := chi.URLParam(req, "filename")
	var body map[string]any
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := configfiles.Write(name, body); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func applyConfig(w http.ResponseWriter, req *http.Request) {
	name := chi.URLParam(req, "filename")
	tag := configfiles.ApplyTag(name)
	if tag == "" {
		http.Error(w, "No apply tag for this config file", http.StatusBadRequest)
		return
	}
	j := jobs.Create(tag, "apply")
	go ansible.RunPlaybook(context.Background(), j.ID, tag)
	writeJSON(w, http.StatusOK, map[string]any{"job_id": j.ID})
}

// ── inventory ────────────────────────────────────────────────────────────────

func getInventory(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"data": inventory.Read()})
}

func putInventory(w http.ResponseWriter, req *http.Request) {
	var body map[string]any
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := inventory.Write(body); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func getCatalog(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, inventory.GetCatalog())
}

func getAppdata(w http.ResponseWriter, req *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"paths": inventory.ResolveAppdata(chi.URLParam(req, "tag"))})
}

// ── rclone ───────────────────────────────────────────────────────────────────

// saltboxUser is the account Saltbox runs apps under (accounts.yml → user.name).
// rclone.conf lives in that user's home, not the SSH/connection user (which in
// local mode defaults to "seed"). Mirrors Saltbox's rclone_config_path.
func saltboxUser() string {
	if m, err := configfiles.Read("accounts"); err == nil {
		if u, ok := m["user"].(map[string]any); ok {
			if name, ok := u["name"].(string); ok && name != "" {
				return name
			}
		}
	}
	return config.Get().User
}

func rcloneConfPath() string {
	return "/home/" + saltboxUser() + "/.config/rclone/rclone.conf"
}

func rcloneRemotes(w http.ResponseWriter, _ *http.Request) {
	remotes, path := rclone.Remotes(rcloneConfPath())
	writeJSON(w, http.StatusOK, map[string]any{"path": path, "remotes": remotes})
}

func rcloneStatus(w http.ResponseWriter, _ *http.Request) {
	remotes, _ := rclone.Remotes(rcloneConfPath())
	names := make([]string, 0, len(remotes))
	for n := range remotes {
		names = append(names, n)
	}
	writeJSON(w, http.StatusOK, rclone.GetStatus(names))
}

func rcloneLogs(w http.ResponseWriter, req *http.Request) {
	unit := req.URL.Query().Get("unit")
	if strings.ContainsAny(unit, " ;|&$`\n") || unit == "" {
		http.Error(w, "Invalid unit", http.StatusBadRequest)
		return
	}
	lines, _ := strconv.Atoi(req.URL.Query().Get("lines"))
	if lines == 0 {
		lines = 200
	}
	writeJSON(w, http.StatusOK, map[string]any{"unit": unit, "logs": rclone.Logs(unit, lines)})
}

func mountTemplates(w http.ResponseWriter, _ *http.Request) {
	const dir = "/opt/mount-templates"
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	rc, out, _ := executor.Get().Run(ctx, []string{
		"find", dir, "-name", "*.j2", "-type", "f", "-not", "-path", "*/.*",
	}, "")
	templates := []string{}
	if rc == 0 {
		prefix := dir + "/"
		for _, line := range strings.Split(out, "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			rel := strings.TrimSuffix(strings.TrimPrefix(line, prefix), ".j2")
			if strings.Contains(rel, "/") {
				templates = append(templates, strings.TrimSuffix(line, ".j2")+".j2")
			} else {
				templates = append(templates, rel)
			}
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"templates": templates, "path": dir})
}
