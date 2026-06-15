package apps

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"sb-ui/internal/docker"
	"sb-ui/internal/executor"
)

// ImageInfo returns docker image details + cached update status for one image.
func ImageInfo(_ context.Context, image string) map[string]any {
	info := map[string]any{"image": image}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	rc, out, err := executor.Get().Run(ctx, []string{"docker", "image", "inspect", image}, "")
	if err == nil && rc == 0 && strings.TrimSpace(out) != "" {
		var arr []struct {
			Created      string   `json:"Created"`
			Size         int64    `json:"Size"`
			Architecture string   `json:"Architecture"`
			Os           string   `json:"Os"`
			ID           string   `json:"Id"`
			RepoDigests  []string `json:"RepoDigests"`
			RepoTags     []string `json:"RepoTags"`
		}
		if json.Unmarshal([]byte(out), &arr) == nil && len(arr) > 0 {
			d := arr[0]
			info["created"] = d.Created
			info["size"] = d.Size
			info["architecture"] = d.Architecture
			info["os"] = d.Os
			info["id"] = d.ID
			info["tags"] = d.RepoTags
			if len(d.RepoDigests) > 0 {
				parts := strings.SplitN(d.RepoDigests[0], "@", 2)
				info["digest"] = parts[len(parts)-1]
			} else {
				info["digest"] = nil
			}
		}
	}
	info["outdated"] = docker.CachedUpdates()[image]
	meta := docker.UpdatesMeta()
	if ts, ok := meta["ts"].(map[string]float64); ok {
		if v, ok := ts[image]; ok {
			info["checked_at"] = v
		} else {
			info["checked_at"] = nil
		}
	}
	return info
}
