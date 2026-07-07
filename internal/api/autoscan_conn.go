package api

// Connection registry for the built-in autoscan: a persistent record of every *arr
// that talks to us (inbound webhooks) crossed with an active health probe of each
// discovered *arr's API (outbound). Two directions, both surfaced:
//   - inbound  (arr → sb-ui): last webhook time / result / hits — did the arr reach us?
//   - outbound (sb-ui → arr): GET /api/v3/system/status with the arr's API key — can we
//     reach it, and if not, why (refused / timeout / DNS / 401)?
// A background prober re-checks periodically so a dropped link shows up on its own.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"sb-ui/internal/store"
)

const (
	autoscanConnsRel = "cache/autoscan_connections.json"
	autoscanConnsMax = 60
	connProbeEvery   = 60 * time.Second
)

// connLink is one known connection — keyed by app + instance (falling back to the
// caller IP for senders we can't name). Persisted so the picture survives restarts.
type connLink struct {
	Key        string     `json:"key"`
	Source     string     `json:"source"`              // sonarr | radarr | whisparr | generic | unknown
	Instance   string     `json:"instance,omitempty"`  // instanceName from the webhook / container name
	AppURL     string     `json:"app_url,omitempty"`   // applicationUrl the arr advertised
	ProbeURL   string     `json:"probe_url,omitempty"` // base URL we probe (docker/web)
	Remote     string     `json:"remote,omitempty"`    // caller IP of the last webhook
	FirstSeen  time.Time  `json:"first_seen"`
	LastSeen   *time.Time `json:"last_seen,omitempty"` // last inbound webhook (nil = never)
	LastEvent  string     `json:"last_event,omitempty"`
	LastResult string     `json:"last_result,omitempty"`
	Hits       int        `json:"hits"`
	Matched    bool       `json:"matched"` // linked to a discovered arr we can actively probe
	// active probe (sb-ui → arr API)
	Health     string     `json:"health"` // ok | fail | unknown
	HealthAt   *time.Time `json:"health_at,omitempty"`
	HealthNote string     `json:"health_note,omitempty"` // version+latency on ok, reason on fail
}

type connFile struct {
	Links []*connLink `json:"links"`
}

type connRegistry struct {
	mu    sync.Mutex
	links []*connLink
}

var (
	connOnce sync.Once
	connInst *connRegistry
)

func connReg() *connRegistry {
	connOnce.Do(func() {
		connInst = &connRegistry{}
		var f connFile
		store.ReadJSON(autoscanConnsRel, &f)
		links := f.Links[:0]
		for _, l := range f.Links {
			if l.Instance == "" && isLoopbackRemote(l.Remote) { // drop stale self-test entries
				continue
			}
			if l.Health == "" {
				l.Health = "unknown"
			}
			links = append(links, l)
		}
		connInst.links = links
		go connInst.runProber()
	})
	return connInst
}

// normName collapses "Sonarr 4K" / "sonarr-4k" so a webhook's instanceName matches the
// discovered container name.
func normName(s string) string {
	return strings.NewReplacer(" ", "", "-", "", "_", "").Replace(strings.ToLower(strings.TrimSpace(s)))
}

// isLoopbackRemote reports whether an IP is the local host (i.e. our own self-test).
func isLoopbackRemote(ip string) bool {
	ip = strings.TrimSpace(ip)
	return ip == "::1" || ip == "localhost" || strings.HasPrefix(ip, "127.")
}

func connKey(source, instance, remote string) string {
	source = strings.ToLower(strings.TrimSpace(source))
	if n := normName(instance); n != "" {
		return source + "/" + n
	}
	if remote != "" {
		return source + "@" + remote
	}
	return source
}

// byKeyLocked finds a link by key (caller holds mu).
func (r *connRegistry) byKeyLocked(key string) *connLink {
	for _, l := range r.links {
		if l.Key == key {
			return l
		}
	}
	return nil
}

func (r *connRegistry) persistLocked() {
	if len(r.links) > autoscanConnsMax { // drop the least-recently-active
		trimConns(r.links)
		r.links = r.links[:autoscanConnsMax]
	}
	store.WriteJSON(autoscanConnsRel, connFile{Links: r.links})
}

// trimConns sorts links so the freshest (by last activity) are first, before capping.
func trimConns(ls []*connLink) {
	activity := func(l *connLink) time.Time {
		t := l.FirstSeen
		if l.LastSeen != nil && l.LastSeen.After(t) {
			t = *l.LastSeen
		}
		if l.HealthAt != nil && l.HealthAt.After(t) {
			t = *l.HealthAt
		}
		return t
	}
	for i := 1; i < len(ls); i++ { // insertion sort (small n) newest-first
		for j := i; j > 0 && activity(ls[j]).After(activity(ls[j-1])); j-- {
			ls[j], ls[j-1] = ls[j-1], ls[j]
		}
	}
}

