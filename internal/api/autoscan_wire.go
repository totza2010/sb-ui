package api

// Auto-wire an *arr's Webhook notification via its own API. Since we can reach each
// discovered *arr's API (X-Api-Key over the docker bridge), we can also create/update
// its Webhook connection and trigger its built-in test — trying the URLs the *arr can
// actually reach us on (docker gateway, host.docker.internal, the browser host) and
// reporting which works. This removes the manual "paste a URL, guess the host" dance
// and diagnoses "connection refused" straight from the *arr's own test result.

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"
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

// webhookCandidates lists the base URLs an *arr might reach sb-ui on, best-first.
func webhookCandidates(inst arrInstance, browserHost string) []string {
	port := serverPort()
	var bases []string
	add := func(host string) {
		if host = strings.TrimSpace(host); host != "" {
			bases = append(bases, "http://"+host+":"+port)
		}
	}
	add(gatewayOf(inst.IP))     // docker gateway = the host, from inside the container
	add("host.docker.internal") // works on some docker setups
	add(browserHost)            // the host you loaded the UI on (LAN/hostname)
	// dedup preserving order
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

// webhookResource builds an *arr Webhook notification body (shared by test + save).
// The token rides in the URL path, so no basic-auth fields are needed.
func webhookResource(url string, id int) string {
	idField := ""
	if id > 0 {
		idField = fmt.Sprintf(`"id":%d,`, id)
	}
	return fmt.Sprintf(`{%s"onDownload":true,"onUpgrade":true,"onRename":true,"name":%q,`+
		`"implementation":"Webhook","configContract":"WebhookSettings","tags":[],`+
		`"fields":[{"name":"url","value":%q},{"name":"method","value":1}]}`,
		idField, autoscanWebhookName, url)
}

// arrTestWebhook asks the *arr to send a test webhook to url and reports the outcome —
// this is the *arr's own reachability test (the thing that throws "connection refused").
func arrTestWebhook(inst arrInstance, url string) (bool, string) {
	ok, body := arrSendRaw(inst, http.MethodPost, "notification/test", webhookResource(url, 0))
	if ok {
		return true, ""
	}
	return false, shortArrError(body)
}

// shortArrError pulls a readable reason out of an *arr validation/error response.
func shortArrError(body string) string {
	body = strings.TrimSpace(body)
	// array of validation failures: [{"errorMessage":"…"}]
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

// arrFindNotification returns the id of our webhook notification if it already exists.
func arrFindNotification(inst arrInstance) (int, bool) {
	ok, body := arrGetRaw(inst, "notification")
	if !ok {
		return 0, false
	}
	var list []struct {
		ID   int    `json:"id"`
		Name string `json:"name"`
	}
	if json.Unmarshal([]byte(body), &list) != nil {
		return 0, false
	}
	for _, n := range list {
		if strings.EqualFold(n.Name, autoscanWebhookName) {
			return n.ID, true
		}
	}
	return 0, false
}

// arrSaveWebhook creates or updates our webhook notification in the *arr.
func arrSaveWebhook(inst arrInstance, url string) (bool, string) {
	if id, exists := arrFindNotification(inst); exists {
		ok, body := arrSendRaw(inst, http.MethodPut, fmt.Sprintf("notification/%d", id), webhookResource(url, id))
		if ok {
			return true, ""
		}
		return false, shortArrError(body)
	}
	ok, body := arrSendRaw(inst, http.MethodPost, "notification", webhookResource(url, 0))
	if ok {
		return true, ""
	}
	return false, shortArrError(body)
}

type wireCandidate struct {
	URL   string `json:"url"`
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

// autoscanWire tests (and optionally saves) our Webhook connection in one *arr via its
// API: it tries each candidate URL through the *arr's own test until one succeeds.
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
	if loadOptions().Autoscan.WebhookToken == "" {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": "no webhook token yet — save the autoscan config first"})
		return
	}
	if listenLoopbackOnly() {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false,
			"error": "sb-ui is bound to loopback (" + serverAddr + ") — no container can reach it. Set SB_UI_ADDR=:" + serverPort() + " and restart."})
		return
	}

	token := loadOptions().Autoscan.WebhookToken
	var cands []wireCandidate
	working := ""
	for _, base := range webhookCandidates(inst, b.Hostname) {
		url := base + "/api/autoscan/webhook/" + token
		tOK, reason := arrTestWebhook(inst, url)
		cands = append(cands, wireCandidate{URL: url, OK: tOK, Error: reason})
		if tOK {
			working = url
			break // first reachable URL wins
		}
	}

	res := map[string]any{"ok": working != "", "candidates": cands, "working": working}
	if b.Save && working != "" {
		saved, err := arrSaveWebhook(inst, working)
		res["saved"] = saved
		if !saved {
			res["save_error"] = err
		}
	}
	writeJSON(w, http.StatusOK, res)
}
