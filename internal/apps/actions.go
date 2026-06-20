package apps

import (
	"context"
	"strings"
	"time"

	"sb-ui/internal/docker"
	"sb-ui/internal/executor"
	"sb-ui/internal/jobs"
)

// AppImages returns the unique container images backing an app (multi-instance
// apps share one image; companions may differ).
func AppImages(tag string) []string {
	seen := map[string]bool{}
	var out []string
	for _, a := range List() {
		if a.Tag != tag {
			continue
		}
		for _, c := range append(append([]Instance{}, a.Instances...), a.Companions...) {
			if c.Image != nil && *c.Image != "" && !seen[*c.Image] {
				seen[*c.Image] = true
				out = append(out, *c.Image)
			}
		}
	}
	return out
}

// RunRemove stops + removes every container an app owns (instances + companions)
// and, when purge is set, deletes their /opt appdata. Docker apps only.
func RunRemove(jobID, tag string, purge bool) {
	jobs.SetStatus(jobID, "running")
	var app *App
	for _, a := range List() {
		if a.Tag == tag {
			app = &a
			break
		}
	}
	if app == nil {
		jobs.PushLog(jobID, tag+" not found.")
		jobs.SetStatus(jobID, "failed")
		return
	}

	seen := map[string]bool{}
	var names []string
	for _, c := range append(append([]Instance{}, app.Instances...), app.Companions...) {
		if c.Kind == "container" && !seen[c.Name] {
			seen[c.Name] = true
			names = append(names, c.Name)
		}
	}
	if len(names) == 0 {
		jobs.PushLog(jobID, "No containers to remove (service/binary app).")
		jobs.SetStatus(jobID, "completed")
		return
	}

	for _, n := range names {
		jobs.PushLog(jobID, "Stopping "+n+"…")
		_ = docker.ContainerAction(n, "stop")
		jobs.PushLog(jobID, "Removing container "+n+"…")
		if err := docker.ContainerAction(n, "rm"); err != nil {
			jobs.PushLog(jobID, "  rm failed: "+err.Error())
		}
	}
	if purge {
		ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
		defer cancel()
		for _, n := range names {
			if strings.ContainsAny(n, "/.") || n == "" {
				continue
			}
			path := "/opt/" + n
			jobs.PushLog(jobID, "Deleting appdata "+path+"…")
			_, _, _ = executor.Get().Run(ctx, []string{"rm", "-rf", path}, "")
		}
	}
	jobs.PushLog(jobID, "\nDone.")
	jobs.SetStatus(jobID, "completed")
}
