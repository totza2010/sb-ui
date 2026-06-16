// Package apps builds the installed/available app list from cache.json + docker
// + systemd + inventory: classification, instances, companions, categories,
// action-tag filtering and storage/FUSE detection.
package apps

import (
	"context"
	"encoding/json"
	"regexp"
	"strings"
	"time"

	"sb-ui/internal/config"
	"sb-ui/internal/executor"
)

var internalTags = map[string]bool{
	"always": true, "pre-tasks": true, "sanity-check": true, "user-check": true,
	"never": true, "core": true, "saltbox": true, "mediabox": true,
	"feederbox": true, "preinstall": true,
}

var storageRoles = map[string]bool{
	"rclone": true, "remote": true, "unionfs": true, "mounts": true, "mergerfs": true,
}

type Instance struct {
	Name            string  `json:"name"`
	Installed       bool    `json:"installed"`
	Kind            string  `json:"kind"`
	ContainerStatus *string `json:"container_status"`
	Image           *string `json:"image"`
	Unit            *string `json:"unit"`
}

type App struct {
	Tag             string     `json:"tag"`
	Name            string     `json:"name"`
	Repo            string     `json:"repo"`
	Installed       bool       `json:"installed"`
	Kind            string     `json:"kind"`
	ContainerStatus *string    `json:"container_status"`
	Image           *string    `json:"image"`
	Instances       []Instance `json:"instances"`
	Companions      []Instance `json:"companions"`
	Category        string     `json:"category"`
	OnDemand        bool       `json:"on_demand"`
}

func strp(s string) *string { return &s }

// ── cache.json ───────────────────────────────────────────────────────────────

func readCache() (saltbox, sandbox []string) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	e := executor.Get()
	p := config.Get().CacheFile()
	if ok, _ := e.FileExists(ctx, p); !ok {
		return nil, nil
	}
	raw, err := e.ReadFile(ctx, p)
	if err != nil {
		return nil, nil
	}
	var data map[string]struct {
		Tags []string `json:"tags"`
	}
	if json.Unmarshal([]byte(raw), &data) != nil {
		return nil, nil
	}
	for repoPath, rd := range data {
		var tags []string
		for _, t := range rd.Tags {
			if !internalTags[t] {
				tags = append(tags, t)
			}
		}
		if strings.Contains(strings.ToLower(repoPath), "sandbox") {
			sandbox = tags
		} else {
			saltbox = tags
		}
	}
	return saltbox, sandbox
}

// ── action tags (parsed from saltbox.yml) ────────────────────────────────────

var (
	actionCache map[string]bool
	roleTagRE   = regexp.MustCompile(`role:\s*([A-Za-z0-9_]+).*?tags:\s*\[([^\]]*)\]`)
)

func ClearActionCache() { actionCache = nil }

func actionTags() map[string]bool {
	if actionCache != nil {
		return actionCache
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	content, err := executor.Get().ReadFile(ctx, config.Get().SaltboxPlaybook())
	if err != nil {
		return map[string]bool{}
	}
	roleTags := map[string][]string{}
	for _, m := range roleTagRE.FindAllStringSubmatch(content, -1) {
		role := m[1]
		var tags []string
		for _, t := range strings.Split(m[2], ",") {
			t = strings.TrimSpace(strings.Trim(strings.TrimSpace(t), `'"`))
			if t != "" {
				tags = append(tags, t)
			}
		}
		roleTags[role] = append(roleTags[role], tags...)
	}
	primary, secondary := map[string]bool{}, map[string]bool{}
	for role, tags := range roleTags {
		cand := map[string]bool{role: true, strings.ReplaceAll(role, "_", "-"): true}
		var rolePrimary string
		for _, t := range tags {
			if cand[t] {
				rolePrimary = t
				break
			}
		}
		if rolePrimary != "" {
			primary[rolePrimary] = true
		}
		for _, t := range tags {
			if t != rolePrimary {
				secondary[t] = true
			}
		}
	}
	action := map[string]bool{}
	for t := range secondary {
		if !primary[t] {
			action[t] = true
		}
	}
	actionCache = action
	return action
}

// ── installed binaries ───────────────────────────────────────────────────────

func installedBinaries() map[string]bool {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	rc, out, err := executor.Get().Run(ctx, []string{"ls", "/usr/bin/", "/usr/local/bin/"}, "")
	set := map[string]bool{}
	if err != nil || rc != 0 {
		return set
	}
	for _, l := range strings.Split(out, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			set[l] = true
		}
	}
	return set
}
