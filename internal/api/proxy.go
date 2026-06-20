package api

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"sb-ui/internal/executor"
	"sb-ui/internal/store"
)

// tsdproxy: a second reverse proxy beside Traefik that exposes services on the
// Tailscale network (auto HTTPS + MagicDNS, no public exposure). Installed as a
// standalone systemd SERVICE on the host so it survives Docker restarts. sb-ui
// keeps the list of host-service proxies as its own JSON source of truth and
// regenerates tsdproxy's watched list file (no yaml dependency, no restart — the
// list provider watches the file).

const (
	tsdBin     = "/usr/local/bin/tsdproxy"
	tsdCfgDir  = "/etc/tsdproxy"
	tsdCfg     = "/etc/tsdproxy/tsdproxy.yaml"
	tsdList    = "/etc/tsdproxy/proxies.yaml"
	tsdData    = "/var/lib/tsdproxy"
	tsdUnit    = "/etc/systemd/system/tsdproxy.service"
	proxyLists    = "cache/proxy_lists.json" // sb-ui source of truth (other host services)
	proxySelf     = "cache/proxy_self.json"  // sb-ui's own self-expose config
	proxyDash     = "cache/proxy_dash.json"  // tsdproxy's built-in dashboard expose config
	proxyOptsFile = "cache/proxy_opts.json"  // advanced tsdproxy.yaml options
)

// proxyOpts mirrors the editable server-level tsdproxy.yaml settings (v2.3.3).
// Auth (clientId/secret/authKey/tags) is handled separately in the Auth tab.
type proxyOpts struct {
	// Server
	LogLevel       string `json:"log_level"`       // log.level
	LogJSON        bool   `json:"log_json"`        // log.json
	DashPort       int    `json:"dash_port"`       // http.port
	AccessLog      bool   `json:"access_log"`      // proxyAccessLog
	AdminLocalhost bool   `json:"admin_localhost"` // adminAllowLocalhost
	// Tailscale provider
	ControlURL         string `json:"control_url"`          // controlUrl (Headscale)
	PreventDuplicates  bool   `json:"prevent_duplicates"`   // preventDuplicates
	MaxCertConcurrency int    `json:"max_cert_concurrency"` // maxCertConcurrency
	// Docker provider
	TargetHostname string `json:"target_hostname"`   // docker targetHostname
	TryInternalNet bool   `json:"try_internal_net"`  // tryDockerInternalNetwork
	// Health check (applied to both docker + lists providers)
	HealthCheck    bool `json:"health_check"`    // healthCheckEnabled
	HealthInterval int  `json:"health_interval"` // healthCheckInterval (s)
	HealthFailures int  `json:"health_failures"` // healthCheckFailures
	HealthCooldown int  `json:"health_cooldown"` // healthCheckCooldown (s)
	AutoRestart    bool `json:"auto_restart"`    // autoRestart
}

func loadProxyOpts() proxyOpts {
	o := proxyOpts{ // tsdproxy v2.3.3 defaults (true-by-default fields preset so old
		// configs without the key keep the right value)
		LogLevel: "info", DashPort: 8080, AdminLocalhost: true,
		MaxCertConcurrency: 2, TargetHostname: "host.docker.internal", TryInternalNet: true,
		HealthCheck: true, HealthInterval: 30, HealthFailures: 3, HealthCooldown: 0,
		AutoRestart: true,
	}
	store.ReadJSON(proxyOptsFile, &o)
	if o.LogLevel == "" {
		o.LogLevel = "info"
	}
	if o.DashPort == 0 {
		o.DashPort = 8080
	}
	if o.MaxCertConcurrency < 1 {
		o.MaxCertConcurrency = 2
	}
	if o.TargetHostname == "" {
		o.TargetHostname = "host.docker.internal"
	}
	if o.HealthInterval < 1 {
		o.HealthInterval = 30
	}
	if o.HealthFailures < 1 {
		o.HealthFailures = 3
	}
	if o.HealthCooldown < 0 {
		o.HealthCooldown = 0
	}
	return o
}

