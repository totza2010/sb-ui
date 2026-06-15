package api

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"sb-ui/internal/ansible"
	"sb-ui/internal/apps"
	"sb-ui/internal/categories"
	"sb-ui/internal/docker"
	"sb-ui/internal/executor"
	"sb-ui/internal/jobs"
	"sb-ui/internal/sysinfo"
)

func listApps(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, apps.List())
}

func systemInfo(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, sysinfo.Get())
}

func listContainers(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, docker.ListContainers())
}

func saltboxVersion(w http.ResponseWriter, _ *http.Request) {
	out := apps.SaltboxVersion()
	for k, v := range apps.UpdateAvailable() {
		out[k] = v
	}
	writeJSON(w, http.StatusOK, out)
}

func updateStatus(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, docker.CachedUpdates())
}

func updateMeta(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, docker.UpdatesMeta())
}

func listCategories(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"order": categories.Order, "labels": categories.Labels})
}

func checkUpdates(w http.ResponseWriter, _ *http.Request) {
	j := jobs.Create("__update_check__", "check-updates")
	go func() {
		jobs.SetStatus(j.ID, "running")
		imgSet := map[string]bool{}
		for _, img := range docker.ContainerImages() {
			imgSet[img] = true
		}
		images := make([]string, 0, len(imgSet))
		for img := range imgSet {
			images = append(images, img)
		}
		jobs.PushLog(j.ID, "Checking image updates…")
		res := docker.CheckAllUpdates(images)
		out, cur, unk := 0, 0, 0
		for img, v := range res {
			switch {
			case v == nil:
				unk++
				jobs.PushLog(j.ID, "[unknown]  "+img)
			case *v:
				out++
				jobs.PushLog(j.ID, "[OUTDATED] "+img)
			default:
				cur++
				jobs.PushLog(j.ID, "[current]  "+img)
			}
		}
		jobs.PushLog(j.ID, "\nDone.")
		jobs.SetStatus(j.ID, "completed")
	}()
	writeJSON(w, http.StatusOK, map[string]any{"job_id": j.ID})
}

func containerAction(w http.ResponseWriter, req *http.Request) {
	name, action := chi.URLParam(req, "name"), chi.URLParam(req, "action")
	if action != "start" && action != "stop" && action != "restart" {
		http.Error(w, "Unknown action", http.StatusBadRequest)
		return
	}
	if err := docker.ContainerAction(name, action); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func serviceAction(w http.ResponseWriter, req *http.Request) {
	name, action := chi.URLParam(req, "name"), chi.URLParam(req, "action")
	if action != "start" && action != "stop" && action != "restart" {
		http.Error(w, "Unknown action", http.StatusBadRequest)
		return
	}
	if err := docker.ServiceAction(name, action); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// imageInfo: docker image inspect + cached update status.
func imageInfo(w http.ResponseWriter, req *http.Request) {
	image := req.URL.Query().Get("image")
	info := apps.ImageInfo(context.Background(), image)
	writeJSON(w, http.StatusOK, info)
}

func pullApp(w http.ResponseWriter, req *http.Request) {
	tag := chi.URLParam(req, "tag")
	j := jobs.Create(tag, "pull")
	go func() {
		jobs.SetStatus(j.ID, "running")
		name := strings.TrimPrefix(strings.TrimPrefix(tag, "sandbox-"), "mod-")
		image := docker.ContainerImages()[name]
		if image == "" {
			jobs.PushLog(j.ID, "Container "+name+" not found or not running.")
			jobs.SetStatus(j.ID, "failed")
			return
		}
		jobs.PushLog(j.ID, "Pulling latest image: "+image)
		s, err := executor.Get().RunStream(context.Background(), []string{"docker", "pull", image}, "", false)
		if err != nil {
			jobs.PushLog(j.ID, "ERROR: "+err.Error())
			jobs.SetStatus(j.ID, "failed")
			return
		}
		for line := range s.Lines {
			jobs.PushLog(j.ID, line)
		}
		jobs.PushLog(j.ID, "\nImage pulled — reinstalling…\n")
		ansible.RunPlaybook(context.Background(), j.ID, tag) // sets final status
	}()
	writeJSON(w, http.StatusOK, map[string]any{"job_id": j.ID})
}

func removeApp(w http.ResponseWriter, req *http.Request) {
	tag := chi.URLParam(req, "tag")
	purge := req.URL.Query().Get("purge") == "true"
	j := jobs.Create(tag, "remove")
	go apps.RunRemove(j.ID, tag, purge)
	writeJSON(w, http.StatusOK, map[string]any{"job_id": j.ID})
}

func appLogs(w http.ResponseWriter, req *http.Request) {
	name := chi.URLParam(req, "tag")
	if strings.ContainsAny(name, "/.") {
		http.Error(w, "Invalid name", http.StatusBadRequest)
		return
	}
	lines, _ := strconv.Atoi(req.URL.Query().Get("lines"))
	if lines <= 0 {
		lines = 200
	}
	if lines > 2000 {
		lines = 2000
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_, out, _ := executor.Get().Run(ctx, []string{
		"docker", "logs", "--tail", strconv.Itoa(lines), "--timestamps", name,
	}, "")
	writeJSON(w, http.StatusOK, map[string]any{"name": name, "logs": out})
}

func appOpt(w http.ResponseWriter, req *http.Request) {
	name := chi.URLParam(req, "tag")
	if strings.ContainsAny(name, "/.") || name == "" {
		http.Error(w, "Invalid name", http.StatusBadRequest)
		return
	}
	root := "/opt/" + name
	target := root
	if rel := req.URL.Query().Get("path"); rel != "" {
		t, ok := safeJoin(root, rel)
		if !ok {
			http.Error(w, "Path outside app folder", http.StatusBadRequest)
			return
		}
		target = t
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	rc, out, _ := executor.Get().Run(ctx, []string{
		"find", target, "-maxdepth", "1", "-mindepth", "1", "-printf", `%y\t%s\t%f\n`,
	}, "")
	entries := []fsEntry{}
	if rc == 0 {
		for _, line := range strings.Split(out, "\n") {
			p := strings.SplitN(line, "\t", 3)
			if len(p) != 3 {
				continue
			}
			typ := "file"
			if p[0] == "d" {
				typ = "dir"
			}
			sz, _ := strconv.ParseInt(p[1], 10, 64)
			entries = append(entries, fsEntry{Type: typ, Size: sz, Name: p[2]})
		}
	}
	sortEntries(entries)
	writeJSON(w, http.StatusOK, map[string]any{
		"name": name, "path": req.URL.Query().Get("path"), "base": root,
		"entries": entries, "exists": rc == 0,
	})
}
