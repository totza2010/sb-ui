package apps

import (
	"context"
	"strconv"
	"strings"
	"time"

	"sb-ui/internal/config"
	"sb-ui/internal/executor"
)

func git(args ...string) (int, string) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	repo := config.Get().SaltboxRepo
	cmd := append([]string{"git", "-C", repo}, args...)
	rc, out, err := executor.Get().Run(ctx, cmd, "")
	if err != nil {
		return -1, ""
	}
	return rc, out
}

// SaltboxVersion returns current commit info for the Saltbox repo.
func SaltboxVersion() map[string]any {
	rc, sha := git("rev-parse", "--short", "HEAD")
	_, date := git("log", "-1", "--format=%ci")
	_, tag := git("describe", "--tags", "--always")
	out := map[string]any{"sha": "unknown", "date": nil, "tag": nil}
	if rc == 0 {
		out["sha"] = strings.TrimSpace(sha)
	}
	if d := strings.TrimSpace(date); d != "" {
		out["date"] = d
	}
	if t := strings.TrimSpace(tag); t != "" {
		out["tag"] = t
	}
	return out
}

// UpdateAvailable returns how many commits behind origin + recent commit lines.
func UpdateAvailable() map[string]any {
	git("fetch", "--quiet")
	rc, count := git("rev-list", "HEAD..origin/HEAD", "--count")
	behind := 0
	if rc == 0 {
		if n, err := strconv.Atoi(strings.TrimSpace(count)); err == nil {
			behind = n
		}
	}
	_, logRaw := git("log", "HEAD..origin/HEAD", "--oneline", "--max-count=10")
	var commits []string
	for _, l := range strings.Split(logRaw, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			commits = append(commits, l)
		}
	}
	if commits == nil {
		commits = []string{}
	}
	return map[string]any{"behind": behind, "commits": commits}
}