// dashTarget is tsdproxy's own dashboard URL on the host (http.port, loopback).
func dashTarget() string { return "http://127.0.0.1:" + strconv.Itoa(loadProxyOpts().DashPort) }

// tailnetSuffix returns the host's MagicDNS suffix (e.g. "tail1f0818.ts.net") so we
// can build https redirect targets. Empty if tailscale isn't reachable (redirect is
// then skipped — https still works directly).
func tailnetSuffix() string {
	rc, out := runHost("", "tailscale", "status", "--json")
	if rc != 0 {
		return ""
	}
	var s struct {
		MagicDNSSuffix string `json:"MagicDNSSuffix"`
	}
	_ = json.Unmarshal([]byte(out), &s)
	return strings.Trim(strings.TrimSpace(s.MagicDNSSuffix), ".")
}

type proxyEntry struct {
	Name   string `json:"name"`   // tailnet hostname (→ name.<tailnet>.ts.net)
	Target string `json:"target"` // backend URL reachable from the host, e.g. http://127.0.0.1:9180
	Label  string `json:"label"`  // dashboard card label (optional)
	Icon   string `json:"icon"`   // dashboard card icon, e.g. "si/synology" (optional)
	Hidden bool   `json:"hidden"` // hide from the tsdproxy dashboard (visible defaults true)
}

// tsAuth carries how tsdproxy authenticates to the tailnet — either a plain auth
// key, or an OAuth client (recommended: never expires, but requires a tag).
type tsAuth struct {
	Mode         string `json:"mode"` // "authkey" | "oauth"
	AuthKey      string `json:"auth_key"`
	ClientID     string `json:"client_id"`
	ClientSecret string `json:"client_secret"`
	Tags         string `json:"tags"`
}

func (a *tsAuth) normalize() {
	a.Mode = strings.TrimSpace(a.Mode)
	a.AuthKey = strings.TrimSpace(a.AuthKey)
	a.ClientID = strings.TrimSpace(a.ClientID)
	a.ClientSecret = strings.TrimSpace(a.ClientSecret)
	a.Tags = strings.TrimSpace(a.Tags)
	if a.Mode == "" { // infer from which fields are present
		if a.ClientID != "" || a.ClientSecret != "" {
			a.Mode = "oauth"
		} else {
			a.Mode = "authkey"
		}
	}
	if a.Mode == "oauth" && a.Tags == "" {
		a.Tags = "tag:tsdproxy"
	}
}

func (a tsAuth) validate() error {
	if a.Mode == "oauth" {
		if a.ClientID == "" || a.ClientSecret == "" {
			return errors.New("OAuth client ID and secret are required")
		}
		return nil
	}
	if a.AuthKey == "" {
		return errors.New("Tailscale auth key required")
	}
	return nil
}

// runHost runs a non-privileged command on the host (for outbound HTTP via curl).
func runHost(stdin string, args ...string) (int, string) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	rc, out, _ := executor.Get().Run(ctx, args, stdin)
	return rc, out
}

// managedProxyCfg controls a sb-ui-managed special entry (sb-ui itself, or the
// tsdproxy dashboard). We store only enabled + the tailnet name; the target URL is
// resolved live so dynamic ports never go stale and the user never edits files.
type managedProxyCfg struct {
	Enabled bool   `json:"enabled"`
	Name    string `json:"name"`
	Label   string `json:"label"`  // dashboard card label (optional)
	Icon    string `json:"icon"`   // dashboard card icon (optional)
	Hidden  bool   `json:"hidden"` // hide from the tsdproxy dashboard
}

func loadManaged(path, defName string) managedProxyCfg {
	var c managedProxyCfg
	store.ReadJSON(path, &c)
	if strings.TrimSpace(c.Name) == "" {
		c.Name = defName
	}
	return c
}

