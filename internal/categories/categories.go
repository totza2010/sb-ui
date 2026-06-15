// Package categories groups apps for the dashboard. Curated role→category map
// (port of categories.py). `utility` apps are on-demand tools, not 24/7 services.
package categories

import "strings"

var Order = []string{
	"core", "media", "downloads", "indexers", "automation",
	"database", "monitoring", "utility", "other",
}

var Labels = map[string]string{
	"core":       "Core & system",
	"media":      "Media servers",
	"downloads":  "Download clients",
	"indexers":   "Indexers",
	"automation": "Automation & post-processing",
	"database":   "Databases",
	"monitoring": "Monitoring",
	"utility":    "Utilities (on-demand)",
	"other":      "Other",
}

var onDemand = map[string]bool{"utility": true}

var roleCategory = map[string]string{}

func add(cat string, roles ...string) {
	for _, r := range roles {
		roleCategory[r] = cat
	}
}

func init() {
	add("core",
		"kernel", "hetzner", "user", "shell", "rclone", "mount_templates", "system",
		"common", "motd", "remote", "unionfs", "nvidia", "docker", "scripts", "sandbox",
		"crowdsec", "traefik", "cloudflare", "authelia", "authentik", "lldap", "gluetun",
		"nginx", "ddclient", "ddns", "docker_socket_proxy", "portainer", "organizr",
		"python", "error_pages")
	add("media",
		"plex", "emby", "jellyfin", "tautulli", "overseerr", "petio", "seerr",
		"jellyseerr", "ombi", "plex_db", "plex_meta_manager", "kavita", "calibre",
		"audiobookshelf", "navidrome")
	add("downloads",
		"sabnzbd", "nzbget", "qbittorrent", "deluge", "rtorrent", "transmission",
		"transfer", "nzbthrottle", "aria2")
	add("indexers", "jackett", "prowlarr", "nzbhydra2")
	add("automation",
		"sonarr", "radarr", "lidarr", "readarr", "bazarr", "whisparr", "mylar",
		"autobrr", "autoscan", "unpackerr", "subliminal", "autoplow", "cloudplow",
		"recyclarr", "cleanuparr", "cross_seed", "arr_db", "asshama")
	add("database", "postgres", "mariadb", "mongodb", "redis", "mysql")
	add("monitoring",
		"grafana", "prometheus", "netdata", "cadvisor", "node_exporter", "scrutiny",
		"dozzle", "diun", "autoheal", "uptime_kuma")
	add("utility",
		"btop", "ctop", "glances", "iperf3", "yyq", "apprise", "speedtest", "nethogs",
		"mainline", "btrfsmaintenance", "diag", "custom")
}

func Categorize(tag string) string {
	key := strings.ReplaceAll(tag, "-", "_")
	if c, ok := roleCategory[key]; ok {
		return c
	}
	return "other"
}

func IsOnDemand(cat string) bool { return onDemand[cat] }
