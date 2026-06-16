package api

import (
	"context"
	"encoding/json"
	"net/http"
	"path"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"sb-ui/internal/ansible"
	"sb-ui/internal/config"
	"sb-ui/internal/executor"
	"sb-ui/internal/jobs"
	"sb-ui/internal/patches"
	"sb-ui/internal/rolegen"
)

func rolePreview(w http.ResponseWriter, req *http.Request) {
	var spec rolegen.Spec
	if err := json.NewDecoder(req.Body).Decode(&spec); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"defaults": rolegen.GenerateDefaults(spec),
		"tasks":    rolegen.GenerateTasks(spec),
	})
}

// roleCommit generates a user role into saltbox_mod (the persistent home for
// own roles — survives `sb update`), registers it, and installs `mod-<name>`.
func roleCommit(w http.ResponseWriter, req *http.Request) {
	var spec rolegen.Spec
	if err := json.NewDecoder(req.Body).Decode(&spec); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if !validRoleName(spec.Name) {
		http.Error(w, "Invalid role name", http.StatusBadRequest)
		return
	}
	if err := rolegen.WriteRoleMod(spec); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	_ = rolegen.PatchModYml(spec.Name)
	tag := "mod-" + spec.Name
	j := jobs.Create(tag, "install")
	go ansible.RunPlaybook(context.Background(), j.ID, tag)
	writeJSON(w, http.StatusOK, map[string]any{"job_id": j.ID})
}

// listModRoles lists user-managed saltbox_mod roles (excluding sb-ui's own
// `sbui` role — you don't manage the manager from inside itself).
func listModRoles(w http.ResponseWriter, _ *http.Request) {
	modBase := config.Get().SaltboxModRepo + "/roles"
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	rc, out, _ := executor.Get().Run(ctx, []string{
		"find", modBase, "-maxdepth", "1", "-mindepth", "1", "-type", "d", "-printf", `%f\n`,
	}, "")
	registered := registeredModRoles(ctx)
	roles := []map[string]any{}
	if rc == 0 {
		for _, name := range strings.Split(out, "\n") {
			name = strings.TrimSpace(name)
			if name == "" || name == "sbui" {
				continue
			}
			roles = append(roles, map[string]any{
				"name": name, "registered": registered[name],
			})
		}
	}
	sortRoleEntries(roles)
	writeJSON(w, http.StatusOK, map[string]any{"roles": roles, "base": modBase})
}

// registeredModRoles parses saltbox_mod.yml for `role: <name>` entries.
func registeredModRoles(ctx context.Context) map[string]bool {
	set := map[string]bool{}
	content, err := executor.Get().ReadFile(ctx, config.Get().SaltboxModPlaybook())
	if err != nil {
		return set
	}
	for _, m := range roleEntryRE.FindAllStringSubmatch(content, -1) {
		set[m[1]] = true
	}
	return set
}

var roleEntryRE = regexp.MustCompile(`role:\s*([A-Za-z0-9_-]+)`)