func loadSelfProxy() managedProxyCfg { return loadManaged(proxySelf, "sb-ui") }
func loadDashProxy() managedProxyCfg { return loadManaged(proxyDash, "dash") }
func saveSelfProxy(c managedProxyCfg) { store.WriteJSON(proxySelf, c) }

// managedEntries returns the live sb-ui-managed entries (self + dashboard) that are
// enabled, with their targets resolved now.
func managedEntries() []proxyEntry {
	orDefault := func(v, def string) string {
		if strings.TrimSpace(v) == "" {
			return def
		}
		return v
	}
	var out []proxyEntry
	if s := loadSelfProxy(); s.Enabled {
		out = append(out, proxyEntry{Name: s.Name, Target: selfTarget(), Label: orDefault(s.Label, "sb-ui"), Icon: s.Icon, Hidden: s.Hidden})
	}
	if d := loadDashProxy(); d.Enabled {
		out = append(out, proxyEntry{Name: d.Name, Target: dashTarget(), Label: orDefault(d.Label, "tsdproxy Dashboard"), Icon: orDefault(d.Icon, "tsdproxy"), Hidden: d.Hidden})
	}
	return out
}

// readExistingAuth recovers the Tailscale credentials from the live tsdproxy.yaml so
// advanced-settings changes can rewrite the file without losing them (sb-ui never
// stores secrets — the file is the single source of truth).
func readExistingAuth() tsAuth {
	var a tsAuth
	rc, out := sudoRun("cat", tsdCfg)
	if rc != 0 {
		return a
	}
	unq := func(s string) string { return strings.Trim(strings.TrimSpace(s), `"`) }
	for _, ln := range strings.Split(out, "\n") {
		ln = strings.TrimSpace(ln)
		switch {
		case strings.HasPrefix(ln, "clientId:"):
			a.ClientID = unq(strings.TrimPrefix(ln, "clientId:"))
		case strings.HasPrefix(ln, "clientSecret:"):
			a.ClientSecret = unq(strings.TrimPrefix(ln, "clientSecret:"))
		case strings.HasPrefix(ln, "tags:"):
			a.Tags = unq(strings.TrimPrefix(ln, "tags:"))
		case strings.HasPrefix(ln, "authKey:"):
			a.AuthKey = unq(strings.TrimPrefix(ln, "authKey:"))
		}
	}
	a.normalize()
	return a
}

// syncSelfProxy regenerates proxies.yaml on sb-ui startup so managed entries always
// point at the current (dynamic) backend ports — they can change across reinstalls,
// and we never want the user to hand-edit the file to fix it.
func syncSelfProxy() {
	if !hostHas(tsdBin) {
		return
	}
	if !loadSelfProxy().Enabled && !loadDashProxy().Enabled {
		return
	}
	regenProxyFile(loadProxyEntries())
}

// selfTarget resolves sb-ui's own backend URL. tsdproxy runs on the host, so it
// reaches sb-ui over loopback. The port is dynamic (the Saltbox role picks one in
// 9180-9189), so we read it from the host's service unit — NOT our own process env,
// which is wrong when sb-ui is driven remotely (dev) or hasn't the var set.
func selfTarget() string {
	port := hostSbuiPort()
	if port == "" {
		addr := os.Getenv("SB_UI_ADDR") // fallback: our own listen addr
		if strings.TrimSpace(addr) == "" {
			addr = "127.0.0.1:8000"
		}
		port = addr
		if i := strings.LastIndex(addr, ":"); i >= 0 {
			port = addr[i+1:]
		}
	}
	return "http://127.0.0.1:" + port
}

