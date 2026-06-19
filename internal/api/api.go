// Package api wires the HTTP + WebSocket routes that serve the React frontend.
package api

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"

	"sb-ui/internal/ansible"
	"sb-ui/internal/buildinfo"
	"sb-ui/internal/jobs"
)

// Mount registers the API + WS routes on r. Static frontend / SPA fallback is
// handled by the caller's NotFound handler.
func Mount(r chi.Router) {
	r.Get("/api/health", health)

	// Setup wizard / connection
	r.Get("/api/setup/status", setupStatus)
	r.Post("/api/setup/test", setupTest)
	r.Post("/api/setup/save", setupSave)

	// Self-update (sb-ui's own binary)
	r.Get("/api/self/version", selfVersion)
	r.Post("/api/self/update", selfUpdate)

	r.Get("/api/jobs", listJobs)
	r.Post("/api/jobs/clear", clearJobs)
	r.Get("/api/jobs/{id}", getJob)
	r.Delete("/api/jobs/{id}", deleteJob)

	// System dashboard
	r.Get("/api/system", systemInfo)
	r.Get("/api/containers", listContainers)
	r.Get("/api/containers/stats", containerStats)
	r.Get("/api/containers/{name}/inspect", containerInspect)
	r.Get("/api/status", systemStatus)
	r.Get("/api/mounts", listMounts)
	r.Get("/api/storage", storageInfo)

	// Apps + status
	r.Get("/api/apps", listApps)
	r.Get("/api/apps/saltbox-version", saltboxVersion)
	r.Get("/api/apps/update-status", updateStatus)
	r.Get("/api/apps/update-meta", updateMeta)
	r.Get("/api/apps/{tag}/image-info", imageInfo)
	r.Get("/api/apps/{tag}/logs", appLogs)
	r.Get("/api/apps/{tag}/opt", appOpt)
	r.Get("/api/categories", listCategories)
	r.Post("/api/apps/check-updates", checkUpdates)

	// App actions
	r.Post("/api/apps/install-set", installSet)
	r.Post("/api/apps/{tag}/install", installApp("install"))
	r.Post("/api/apps/{tag}/reinstall", installApp("reinstall"))
	r.Post("/api/apps/{tag}/pull", pullApp)
	r.Post("/api/apps/{tag}/remove", removeApp)

	// Container / service lifecycle
	r.Post("/api/containers/{name}/{action}", containerAction)
	r.Post("/api/services/{name}/{action}", serviceAction)

	// Config files
	r.Get("/api/config/{filename}", getConfig)
	r.Put("/api/config/{filename}", putConfig)
	r.Post("/api/config/{filename}/apply", applyConfig)

	// Inventory + appdata
	r.Get("/api/inventory", getInventory)
	r.Put("/api/inventory", putInventory)
	r.Get("/api/inventory/catalog", getCatalog)
	r.Get("/api/apps/{tag}/appdata", getAppdata)

	// rclone
	r.Get("/api/rclone/remotes", rcloneRemotes)
	r.Get("/api/rclone/ls", rcloneLs)
	r.Post("/api/rclone/mkdir", rcloneMkdir)
	r.Get("/api/rclone/fsinfo", rcloneFsinfo)
	r.Get("/api/rclone/size", rcloneSize)
	r.Get("/api/rclone/about", rcloneAbout)
	r.Post("/api/rclone/delete", rcloneDelete)
	r.Post("/api/rclone/moveto", rcloneMoveto)
	r.Post("/api/rclone/copyto", rcloneCopyto)
	r.Get("/api/rclone/categories", rcloneCategories)
	r.Post("/api/rclone/cleanup", rcloneCleanup)
	r.Post("/api/rclone/dedupe", rcloneDedupe)
	r.Post("/api/rclone/link", rcloneLink)

	// Central options + Plex
	r.Get("/api/options", getOptions)
	r.Put("/api/options", putOptions)
	r.Get("/api/plex/test", plexTest)

	// teldrive (tgdrive) panel — only meaningful when teldrive remotes exist
	r.Get("/api/teldrive/remotes", teldriveRemotesHandler)
	r.Get("/api/teldrive/search", teldriveSearch)
	r.Get("/api/teldrive/storage", teldriveStorage)
	r.Get("/api/rclone/providers", rcloneProviders)
	r.Post("/api/rclone/transfer", rcloneTransfer)
	r.Get("/api/transfers/{id}/stats", transferStatsHandler)
	r.Get("/api/transfers/{id}/telemetry", transferTelemetry)
	r.Delete("/api/transfers/{id}/telemetry", deleteTelemetry)
	r.Post("/api/telemetry/purge", purgeTelemetry)
	r.Post("/api/transfers/{id}/stop", stopTransfer)

	// Transfer tasks (save / run / queue) + scheduler
	r.Get("/api/tasks", listTasks)
	r.Post("/api/tasks", createTask)
	r.Put("/api/tasks/{id}", updateTask)
	r.Delete("/api/tasks/{id}", deleteTask)
	r.Post("/api/tasks/{id}/run", runTaskNow)
	r.Post("/api/tasks/{id}/queue", queueTaskNow)
	r.Post("/api/tasks/{id}/toggle", toggleTask)

	// Transfer queue manager
	r.Get("/api/queue", queueState)
	r.Post("/api/queue/start", queueStart)
	r.Post("/api/queue/stop", queueStop)
	r.Post("/api/queue/purge", queuePurge)
	r.Post("/api/queue/{id}/remove", queueRemove)
	r.Post("/api/queue/{id}/up", queueMove(-1))
	r.Post("/api/queue/{id}/down", queueMove(1))

	startScheduler()

	// Smart Uploader (cloudplow++)
	r.Get("/api/uploader", getUploader)
	r.Put("/api/uploader", putUploader)
	r.Get("/api/uploader/status", uploaderStatus)
	r.Post("/api/uploader/run", uploaderRun)
	r.Post("/api/uploader/simulate", uploaderSimulate)
	r.Get("/api/uploader/calibration", uploaderCalibration)
	startUploader()
	r.Get("/api/rclone/status", rcloneStatus)
	r.Get("/api/rclone/logs", rcloneLogs)
	r.Get("/api/rclone/mount-templates", mountTemplates)

	// Filesystem browse + edit (/fs/read|write are aliases of /fs/file)
	r.Get("/api/fs", fsList)
	r.Get("/api/fs/file", fsReadFile)
	r.Put("/api/fs/file", fsWriteFile)
	r.Get("/api/fs/read", fsReadFile)
	r.Put("/api/fs/write", fsWriteFile)
	r.Get("/api/fs/du", fsDu)
	r.Post("/api/fs/mkdir", fsMkdir)
	r.Post("/api/fs/rename", fsRename)
	r.Post("/api/fs/delete", fsDelete)
	r.Post("/api/fs/move", fsTransfer(true))
	r.Post("/api/fs/copy", fsTransfer(false))
	r.Post("/api/fs/upload", fsUpload)
	r.Get("/api/fs/download", fsDownload)

	// Bundles / install types / custom sets
	r.Get("/api/bundles", listBundles)
	r.Get("/api/install-types", getInstallTypes)
	r.Put("/api/install-types", putInstallTypes)
	r.Get("/api/custom-sets", getCustomSets)
	r.Put("/api/custom-sets", putCustomSet)
	r.Delete("/api/custom-sets/{id}", deleteCustomSet)

	// Saltbox update + patches
	r.Post("/api/apps/saltbox-update", saltboxUpdate)
	r.Post("/api/apps/apply-patches", applyPatches)

	// Role Builder
	r.Get("/api/roles/mod", listModRoles)
	r.Post("/api/roles/preview", rolePreview)
	r.Post("/api/roles/commit", roleCommit)
	r.Delete("/api/roles/{role}", removeModRole)

	// Role file editor + patches
	r.Get("/api/roles/{role}/files", roleFiles)
	r.Get("/api/roles/{role}/file", roleReadFile)
	r.Put("/api/roles/{role}/file", roleWriteFile)
	r.Get("/api/roles/{role}/patches", rolePatches)
	r.Get("/api/roles/{role}/patch", rolePatch)
	r.Post("/api/roles/{role}/patches/rebuild", rolePatchRebuild)
	r.Get("/api/roles/{role}/patches/rebuild-preview", rolePatchPreview)

	// WebSocket log stream
	r.Get("/ws/jobs/{id}", jobWS)
}

