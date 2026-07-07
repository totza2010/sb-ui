package api

// Auto-wire an *arr's Webhook connection via its own API using the typed devopsarr
// clients. Two independent directions:
//   - SEND (sb-ui → arr, create/update the notification): works whenever the arr API is
//     reachable. Uses ForceSave so the arr persists it even when it can't reach the URL
//     itself (e.g. during setup, or a dev instance) — the config is what matters.
//   - RECEIVE (arr → sb-ui, the arr's own Test): informational — confirms the arr can
//     reach us on the chosen URL. Fine for it to fail; the webhook is still set.
// The notification body is taken from the arr's own schema template, so every required
// field for that arr/version is present and correct.

import (
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"strconv"
	"strings"

	"sb-ui/internal/executor"

	"github.com/devopsarr/radarr-go/radarr"
	"github.com/devopsarr/sonarr-go/sonarr"
	"github.com/devopsarr/whisparr-go/whisparr"
)

const autoscanWebhookName = "sb-ui autoscan"

// arrByConnKey resolves a connection-registry key back to its discovered *arr instance.
func arrByConnKey(key string) (arrInstance, bool) {
	for _, inst := range arrInstancesCached() {
		if connKey(inst.Kind, inst.Name, "") == key {
			return inst, true
		}
	}
	return arrInstance{}, false
}

// listenLoopbackOnly reports whether sb-ui is bound to loopback — in which case no
// container can reach it no matter the URL (the real fix is SB_UI_ADDR=:port).
func listenLoopbackOnly() bool {
	host, _, err := net.SplitHostPort(serverAddr)
	if err != nil {
		return false
	}
	return host == "127.0.0.1" || host == "localhost" || host == "::1"
}

// gatewayOf returns the likely docker-bridge gateway (host) for a container IP, e.g.
// 172.19.0.23 → 172.19.0.1 — the address a container uses to reach services on the host.
func gatewayOf(ip string) string {
	if i := strings.LastIndex(ip, "."); i > 0 {
		return ip[:i] + ".1"
	}
	return ""
}

// hostIPv4s returns the host's non-loopback IPv4 addresses (LAN IPs like 192.168.1.170)
// — the most reliable way for a container to reach sb-ui on the host, since it doesn't
// depend on the docker network layout the way the per-network gateway does. Only
// meaningful when sb-ui runs on the host itself (local executor).
func hostIPv4s() []string {
	if _, local := executor.Get().(executor.LocalExecutor); !local {
		return nil
	}
	var out []string
	addrs, _ := net.InterfaceAddrs()
	for _, a := range addrs {
		var ip net.IP
		switch v := a.(type) {
		case *net.IPNet:
			ip = v.IP
		case *net.IPAddr:
			ip = v.IP
		}
		if ip == nil || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
			continue
		}
		if ip4 := ip.To4(); ip4 != nil {
			out = append(out, ip4.String())
		}
	}
	return out
}

// webhookCandidates lists the base URLs an *arr might reach sb-ui on, best-first: the
// host's own LAN IP (works regardless of docker network), then the per-network gateway,
// then host.docker.internal, then the host you loaded the UI on.
func webhookCandidates(inst arrInstance, browserHost string) []string {
	port := serverPort()
	var bases []string
	add := func(host string) {
		if host = strings.TrimSpace(host); host != "" {
			bases = append(bases, "http://"+host+":"+port)
		}
	}
	for _, ip := range hostIPv4s() {
		add(ip)
	}
	add(gatewayOf(inst.IP))
	add("host.docker.internal")
	add(browserHost)
	seen := map[string]bool{}
	out := bases[:0]
	for _, b := range bases {
		if !seen[b] {
			seen[b] = true
			out = append(out, b)
		}
	}
	return out
}

// arrAPIErr turns a typed devopsarr error into a readable reason, pulling the arr's own
// message out of the response body when present.
func arrAPIErr(err error) string {
	if err == nil {
		return ""
	}
	var withBody interface{ Body() []byte }
	if errors.As(err, &withBody) {
		if r := shortArrError(strings.TrimSpace(string(withBody.Body()))); r != "" {
			return r
		}
	}
	return err.Error()
}