// hostSbuiPort reads SB_UI_ADDR from the sb-ui systemd unit on the host and returns
// its port (e.g. "9180"). Empty if the service/var isn't found.
func hostSbuiPort() string {
	rc, out := sudoRun("systemctl", "show", "-p", "Environment", "saltbox_managed_sbui")
	if rc != 0 {
		return ""
	}
	// out like: Environment=SB_UI_ADDR=:9180 SALTBOX_CONFIGURED=true
	out = strings.TrimPrefix(strings.TrimSpace(out), "Environment=")
	for _, tok := range strings.Fields(out) {
		if v, ok := strings.CutPrefix(tok, "SB_UI_ADDR="); ok {
			if i := strings.LastIndex(v, ":"); i >= 0 {
				return v[i+1:]
			}
			return v
		}
	}
	return ""
}

// sudoRun runs a privileged command on the host.
func sudoRun(args ...string) (int, string) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	rc, out, _ := executor.Get().Run(ctx, append([]string{"sudo"}, args...), "")
	return rc, out
}

// sudoWrite writes content to a root-owned path (base64 piped through tee to avoid
// quoting issues).
func sudoWrite(path, content string) (int, string) {
	b64 := base64.StdEncoding.EncodeToString([]byte(content))
	return sudoRun("sh", "-c", "echo "+b64+" | base64 -d | tee "+path+" >/dev/null")
}

func hostHas(path string) bool {
	rc, _ := sudoRun("test", "-e", path)
	return rc == 0
}

// ── status ────────────────────────────────────────────────────────────────────

func proxyStatus(w http.ResponseWriter, _ *http.Request) {
	rc, active := sudoRun("systemctl", "is-active", "tsdproxy")
	writeJSON(w, http.StatusOK, map[string]any{
		"installed":  hostHas(tsdBin),
		"configured": hostHas(tsdCfg),
		"active":     rc == 0 && strings.TrimSpace(active) == "active",
		"status":     strings.TrimSpace(active),
	})
}

// ── install ───────────────────────────────────────────────────────────────────