func health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "backend": "go", "version": buildinfo.Version})
}

func listJobs(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, jobs.ListDicts())
}

func getJob(w http.ResponseWriter, req *http.Request) {
	d, ok := jobs.JobDict(chi.URLParam(req, "id"))
	if !ok {
		http.Error(w, "Job not found", http.StatusNotFound)
		return
	}
	writeJSON(w, http.StatusOK, d)
}

func deleteJob(w http.ResponseWriter, req *http.Request) {
	id := chi.URLParam(req, "id")
	if !jobs.Delete(id) {
		http.Error(w, "Job not found or still running", http.StatusConflict)
		return
	}
	removeTelemetry(id)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func clearJobs(w http.ResponseWriter, _ *http.Request) {
	ids := jobs.ClearFinished()
	for _, id := range ids {
		removeTelemetry(id)
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "removed": len(ids)})
}

func installApp(action string) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		tag := chi.URLParam(req, "tag")
		j := jobs.Create(tag, action)
		go ansible.RunPlaybook(context.Background(), j.ID, tag)
		writeJSON(w, http.StatusOK, map[string]any{"job_id": j.ID})
	}
}

func installSet(w http.ResponseWriter, req *http.Request) {
	var body struct {
		Tags []string `json:"tags"`
	}
	_ = json.NewDecoder(req.Body).Decode(&body)
	if len(body.Tags) == 0 {
		http.Error(w, "No tags provided", http.StatusBadRequest)
		return
	}
	j := jobs.Create(joinTags(body.Tags), "install-set")
	go ansible.RunMulti(context.Background(), j.ID, body.Tags)
	writeJSON(w, http.StatusOK, map[string]any{"job_id": j.ID})
}

func joinTags(tags []string) string {
	out := ""
	for i, t := range tags {
		if i > 0 {
			out += ","
		}
		out += t
	}
	return out
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
