// Package installtypes customises Saltbox profiles (saltbox/mediabox/feederbox)
// and the dynamic *_enabled lists, persisted as inventory overrides.
package installtypes

import (
	"context"
	"regexp"
	"sort"
	"strings"
	"time"

	"sb-ui/internal/config"
	"sb-ui/internal/executor"
	"sb-ui/internal/inventory"
)

var profileKeys = map[string]string{
	"saltbox": "saltbox_roles", "mediabox": "mediabox_roles", "feederbox": "feederbox_roles",
}
var enabledKeys = []string{"media_servers_enabled", "download_clients_enabled", "download_indexers_enabled"}

var fallbackDefaults = map[string][]string{
	"saltbox_roles":  {"media_server", "download_clients", "download_indexers", "autoscan", "tautulli", "seerr", "portainer", "organizr", "sonarr", "radarr", "lidarr", "iperf3", "glances", "btop"},
	"mediabox_roles": {"media_server", "autoscan", "tautulli", "seerr", "iperf3", "glances", "btop"},
	"feederbox_roles": {"download_clients", "download_indexers", "portainer", "organizr", "sonarr", "radarr", "lidarr", "iperf3", "glances", "btop"},
	"media_servers_enabled":    {"plex"},
	"download_clients_enabled":  {"qbittorrent", "sabnzbd"},
	"download_indexers_enabled": {"jackett", "nzbhydra2"},
}

var enabledOptions = map[string][]string{
	"media_servers_enabled":     {"plex", "emby", "jellyfin"},
	"download_clients_enabled":  {"qbittorrent", "sabnzbd", "nzbget", "deluge"},
	"download_indexers_enabled": {"jackett", "nzbhydra2", "prowlarr"},
}

var listRE = regexp.MustCompile(
	`(?m)^(saltbox_roles|mediabox_roles|feederbox_roles|media_servers_enabled|download_clients_enabled|download_indexers_enabled)\s*:\s*\[([^\]]*)\]`)

func repoDefaults() map[string][]string {
	c := config.Get()
	files := []string{
		c.SaltboxRepo + "/roles/main_tags/defaults/main.yml",
		c.SaltboxRepo + "/roles/media_server/defaults/main.yml",
		c.SaltboxRepo + "/roles/download_clients/defaults/main.yml",
		c.SaltboxRepo + "/roles/download_indexers/defaults/main.yml",
	}
	out := map[string][]string{}
	for k, v := range fallbackDefaults {
		out[k] = v
	}
	e := executor.Get()
	var all strings.Builder
	for _, f := range files {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		if s, err := e.ReadFile(ctx, f); err == nil {
			all.WriteString(s + "\n")
		}
		cancel()
	}
	for _, m := range listRE.FindAllStringSubmatch(all.String(), -1) {
		var items []string
		for _, x := range strings.Split(m[2], ",") {
			x = strings.TrimSpace(strings.Trim(strings.TrimSpace(x), `'"`))
			if x != "" {
				items = append(items, x)
			}
		}
		out[m[1]] = items
	}
	return out
}

func toStrings(v any) ([]string, bool) {
	list, ok := v.([]any)
	if !ok {
		return nil, false
	}
	out := make([]string, 0, len(list))
	for _, it := range list {
		out = append(out, strings.TrimSpace(toStr(it)))
	}
	return out, true
}

func toStr(v any) string {
	switch x := v.(type) {
	case string:
		return x
	default:
		return ""
	}
}

// Get returns profile lists + enabled lists with defaults + overrides.
func Get() map[string]any {
	inv := inventory.Read()
	defaults := repoDefaults()

	available := []string{}
	for name, r := range inventory.GetCatalog().Roles {
		if r.Repo == "saltbox" {
			available = append(available, name)
		}
	}
	sort.Strings(available)

	profiles := map[string]any{}
	for name, key := range profileKeys {
		over, has := inv[key]
		roles := defaults[key]
		if has {
			if s, ok := toStrings(over); ok {
				roles = s
			}
		}
		profiles[name] = map[string]any{"key": key, "roles": roles, "default": defaults[key], "overridden": has}
	}

	enabled := map[string]any{}
	for _, key := range enabledKeys {
		over, has := inv[key]
		val := defaults[key]
		if has {
			if s, ok := toStrings(over); ok {
				val = s
			}
		}
		enabled[key] = map[string]any{"value": val, "default": defaults[key], "options": enabledOptions[key], "overridden": has}
	}

	return map[string]any{"profiles": profiles, "enabled": enabled, "available_roles": available}
}

// Save persists overrides; a list equal to its default is removed (keeps inv clean).
func Save(payload map[string]any) error {
	inv := inventory.Read()
	defaults := repoDefaults()

	apply := func(key string, value []string) {
		if equalSlice(value, defaults[key]) {
			delete(inv, key)
		} else {
			anyList := make([]any, len(value))
			for i, v := range value {
				anyList[i] = v
			}
			inv[key] = anyList
		}
	}

	if profs, ok := payload["profiles"].(map[string]any); ok {
		for name, key := range profileKeys {
			if p, ok := profs[name].(map[string]any); ok {
				if roles, ok := toStrings(p["roles"]); ok {
					apply(key, roles)
				}
			}
		}
	}
	if en, ok := payload["enabled"].(map[string]any); ok {
		for _, key := range enabledKeys {
			if e, ok := en[key].(map[string]any); ok {
				if val, ok := toStrings(e["value"]); ok {
					apply(key, val)
				}
			}
		}
	}
	return inventory.Write(inv)
}

func equalSlice(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