// shortArrError pulls a readable reason out of an *arr validation/error response body.
func shortArrError(body string) string {
	body = strings.TrimSpace(body)
	var arr []struct {
		ErrorMessage string `json:"errorMessage"`
	}
	if json.Unmarshal([]byte(body), &arr) == nil {
		for _, e := range arr {
			if e.ErrorMessage != "" {
				return tidyReason(e.ErrorMessage)
			}
		}
	}
	var obj struct {
		Message string `json:"message"`
	}
	if json.Unmarshal([]byte(body), &obj) == nil && obj.Message != "" {
		return tidyReason(obj.Message)
	}
	if len(body) > 200 {
		body = body[:200] + "…"
	}
	return tidyReason(body)
}

func tidyReason(s string) string {
	switch {
	case strings.Contains(s, "Connection refused") || strings.Contains(s, "actively refused"):
		return "connection refused — sb-ui not reachable at that host:port"
	case strings.Contains(s, "timed out") || strings.Contains(s, "timeout"):
		return "timed out — host unreachable / firewalled"
	case strings.Contains(s, "No such host") || strings.Contains(s, "not resolve"):
		return "DNS — host not resolvable from the *arr"
	}
	return strings.TrimSpace(s)
}

// autoscanWebhookEvents returns the configured trigger set as canonical flags. Empty
// config = the sensible default (import + upgrade + rename).
func autoscanWebhookEvents() map[string]bool {
	evs := loadOptions().Autoscan.WebhookEvents
	if len(evs) == 0 {
		evs = []string{"import", "upgrade", "rename"}
	}
	m := map[string]bool{}
	for _, e := range evs {
		m[strings.ToLower(strings.TrimSpace(e))] = true
	}
	return m
}

// ── typed per-*arr wiring (sonarr / radarr / whisparr share the same generated API) ──

// arrWire creates/updates (save) or tests (save=false) the "sb-ui autoscan" Webhook
// notification in one *arr, dispatching to the right typed client. Returns ok + reason.
func arrWire(inst arrInstance, url string, save bool) (bool, string) {
	switch inst.Kind {
	case "sonarr":
		return sonarrWire(inst, url, save)
	case "radarr":
		return radarrWire(inst, url, save)
	case "whisparr":
		return whisparrWire(inst, url, save)
	}
	return false, "auto-wire isn't supported for " + inst.Kind
}

func sonarrWire(inst arrInstance, url string, save bool) (bool, string) {
	c := sonarrClient(inst)
	ctx, cancel := arrCtx()
	defer cancel()

	schemas, _, err := c.NotificationAPI.ListNotificationSchema(ctx).Execute()
	if err != nil {
		return false, arrAPIErr(err)
	}
	var res *sonarr.NotificationResource
	for i := range schemas {
		if schemas[i].GetImplementation() == "Webhook" {
			r := schemas[i]
			res = &r
			break
		}
	}
	if res == nil {
		return false, "this arr has no Webhook notification type"
	}
	res.SetName(autoscanWebhookName)
	ev := autoscanWebhookEvents()
	res.SetOnDownload(ev["import"])
	res.SetOnUpgrade(ev["upgrade"])
	res.SetOnRename(ev["rename"])
	res.SetOnEpisodeFileDelete(ev["delete"])
	res.SetOnEpisodeFileDeleteForUpgrade(ev["delete"])
	fields := res.GetFields()
	for i := range fields {
		switch fields[i].GetName() {
		case "url":
			fields[i].SetValue(url)
		case "method":
			fields[i].SetValue(int32(1)) // POST
		}
	}
	res.SetFields(fields)

	// existing id — so the test's uniqueness check excludes ours, and save updates it
	var id int32
	if list, _, e := c.NotificationAPI.ListNotification(ctx).Execute(); e == nil {
		for _, n := range list {
			if strings.EqualFold(n.GetName(), autoscanWebhookName) {
				id = n.GetId()
				break
			}
		}
	}
	if id > 0 {
		res.SetId(id)
	}

	if !save {
		if _, err = c.NotificationAPI.TestNotification(ctx).ForceTest(true).NotificationResource(*res).Execute(); err != nil {
			return false, tidyReason(arrAPIErr(err))
		}
		return true, ""
	}
	if id > 0 {
		_, _, err = c.NotificationAPI.UpdateNotification(ctx, id).ForceSave(true).NotificationResource(*res).Execute()
	} else {
		_, _, err = c.NotificationAPI.CreateNotification(ctx).ForceSave(true).NotificationResource(*res).Execute()
	}
	if err != nil {
		return false, arrAPIErr(err)
	}
	return true, ""
}