// removeModRole uninstalls a user role: stop+remove its container, delete the
// role directory, and unregister it from saltbox_mod.yml. Refuses `sbui`.
func removeModRole(w http.ResponseWriter, req *http.Request) {
	role := chi.URLParam(req, "role")
	if !validRoleName(role) || role == "sbui" {
		http.Error(w, "Invalid role", http.StatusBadRequest)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	e := executor.Get()
	// Best-effort container teardown (role name is the usual container name).
	_, _, _ = e.Run(ctx, []string{"docker", "rm", "-f", role}, "")
	dir := config.Get().SaltboxModRepo + "/roles/" + role
	if rc, out, err := e.Run(ctx, []string{"rm", "-rf", dir}, ""); err != nil || rc != 0 {
		msg := strings.TrimSpace(out)
		if err != nil {
			msg = err.Error()
		}
		http.Error(w, "Failed to delete role: "+msg, http.StatusInternalServerError)
		return
	}
	_ = rolegen.UnregisterModYml(role)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func sortRoleEntries(roles []map[string]any) {
	sort.Slice(roles, func(i, j int) bool {
		return roles[i]["name"].(string) < roles[j]["name"].(string)
	})
}

func validRoleName(s string) bool {
	if s == "" || len(s) > 64 {
		return false
	}
	return regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`).MatchString(s)
}

func roleBase(role, repo string) string {
	switch repo {
	case "sandbox":
		return config.Get().SandboxRepo + "/roles/" + role
	case "mod":
		return config.Get().SaltboxModRepo + "/roles/" + role
	}
	return config.Get().SaltboxRepo + "/roles/" + role
}

// safeJoin resolves rel under base, rejecting traversal (posix paths).
func safeJoin(base, rel string) (string, bool) {
	resolved := path.Clean(base + "/" + rel)
	if resolved == base || strings.HasPrefix(resolved, strings.TrimRight(base, "/")+"/") {
		return resolved, true
	}
	return "", false
}

func repoParam(req *http.Request) string {
	if r := req.URL.Query().Get("repo"); r != "" {
		return r
	}
	return "saltbox"
}

func roleFiles(w http.ResponseWriter, req *http.Request) {
	role := chi.URLParam(req, "role")
	repo := repoParam(req)
	base := roleBase(role, repo)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	rc, out, _ := executor.Get().Run(ctx, []string{
		"find", base, "-type", "f", "-not", "-path", "*/.*", "-not", "-name", "*.pyc",
	}, "")
	files := []string{}
	if rc == 0 {
		prefix := strings.TrimRight(base, "/") + "/"
		for _, l := range strings.Split(out, "\n") {
			l = strings.TrimSpace(l)
			if strings.HasPrefix(l, prefix) {
				files = append(files, strings.TrimPrefix(l, prefix))
			}
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"files": files, "base": base})
}

func roleReadFile(w http.ResponseWriter, req *http.Request) {
	role, repo := chi.URLParam(req, "role"), repoParam(req)
	full, ok := safeJoin(roleBase(role, repo), req.URL.Query().Get("path"))
	if !ok {
		http.Error(w, "Path outside role directory", http.StatusBadRequest)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	content, err := executor.Get().ReadFile(ctx, full)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"path": req.URL.Query().Get("path"), "content": content})
}

func roleWriteFile(w http.ResponseWriter, req *http.Request) {
	role, repo := chi.URLParam(req, "role"), repoParam(req)
	rel := req.URL.Query().Get("path")
	full, ok := safeJoin(roleBase(role, repo), rel)
	if !ok {
		http.Error(w, "Path outside role directory", http.StatusBadRequest)
		return
	}
	var body struct {
		Content string `json:"content"`
	}
	_ = json.NewDecoder(req.Body).Decode(&body)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := executor.Get().WriteFile(ctx, full, body.Content); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	patches.Save(repo, role, rel, body.Content) // persist patch (survives sb update)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "path": rel})
}

func rolePatches(w http.ResponseWriter, req *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"patches": patches.List(repoParam(req), chi.URLParam(req, "role")),
	})
}

func rolePatch(w http.ResponseWriter, req *http.Request) {
	content, ok := patches.ReadPatch(repoParam(req), chi.URLParam(req, "role"), req.URL.Query().Get("path"))
	if !ok {
		writeJSON(w, http.StatusOK, map[string]any{"path": req.URL.Query().Get("path"), "patch": nil})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"path": req.URL.Query().Get("path"), "patch": content})
}

func rolePatchRebuild(w http.ResponseWriter, req *http.Request) {
	role, repo := chi.URLParam(req, "role"), repoParam(req)
	base := roleBase(role, repo)
	rebuilt := []string{}
	failed := []map[string]string{}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	for _, rel := range patches.List(repo, role) {
		full, ok := safeJoin(base, rel)
		if !ok {
			continue
		}
		content, err := executor.Get().ReadFile(ctx, full)
		if err != nil {
			failed = append(failed, map[string]string{"file": rel, "error": err.Error()})
			continue
		}
		patches.Save(repo, role, rel, content)
		rebuilt = append(rebuilt, rel)
	}
	writeJSON(w, http.StatusOK, map[string]any{"rebuilt": rebuilt, "failed": failed})
}

func rolePatchPreview(w http.ResponseWriter, req *http.Request) {
	role, repo := chi.URLParam(req, "role"), repoParam(req)
	base := roleBase(role, repo)
	items := []map[string]any{}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	for _, rel := range patches.List(repo, role) {
		full, ok := safeJoin(base, rel)
		if !ok {
			continue
		}
		content, err := executor.Get().ReadFile(ctx, full)
		if err != nil {
			items = append(items, map[string]any{"file": rel, "error": err.Error()})
			continue
		}
		prev := patches.Preview(repo, role, rel, content)
		prev["file"] = rel
		items = append(items, prev)
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}
