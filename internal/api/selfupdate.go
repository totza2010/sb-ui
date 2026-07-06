package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"sb-ui/internal/jobs"
	"sb-ui/internal/selfupdate"
	"sb-ui/internal/store"
)

const updateChannelRel = "cache/update_channel"

// updateChannel is the persisted self-update channel ("stable" | "nightly").
func updateChannel() string {
	if c, ok := store.ReadText(updateChannelRel); ok && strings.TrimSpace(c) == "nightly" {
		return "nightly"
	}
	return "stable"
}

// selfVersion reports the running version vs the newest release on the current channel.
func selfVersion(w http.ResponseWriter, _ *http.Request) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	writeJSON(w, http.StatusOK, selfupdate.Check(ctx, updateChannel()))
}

// selfSetChannel switches between the stable and nightly (master) update channels.
func selfSetChannel(w http.ResponseWriter, req *http.Request) {
	var b struct {
		Channel string `json:"channel"`
	}
	_ = json.NewDecoder(req.Body).Decode(&b)
	ch := "stable"
	if strings.TrimSpace(b.Channel) == "nightly" {
		ch = "nightly"
	}
	store.WriteText(updateChannelRel, ch)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "channel": ch})
}

// selfUpdate downloads the newest release on the current channel and re-execs (job).
func selfUpdate(w http.ResponseWriter, _ *http.Request) {
	j := jobs.Create("sb-ui", "self-update")
	go selfupdate.Run(j.ID, updateChannel())
	writeJSON(w, http.StatusOK, map[string]any{"job_id": j.ID})
}