func radarrWire(inst arrInstance, url string, save bool) (bool, string) {
	c := radarrClient(inst)
	ctx, cancel := arrCtx()
	defer cancel()

	schemas, _, err := c.NotificationAPI.ListNotificationSchema(ctx).Execute()
	if err != nil {
		return false, arrAPIErr(err)
	}
	var res *radarr.NotificationResource
	for i := range schemas {
		if schemas[i].GetImplementation() == "Webhook" {
			r := schemas[i]
			res = &r
			break
		}
	}
	if res == nil {
		return false, "this arr has no Webhook notification type"
	}
	res.SetName(autoscanWebhookName)
	ev := autoscanWebhookEvents()
	res.SetOnDownload(ev["import"])
	res.SetOnUpgrade(ev["upgrade"])
	res.SetOnRename(ev["rename"])
	res.SetOnMovieFileDelete(ev["delete"])
	res.SetOnMovieFileDeleteForUpgrade(ev["delete"])
	fields := res.GetFields()
	for i := range fields {
		switch fields[i].GetName() {
		case "url":
			fields[i].SetValue(url)
		case "method":
			fields[i].SetValue(int32(1))
		}
	}
	res.SetFields(fields)

	var id int32
	if list, _, e := c.NotificationAPI.ListNotification(ctx).Execute(); e == nil {
		for _, n := range list {
			if strings.EqualFold(n.GetName(), autoscanWebhookName) {
				id = n.GetId()
				break
			}
		}
	}
	if id > 0 {
		res.SetId(id)
	}

	if !save {
		if _, err = c.NotificationAPI.TestNotification(ctx).NotificationResource(*res).Execute(); err != nil {
			return false, tidyReason(arrAPIErr(err))
		}
		return true, ""
	}
	if id > 0 {
		_, _, err = c.NotificationAPI.UpdateNotification(ctx, id).ForceSave(true).NotificationResource(*res).Execute()
	} else {
		_, _, err = c.NotificationAPI.CreateNotification(ctx).ForceSave(true).NotificationResource(*res).Execute()
	}
	if err != nil {
		return false, arrAPIErr(err)
	}
	return true, ""
}

func whisparrWire(inst arrInstance, url string, save bool) (bool, string) {
	c := whisparrClient(inst)
	ctx, cancel := arrCtx()
	defer cancel()

	schemas, _, err := c.NotificationAPI.ListNotificationSchema(ctx).Execute()
	if err != nil {
		return false, arrAPIErr(err)
	}
	var res *whisparr.NotificationResource
	for i := range schemas {
		if schemas[i].GetImplementation() == "Webhook" {
			r := schemas[i]
			res = &r
			break
		}
	}
	if res == nil {
		return false, "this arr has no Webhook notification type"
	}
	res.SetName(autoscanWebhookName)
	ev := autoscanWebhookEvents()
	res.SetOnDownload(ev["import"])
	res.SetOnUpgrade(ev["upgrade"])
	res.SetOnRename(ev["rename"])
	res.SetOnMovieFileDelete(ev["delete"])
	res.SetOnMovieFileDeleteForUpgrade(ev["delete"])
	fields := res.GetFields()
	for i := range fields {
		switch fields[i].GetName() {
		case "url":
			fields[i].SetValue(url)
		case "method":
			fields[i].SetValue(int32(1))
		}
	}
	res.SetFields(fields)

	var id int32
	if list, _, e := c.NotificationAPI.ListNotification(ctx).Execute(); e == nil {
		for _, n := range list {
			if strings.EqualFold(n.GetName(), autoscanWebhookName) {
				id = n.GetId()
				break
			}
		}
	}
	if id > 0 {
		res.SetId(id)
	}

	if !save {
		if _, err = c.NotificationAPI.TestNotification(ctx).NotificationResource(*res).Execute(); err != nil {
			return false, tidyReason(arrAPIErr(err))
		}
		return true, ""
	}
	if id > 0 {
		_, _, err = c.NotificationAPI.UpdateNotification(ctx, strconv.Itoa(int(id))).ForceSave(true).NotificationResource(*res).Execute()
	} else {
		_, _, err = c.NotificationAPI.CreateNotification(ctx).ForceSave(true).NotificationResource(*res).Execute()
	}
	if err != nil {
		return false, arrAPIErr(err)
	}
	return true, ""
}

