package api

import (
	"context"
	"fmt"
	"strings"
	"time"

	"sb-ui/internal/executor"
	"sb-ui/internal/inventory"
)

// autoscan (the saltbox role) has NO HTTP pause/resume API — its HTTP server only
// exposes trigger webhooks (/triggers/*). So to make it "wait" during an upload we
// freeze the container with `docker pause` and thaw it with `docker unpause` after;
// triggers that arrive meanwhile queue in its datastore and process on resume.

func autoscanContainer() string {
	for _, ap := range inventory.ResolveAppdata("autoscan") {
		if ap.Instance != "" {
			return ap.Instance
		}
	}
	return ""
}

// autoscanHold pauses (hold=true) or unpauses (hold=false) the autoscan container so
// it doesn't scan the media root while it's being moved.
func autoscanHold(hold bool) error {
	name := autoscanContainer()
	if name == "" {
		return fmt.Errorf("autoscan container not found")
	}
	action := "unpause"
	if hold {
		action = "pause"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	rc, out, err := executor.Get().Run(ctx, []string{"docker", action, name}, "")
	if err != nil {
		return err
	}
	if rc != 0 {
		return fmt.Errorf("docker %s %s: %s", action, name, strings.TrimSpace(out))
	}
	return nil
}

// autoscanStatus reports whether the container is currently paused (for the self-test).
func autoscanStatus() string {
	name := autoscanContainer()
	if name == "" {
		return "container not found"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	rc, out, err := executor.Get().Run(ctx, []string{"docker", "inspect", "-f", "{{.State.Status}}", name}, "")
	if err != nil || rc != 0 {
		return name + ": unknown"
	}
	return name + ": " + strings.TrimSpace(out)
}
