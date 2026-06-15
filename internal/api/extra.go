package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"sb-ui/internal/apps"
	"sb-ui/internal/bundles"
	"sb-ui/internal/customsets"
	"sb-ui/internal/executor"
	"sb-ui/internal/installtypes"
	"sb-ui/internal/inventory"
	"sb-ui/internal/jobs"
	"sb-ui/internal/patches"
)

// ── bundles ──────────────────────────────────────────────────────────────────

func listBundles(w http.ResponseWriter, req *http.Request) {
	refresh := req.URL.Query().Get("refresh") == "1" || req.URL.Query().Get("refresh") == "true"
	writeJSON(w, http.StatusOK, bundles.GetBundles(refresh))
}

// ── install types ────────────────────────────────────────────────────────────

func getInstallTypes(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, installtypes.Get())
}

func putInstallTypes(w http.ResponseWriter, req *http.Request) {
	var body map[string]any
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := installtypes.Save(body); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// ── custom sets ──────────────────────────────────────────────────────────────

func getCustomSets(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, customsets.GetAll())
}

func putCustomSet(w http.ResponseWriter, req *http.Request) {
	var body struct {
		ID   string   `json:"id"`
		Name string   `json:"name"`
		Tags []string `json:"tags"`
	}
	_ = json.NewDecoder(req.Body).Decode(&body)
	rec, err := customsets.Upsert(body.Name, body.Tags, body.ID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, http.StatusOK, rec)
}

func deleteCustomSet(w http.ResponseWriter, req *http.Request) {
	customsets.Delete(chi.URLParam(req, "id"))
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// ── saltbox update + apply patches ───────────────────────────────────────────

func saltboxUpdate(w http.ResponseWriter, _ *http.Request) {
	j := jobs.Create("saltbox", "update")
	go func() {
		jobs.SetStatus(j.ID, "running")
		jobs.PushLog(j.ID, "=== sb update --reset-branch ===")
		s, err := executor.Get().RunStream(context.Background(),
			[]string{"sb", "update", "--reset-branch"}, "", true)
		if err != nil {
			jobs.PushLog(j.ID, "ERROR: "+err.Error())
			jobs.SetStatus(j.ID, "failed")
			return
		}
		for line := range s.Lines {
			jobs.PushLog(j.ID, line)
		}
		if s.Exit() != 0 {
			jobs.PushLog(j.ID, "\nsb update exited with code "+strconv.Itoa(s.Exit()))
			jobs.SetStatus(j.ID, "failed")
			return
		}
		bundles.ClearCache()
		apps.ClearActionCache()
		inventory.InvalidateCatalog()
		reapply(j.ID)
		jobs.SetStatus(j.ID, "completed")
	}()
	writeJSON(w, http.StatusOK, map[string]any{"job_id": j.ID})
}

func applyPatches(w http.ResponseWriter, _ *http.Request) {
	j := jobs.Create("saltbox", "apply-patches")
	go func() {
		jobs.SetStatus(j.ID, "running")
		jobs.PushLog(j.ID, "=== Applying role file patches ===")
		res := reapply(j.ID)
		bad := 0
		for _, r := range res {
			if r.Status == "conflict" || r.Status == "error" {
				bad++
			}
		}
		if bad > 0 {
			jobs.SetStatus(j.ID, "failed")
		} else {
			jobs.SetStatus(j.ID, "completed")
		}
	}()
	writeJSON(w, http.StatusOK, map[string]any{"job_id": j.ID})
}

func reapply(jobID string) []patches.Result {
	res := patches.Restore("saltbox")
	if len(res) == 0 {
		jobs.PushLog(jobID, "No patches to apply.")
		return res
	}
	for _, r := range res {
		jobs.PushLog(jobID, "  ["+strings.ToUpper(r.Status)+"] "+r.Role+"/"+r.File)
		if r.Output != "" {
			for _, l := range strings.Split(r.Output, "\n") {
				jobs.PushLog(jobID, "    "+l)
			}
		}
	}
	return res
}
