package api

import (
	"context"
	"net/http"
	"time"

	"sb-ui/internal/jobs"
	"sb-ui/internal/selfupdate"
)

// selfVersion reports the running version vs the latest GitHub release.
func selfVersion(w http.ResponseWriter, _ *http.Request) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	writeJSON(w, http.StatusOK, selfupdate.Check(ctx))
}

// selfUpdate downloads the latest release and re-execs into it (streamed job).
func selfUpdate(w http.ResponseWriter, _ *http.Request) {
	j := jobs.Create("sb-ui", "self-update")
	go selfupdate.Run(j.ID)
	writeJSON(w, http.StatusOK, map[string]any{"job_id": j.ID})
}