// upsertInbound records an inbound webhook against its connection (creating one if new).
func (r *connRegistry) upsertInbound(h inboundHook) {
	r.mu.Lock()
	defer r.mu.Unlock()
	key := connKey(h.Source, h.Instance, h.Remote)
	l := r.byKeyLocked(key)
	now := time.Now()
	if l == nil {
		l = &connLink{Key: key, Source: h.Source, Health: "unknown", FirstSeen: now}
		r.links = append(r.links, l)
	}
	if h.Instance != "" {
		l.Instance = h.Instance
	}
	if h.AppURL != "" {
		l.AppURL = h.AppURL
	}
	if h.Remote != "" {
		l.Remote = h.Remote
	}
	t := now
	l.LastSeen = &t
	l.LastEvent = h.Event
	l.LastResult = h.Result
	l.Hits++
	r.persistLocked()
}

// probeArrInstance actively checks one arr's API (system/status), returning ok + a short
// human reason (version+latency on success, the failure cause otherwise).
func probeArrInstance(inst arrInstance) (bool, string) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	url := arrBaseURL(inst) + "/api/v3/system/status"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return false, err.Error()
	}
	req.Header.Set("X-Api-Key", inst.APIKey)
	start := time.Now()
	resp, err := arrHTTP.Do(req)
	if err != nil {
		return false, transportReason(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	ms := time.Since(start).Milliseconds()
	switch {
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return false, fmt.Sprintf("HTTP %d — API key rejected", resp.StatusCode)
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		var s struct {
			Version string `json:"version"`
		}
		_ = json.Unmarshal(b, &s)
		if s.Version != "" {
			return true, fmt.Sprintf("v%s · %dms", s.Version, ms)
		}
		return true, fmt.Sprintf("reachable · %dms", ms)
	default:
		return false, fmt.Sprintf("HTTP %d", resp.StatusCode)
	}
}

// transportReason turns a Go transport error into a short cause the UI can show.
func transportReason(err error) string {
	s := err.Error()
	switch {
	case strings.Contains(s, "connection refused"):
		return "connection refused — arr not listening / wrong port"
	case strings.Contains(s, "context deadline exceeded") || strings.Contains(s, "Client.Timeout"):
		return "timed out — arr unreachable / firewalled"
	case strings.Contains(s, "no such host"):
		return "DNS lookup failed — hostname not resolvable"
	case strings.Contains(s, "no route to host"):
		return "no route to host"
	}
	if i := strings.LastIndex(s, ": "); i > 0 { // drop the noisy prefix
		return s[i+2:]
	}
	return s
}

// probeAll ensures a link for every discovered arr, probes each, and returns the list.
func (r *connRegistry) probeAll() []connLink {
	insts := arrInstancesCached()

	r.mu.Lock()
	for _, inst := range insts {
		key := connKey(inst.Kind, inst.Name, "")
		l := r.byKeyLocked(key)
		if l == nil {
			l = &connLink{Key: key, Source: inst.Kind, Instance: inst.Name, Health: "unknown", FirstSeen: time.Now()}
			r.links = append(r.links, l)
		}
		l.Matched = true
		l.ProbeURL = arrBaseURL(inst)
	}
	r.mu.Unlock()

	for _, inst := range insts {
		ok, note := probeArrInstance(inst)
		now := time.Now()
		r.mu.Lock()
		if l := r.byKeyLocked(connKey(inst.Kind, inst.Name, "")); l != nil {
			l.Health = boolHealth(ok)
			l.HealthAt = &now
			l.HealthNote = note
		}
		r.mu.Unlock()
	}

	r.mu.Lock()
	r.persistLocked()
	out := r.snapshotLocked()
	r.mu.Unlock()
	return out
}

func boolHealth(ok bool) string {
	if ok {
		return "ok"
	}
	return "fail"
}

func (r *connRegistry) snapshotLocked() []connLink {
	trimConns(r.links)
	out := make([]connLink, len(r.links))
	for i, l := range r.links {
		out[i] = *l
	}
	return out
}

func (r *connRegistry) list() []connLink {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.snapshotLocked()
}

func (r *connRegistry) clear() {
	r.mu.Lock()
	r.links = nil
	store.WriteJSON(autoscanConnsRel, connFile{Links: nil})
	r.mu.Unlock()
}

// runProber periodically re-checks every discovered arr so a link that has dropped
// (arr stopped, network down, key revoked) is reflected without waiting for a webhook.
func (r *connRegistry) runProber() {
	// small initial delay so discovery/executor are ready after boot
	time.Sleep(20 * time.Second)
	for {
		r.probeAll()
		time.Sleep(connProbeEvery)
	}
}