// arrConnectionNames lists the names of every Connection (notification) in the *arr —
// used to prove, after a save, that "sb-ui autoscan" actually landed in THIS arr (so a
// "nothing changed" report where the user is looking at a different arr is obvious).
func arrConnectionNames(inst arrInstance) []string {
	ok, body := arrGetRaw(inst, "notification")
	if !ok {
		return nil
	}
	var list []struct {
		Name string `json:"name"`
	}
	if json.Unmarshal([]byte(body), &list) != nil {
		return nil
	}
	names := make([]string, 0, len(list))
	for _, n := range list {
		names = append(names, n.Name)
	}
	return names
}

// arrWiredURL returns the URL of our "sb-ui autoscan" webhook in the arr, if configured
// (so the UI can show a persistent "wired" state, not just a transient save result).
func arrWiredURL(inst arrInstance) (string, bool) {
	ok, body := arrGetRaw(inst, "notification")
	if !ok {
		return "", false
	}
	var list []struct {
		Name   string `json:"name"`
		Fields []struct {
			Name  string          `json:"name"`
			Value json.RawMessage `json:"value"` // mixed types (url=string, method=number)
		} `json:"fields"`
	}
	if json.Unmarshal([]byte(body), &list) != nil {
		return "", false
	}
	for _, n := range list {
		if !strings.EqualFold(n.Name, autoscanWebhookName) {
			continue
		}
		for _, f := range n.Fields {
			if f.Name == "url" {
				var s string
				if json.Unmarshal(f.Value, &s) == nil {
					return s, true
				}
			}
		}
		return "", true
	}
	return "", false
}

type wireCandidate struct {
	URL   string `json:"url"`
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

// autoscanWire writes our Webhook connection into one *arr and reports the two directions
// independently (see file header): SEND always runs (ForceSave), RECEIVE is a best-effort
// reachability test used to pick the URL and inform the user.
func autoscanWire(w http.ResponseWriter, req *http.Request) {
	var b struct {
		Key      string `json:"key"`
		Hostname string `json:"hostname"`
		Save     bool   `json:"save"`
	}
	_ = json.NewDecoder(req.Body).Decode(&b)

	inst, ok := arrByConnKey(b.Key)
	if !ok {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": "no discovered *arr matches this connection"})
		return
	}
	token := loadOptions().Autoscan.WebhookToken
	if token == "" {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": "no webhook token yet — save the autoscan config first"})
		return
	}
	candidates := webhookCandidates(inst, b.Hostname)
	if len(candidates) == 0 {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": "couldn't work out any URL the *arr could reach sb-ui on"})
		return
	}

	var cands []wireCandidate
	working := ""
	for _, base := range candidates {
		url := base + "/api/autoscan/webhook/" + token
		tOK, reason := arrWire(inst, url, false)
		cands = append(cands, wireCandidate{URL: url, OK: tOK, Error: reason})
		if tOK && working == "" {
			working = url
		}
	}
	// URL to write: the one the arr could reach, else the best guess (host LAN IP first).
	saveURL := working
	if saveURL == "" {
		saveURL = candidates[0] + "/api/autoscan/webhook/" + token
	}

	res := map[string]any{"ok": working != "", "candidates": cands, "working": working}
	if b.Save {
		saved, err := arrWire(inst, saveURL, true)
		res["saved"] = saved
		res["saved_url"] = saveURL
		res["ok"] = saved // success = the webhook was written; reachability is secondary
		res["arr"] = inst.Kind + " · " + inst.Name
		res["connections"] = arrConnectionNames(inst) // proof of what's now in THIS arr
		if !saved {
			res["save_error"] = err
		}
	}
	if working == "" && listenLoopbackOnly() {
		res["note"] = "This sb-ui is bound to loopback (" + serverAddr + ") — if it's the one the arr should reach, set SB_UI_ADDR=:" + serverPort() + " so containers can reach it."
	}
	writeJSON(w, http.StatusOK, res)
}