func proxyInstall(w http.ResponseWriter, req *http.Request) {
	var a tsAuth
	_ = json.NewDecoder(req.Body).Decode(&a)
	a.normalize()
	if err := a.validate(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	// 1. download + install the binary (arch-detected) from the latest release.
	dl := `set -e
arch=$(uname -m); case "$arch" in x86_64) a=amd64;; aarch64) a=arm64;; armv7l) a=armv7;; armv6l) a=armv6;; *) a=amd64;; esac
url=$(curl -fsSL https://api.github.com/repos/almeidapaulopt/tsdproxy/releases/latest | grep -o "https://[^\"]*linux_${a}\.tar\.gz" | head -1)
[ -n "$url" ] || { echo "no asset for $a"; exit 1; }
tmp=$(mktemp -d); curl -fsSL "$url" -o "$tmp/t.tgz"; tar -xzf "$tmp/t.tgz" -C "$tmp"
bin=$(find "$tmp" -type f -name 'tsdproxyd' | head -1)
[ -n "$bin" ] || bin=$(find "$tmp" -type f -name 'tsdproxy' | head -1)
[ -n "$bin" ] || { echo "binary not found in tarball"; rm -rf "$tmp"; exit 1; }
sudo install -m755 "$bin" /usr/local/bin/tsdproxy; rm -rf "$tmp"`
	if rc, out := sudoRun("sh", "-c", dl); rc != 0 {
		http.Error(w, "install failed: "+strings.TrimSpace(out), http.StatusInternalServerError)
		return
	}

	// 2. directories + base config + empty list file.
	sudoRun("mkdir", "-p", tsdCfgDir, tsdData)
	if rc, out := sudoWrite(tsdCfg, baseConfig(a, loadProxyOpts())); rc != 0 {
		http.Error(w, "write config failed: "+out, http.StatusInternalServerError)
		return
	}
	if !hostHas(tsdList) {
		sudoWrite(tsdList, "{}\n")
	}

	// 3. systemd unit — Wants= (not Requires=) docker so it survives docker restarts.
	if rc, out := sudoWrite(tsdUnit, unitFile()); rc != 0 {
		http.Error(w, "write unit failed: "+out, http.StatusInternalServerError)
		return
	}
	sudoRun("systemctl", "daemon-reload")
	if rc, out := sudoRun("systemctl", "enable", "--now", "tsdproxy"); rc != 0 {
		http.Error(w, "service start failed: "+strings.TrimSpace(out), http.StatusInternalServerError)
		return
	}

	// 4. sb-ui is the management UI — expose it on the tailnet automatically so the
	// proxy is useful out of the box. Port is resolved live (it's dynamic).
	self := loadSelfProxy()
	self.Enabled = true
	saveSelfProxy(self)
	regenProxyFile(loadProxyEntries())

	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// proxyRekey rewrites the Tailscale auth key and restarts tsdproxy. It also clears
// stale per-node tsnet state so every proxy re-registers cleanly with the new key
// (a bad key leaves nodes in a NeedsLogin state that won't auto-recover).
func proxyRekey(w http.ResponseWriter, req *http.Request) {
	var a tsAuth
	_ = json.NewDecoder(req.Body).Decode(&a)
	a.normalize()
	if err := a.validate(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if !hostHas(tsdBin) {
		http.Error(w, "tsdproxy is not installed", http.StatusBadRequest)
		return
	}
	if rc, out := sudoWrite(tsdCfg, baseConfig(a, loadProxyOpts())); rc != 0 {
		http.Error(w, "write config failed: "+out, http.StatusInternalServerError)
		return
	}
	// Drop stale node state so re-registration uses the new credentials.
	sudoRun("sh", "-c", "rm -rf "+tsdData+"/default/*")
	if rc, out := sudoRun("systemctl", "restart", "tsdproxy"); rc != 0 {
		http.Error(w, "restart failed: "+strings.TrimSpace(out), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// proxyRestart restarts the tsdproxy service (handy when a node is stuck or after
// a manual config edit). Config/list changes auto-reload, so this is rarely needed.
func proxyRestart(w http.ResponseWriter, _ *http.Request) {
	if !hostHas(tsdBin) {
		http.Error(w, "tsdproxy is not installed", http.StatusBadRequest)
		return
	}
	if rc, out := sudoRun("systemctl", "restart", "tsdproxy"); rc != 0 {
		http.Error(w, "restart failed: "+strings.TrimSpace(out), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// proxyTest verifies the supplied credentials before they're committed. OAuth
// clients are validated for real: exchange client_id+secret for a token (proves
// they're correct) and, best-effort, list devices to surface the tailnet + who.
// Plain auth keys can't be introspected via API, so only a format sanity check.
func proxyTest(w http.ResponseWriter, req *http.Request) {
	var a tsAuth
	_ = json.NewDecoder(req.Body).Decode(&a)
	a.normalize()
	if err := a.validate(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if a.Mode != "oauth" {
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":          true,
			"mode":        "authkey",
			"looks_valid": strings.HasPrefix(a.AuthKey, "tskey-"),
			"note":        "Auth keys can't be verified via API — confirmed only when a node connects. Check the Tailscale admin afterwards.",
		})
		return
	}

	// OAuth client-credentials grant → access token (validates id+secret).
	body := "client_id=" + a.ClientID + "&client_secret=" + a.ClientSecret
	rc, out := runHost(body, "curl", "-fsS", "-X", "POST",
		"https://api.tailscale.com/api/v2/oauth/token", "--data", "@-")
	if rc != 0 {
		http.Error(w, "OAuth credentials rejected — check the client ID and secret", http.StatusBadRequest)
		return
	}
	var tok struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
		Scope       string `json:"scope"`
	}
	if json.Unmarshal([]byte(out), &tok); tok.AccessToken == "" {
		http.Error(w, "unexpected response from Tailscale", http.StatusBadGateway)
		return
	}

	resp := map[string]any{"ok": true, "mode": "oauth", "scope": tok.Scope, "expires_in": tok.ExpiresIn}

	// Best-effort: list devices to surface tailnet name + owner + count. Needs the
	// devices:read scope, which an auth_keys-only client may lack — ignore failures.
	if rc2, out2 := runHost("", "curl", "-fsS",
		"https://api.tailscale.com/api/v2/tailnet/-/devices",
		"-H", "Authorization: Bearer "+tok.AccessToken); rc2 == 0 {
		var dl struct {
			Devices []struct {
				Name string `json:"name"`
				User string `json:"user"`
			} `json:"devices"`
		}
		_ = json.Unmarshal([]byte(out2), &dl)
		resp["devices"] = len(dl.Devices)
		if len(dl.Devices) > 0 {
			if i := strings.Index(dl.Devices[0].Name, "."); i >= 0 {
				resp["tailnet"] = dl.Devices[0].Name[i+1:]
			}
			resp["user"] = dl.Devices[0].User
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

func baseConfig(a tsAuth, o proxyOpts) string {
	yn := func(b bool) string {
		if b {
			return "true"
		}
		return "false"
	}
	// Provider auth block: OAuth (clientId+secret+tags) or a plain auth key.
	// NB: only fields that exist in the installed tsdproxy (v2.3.3 strict YAML).
	var prov string
	if a.Mode == "oauth" {
		prov = `      clientId: "` + a.ClientID + `"
      clientSecret: "` + a.ClientSecret + `"
      tags: "` + a.Tags + `"`
	} else {
		prov = `      authKey: "` + a.AuthKey + `"`
	}
	ctrl := o.ControlURL
	if strings.TrimSpace(ctrl) == "" {
		ctrl = "https://controlplane.tailscale.com"
	}
	hi, hf, hc := strconv.Itoa(o.HealthInterval), strconv.Itoa(o.HealthFailures), strconv.Itoa(o.HealthCooldown)
	// Health-check block shared by the docker + lists providers.
	health := `    healthCheckEnabled: ` + yn(o.HealthCheck) + `
    healthCheckInterval: ` + hi + `
    healthCheckFailures: ` + hf + `
    healthCheckCooldown: ` + hc + `
    autoRestart: ` + yn(o.AutoRestart)
	// Docker + List providers both enabled (option B); list watches proxies.yaml.
	return `defaultProxyProvider: default
docker:
  local:
    host: unix:///var/run/docker.sock
    targetHostname: ` + o.TargetHostname + `
    tryDockerInternalNetwork: ` + yn(o.TryInternalNet) + `
    defaultProxyProvider: default
` + health + `
lists:
  hosts:
    filename: ` + tsdList + `
    defaultProxyProvider: default
    defaultProxyAccessLog: ` + yn(o.AccessLog) + `
` + health + `
tailscale:
  providers:
    default:
` + prov + `
      controlUrl: "` + ctrl + `"
      preventDuplicates: ` + yn(o.PreventDuplicates) + `
      maxCertConcurrency: ` + strconv.Itoa(o.MaxCertConcurrency) + `
  dataDir: ` + tsdData + `/
http:
  hostname: 127.0.0.1
  port: ` + strconv.Itoa(o.DashPort) + `
adminAllowLocalhost: ` + yn(o.AdminLocalhost) + `
proxyAccessLog: ` + yn(o.AccessLog) + `
log:
  level: ` + o.LogLevel + `
  json: ` + yn(o.LogJSON) + `
`
}

func unitFile() string {
	return `[Unit]
Description=TSDProxy (Tailscale reverse proxy)
After=network.target docker.service
Wants=docker.service

[Service]
Type=simple
ExecStart=` + tsdBin + ` --config ` + tsdCfg + `
Restart=always
RestartSec=10

[Install]
WantedBy=multi-user.target
`
}

// ── list management (sb-ui JSON → regenerate proxies.yaml) ─────────────────────

func loadProxyEntries() []proxyEntry {
	var e []proxyEntry
	store.ReadJSON(proxyLists, &e)
	return e
}

// writeProxyList persists the user entries and regenerates tsdproxy's watched list
// file (it auto-reloads on change — no restart).
func writeProxyList(entries []proxyEntry) (int, string) {
	store.WriteJSON(proxyLists, entries)
	return regenProxyFile(entries)
}

// regenProxyFile builds proxies.yaml from the sb-ui-managed entries (resolved live)
// + the user host-service entries. Managed entries win on name collision so their
// ports stay fresh even if the user also added a stale manual entry.
func regenProxyFile(entries []proxyEntry) (int, string) {
	var all []proxyEntry
	seen := map[string]bool{}
	for _, m := range managedEntries() {
		all = append(all, m)
		seen[m.Name] = true
	}
	for _, e := range entries {
		if seen[e.Name] {
			continue
		}
		all = append(all, e)
	}
	suffix := tailnetSuffix() // for http→https redirect targets (empty = skip redirect)
	var sb strings.Builder
	if len(all) == 0 {
		sb.WriteString("{}\n")
	}
	for _, e := range all {
		writeEntryYAML(&sb, e, suffix)
	}
	return sudoWrite(tsdList, sb.String())
}

// writeEntryYAML emits one proxies.yaml entry: the 443/https proxy, an optional
// 80/http→https redirect (so the bare hostname works in a browser), and an optional
// dashboard card (label/icon).
func writeEntryYAML(sb *strings.Builder, e proxyEntry, suffix string) {
	sb.WriteString(e.Name + ":\n  ports:\n    \"443\":\n      targets:\n        - " + e.Target + "\n")
	if suffix != "" {
		sb.WriteString("    \"80/http\":\n      isRedirect: true\n      targets:\n        - https://" + e.Name + "." + suffix + "\n")
	}
	if e.Label != "" || e.Icon != "" || e.Hidden {
		sb.WriteString("  dashboard:\n")
		if e.Hidden {
			sb.WriteString("    visible: false\n")
		}
		if e.Label != "" {
			sb.WriteString("    label: \"" + e.Label + "\"\n")
		}
		if e.Icon != "" {
			sb.WriteString("    icon: \"" + e.Icon + "\"\n")
		}
	}
}

// ── managed entries (sb-ui itself + tsdproxy dashboard) ─────────────────────────

// proxyGetManaged / proxyPutManaged drive a toggle + tailnet-name setting for a
// sb-ui-managed special entry. targetFn resolves the live backend URL.
func proxyGetManaged(load func() managedProxyCfg, targetFn func() string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		c := load()
		writeJSON(w, http.StatusOK, map[string]any{"enabled": c.Enabled, "name": c.Name, "label": c.Label, "icon": c.Icon, "hidden": c.Hidden, "target": targetFn()})
	}
}

func proxyPutManaged(path, defName string) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		var c managedProxyCfg
		if err := json.NewDecoder(req.Body).Decode(&c); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		c.Name = strings.TrimSpace(c.Name)
		c.Label = strings.TrimSpace(c.Label)
		c.Icon = strings.TrimSpace(c.Icon)
		if c.Name == "" {
			c.Name = defName
		}
		if strings.ContainsAny(c.Name, " \t\n/:") {
			http.Error(w, "name must be a bare tailnet hostname", http.StatusBadRequest)
			return
		}
		store.WriteJSON(path, c)
		if rc, out := regenProxyFile(loadProxyEntries()); rc != 0 {
			http.Error(w, out, http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	}
}

func proxyGetSelf(w http.ResponseWriter, r *http.Request) { proxyGetManaged(loadSelfProxy, selfTarget)(w, r) }
func proxyPutSelf(w http.ResponseWriter, r *http.Request) { proxyPutManaged(proxySelf, "sb-ui")(w, r) }
func proxyGetDash(w http.ResponseWriter, r *http.Request) {
	proxyGetManaged(loadDashProxy, dashTarget)(w, r)
}
func proxyPutDash(w http.ResponseWriter, r *http.Request) { proxyPutManaged(proxyDash, "dash")(w, r) }

// ── advanced tsdproxy.yaml settings ─────────────────────────────────────────────

func proxyGetOpts(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, loadProxyOpts())
}

func proxyPutOpts(w http.ResponseWriter, req *http.Request) {
	var o proxyOpts
	if err := json.NewDecoder(req.Body).Decode(&o); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	switch o.LogLevel {
	case "trace", "debug", "info", "warn", "error", "fatal", "panic": // ok
	default:
		o.LogLevel = "info"
	}
	if o.DashPort < 1024 || o.DashPort > 65535 {
		o.DashPort = 8080
	}
	if o.MaxCertConcurrency < 1 {
		o.MaxCertConcurrency = 2
	}
	if o.HealthInterval < 1 {
		o.HealthInterval = 30
	}
	if o.HealthFailures < 1 {
		o.HealthFailures = 3
	}
	if o.HealthCooldown < 0 {
		o.HealthCooldown = 0
	}
	o.ControlURL = strings.TrimSpace(o.ControlURL)
	o.TargetHostname = strings.TrimSpace(o.TargetHostname)
	if o.TargetHostname == "" {
		o.TargetHostname = "host.docker.internal"
	}
	store.WriteJSON(proxyOptsFile, o)

	// Apply to the live config (preserving credentials from the existing file) and
	// restart. Server-level settings (port, log, access log) need a restart; the
	// dashboard proxy target may also have changed, so regenerate the list too.
	if hostHas(tsdBin) {
		a := readExistingAuth()
		if err := a.validate(); err != nil {
			http.Error(w, "saved, but couldn't apply: no credentials found in tsdproxy.yaml — set them in the Authentication tab first", http.StatusConflict)
			return
		}
		if rc, out := sudoWrite(tsdCfg, baseConfig(a, o)); rc != 0 {
			http.Error(w, "write config failed: "+out, http.StatusInternalServerError)
			return
		}
		regenProxyFile(loadProxyEntries())
		if rc, out := sudoRun("systemctl", "restart", "tsdproxy"); rc != 0 {
			http.Error(w, "restart failed: "+strings.TrimSpace(out), http.StatusInternalServerError)
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func proxyGetLists(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"entries": loadProxyEntries()})
}

func proxyAddList(w http.ResponseWriter, req *http.Request) {
	var e proxyEntry
	_ = json.NewDecoder(req.Body).Decode(&e)
	e.Name = strings.TrimSpace(e.Name)
	e.Target = strings.TrimSpace(e.Target)
	e.Label = strings.TrimSpace(e.Label)
	e.Icon = strings.TrimSpace(e.Icon)
	if e.Name == "" || e.Target == "" || strings.ContainsAny(e.Name, " \t\n/:") {
		http.Error(w, "name + target required (name = tailnet hostname)", http.StatusBadRequest)
		return
	}
	entries := loadProxyEntries()
	for i := range entries {
		if entries[i].Name == e.Name {
			entries[i] = e // update
			if rc, out := writeProxyList(entries); rc != 0 {
				http.Error(w, out, http.StatusInternalServerError)
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"ok": true})
			return
		}
	}
	entries = append(entries, e)
	if rc, out := writeProxyList(entries); rc != 0 {
		http.Error(w, out, http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func proxyDelList(w http.ResponseWriter, req *http.Request) {
	name := chi.URLParam(req, "name")
	entries := loadProxyEntries()
	out := entries[:0]
	for _, e := range entries {
		if e.Name != name {
			out = append(out, e)
		}
	}
	if rc, msg := writeProxyList(out); rc != 0 {
		http.Error(w, msg, http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
