package api

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"sb-ui/internal/inventory"

	plexgo "github.com/LukeHagar/plexgo"
)

// Integrations page: live connection status for every client library sb-ui talks
// to — the ones already wired into features, the ones not used yet (probed with a
// basic call so we can confirm they communicate), and a Plex bake-off between two
// libraries so we can pick the one that works best.

type connStat struct {
	Label string `json:"label"`
	Value int    `json:"value"`
}

// pathStat is one root folder with its content counts (series/episodes or movies).
type pathStat struct {
	Path  string     `json:"path"`
	Stats []connStat `json:"stats"`
}

type connStatus struct {
	Name        string     `json:"name"`
	BaseURL     string     `json:"base_url"`
	OK          bool       `json:"ok"`
	Version     string     `json:"version,omitempty"`
	Detail      string     `json:"detail,omitempty"` // instance name / machine id
	Error       string     `json:"error,omitempty"`
	LatencyMS   int64      `json:"latency_ms"`
	Recommended bool       `json:"recommended,omitempty"`
	Primary     bool       `json:"primary,omitempty"` // the default instance (e.g. Seerr for requests)
	Stats       []connStat `json:"stats,omitempty"`      // instance totals (series/episodes/movies)
	PathStats   []pathStat `json:"path_stats,omitempty"` // per-root-folder breakdown
}

type integrationGroup struct {
	Key        string       `json:"key"`
	Label      string       `json:"label"`
	Library    string       `json:"library"` // the Go module
	Used       bool         `json:"used"`    // already wired into sb-ui features
	Configured bool          `json:"configured"`
	Note       string        `json:"note,omitempty"`
	Instances  []connStatus  `json:"instances"`
	Libraries  []plexLibInfo `json:"libraries,omitempty"` // Plex only: library breakdown
}

func trimErr(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 240 {
		s = s[:240]
	}
	return s
}

// pathGrouper accumulates per-root-folder counts (up to two stat labels) and emits
// them sorted by path.
type pathGrouper struct {
	order      []string
	by         map[string][2]int
	lbl1, lbl2 string
}

func newPathGrouper() *pathGrouper { return &pathGrouper{by: map[string][2]int{}} }

func (g *pathGrouper) add(path, l1 string, v1 int, l2 string, v2 int) {
	if path == "" {
		return
	}
	g.lbl1, g.lbl2 = l1, l2
	cur, ok := g.by[path]
	if !ok {
		g.order = append(g.order, path)
	}
	cur[0] += v1
	cur[1] += v2
	g.by[path] = cur
}

func (g *pathGrouper) result() []pathStat {
	sort.Strings(g.order)
	out := make([]pathStat, 0, len(g.order))
	for _, p := range g.order {
		c := g.by[p]
		stats := []connStat{{Label: g.lbl1, Value: c[0]}}
		if g.lbl2 != "" {
			stats = append(stats, connStat{Label: g.lbl2, Value: c[1]})
		}
		out = append(out, pathStat{Path: p, Stats: stats})
	}
	return out
}

// probeArr runs a SystemStatus call against one *arr-family instance (the same
// devopsarr client family for all four apps) and reports version + reachability.
func probeArr(inst arrInstance) connStatus {
	cs := connStatus{Name: inst.Name, BaseURL: arrBaseURL(inst)}
	start := time.Now()
	ctx, cancel := arrCtx()
	defer cancel()
	switch inst.Kind {
	case "sonarr":
		cl := sonarrClient(inst)
		st, _, e := cl.SystemAPI.GetSystemStatus(ctx).Execute()
		cs.LatencyMS = time.Since(start).Milliseconds()
		if e != nil {
			cs.Error = trimErr(e.Error())
			break
		}
		cs.OK, cs.Version, cs.Detail = true, st.GetVersion(), st.GetInstanceName()
		if series, _, se := cl.SeriesAPI.ListSeries(ctx).Execute(); se == nil {
			g := newPathGrouper()
			totEps := 0
			for i := range series {
				stt := series[i].GetStatistics()
				eps := int(stt.GetEpisodeFileCount())
				totEps += eps
				g.add(series[i].GetRootFolderPath(), "series", 1, "episodes", eps)
			}
			cs.Stats = []connStat{{Label: "series", Value: len(series)}, {Label: "episodes", Value: totEps}}
			cs.PathStats = g.result()
		}
	case "radarr":
		cl := radarrClient(inst)
		st, _, e := cl.SystemAPI.GetSystemStatus(ctx).Execute()
		cs.LatencyMS = time.Since(start).Milliseconds()
		if e != nil {
			cs.Error = trimErr(e.Error())
			break
		}
		cs.OK, cs.Version, cs.Detail = true, st.GetVersion(), st.GetInstanceName()
		if movies, _, me := cl.MovieAPI.ListMovie(ctx).Execute(); me == nil {
			g := newPathGrouper()
			for i := range movies {
				g.add(movies[i].GetRootFolderPath(), "movies", 1, "", 0)
			}
			cs.Stats = []connStat{{Label: "movies", Value: len(movies)}}
			cs.PathStats = g.result()
		}
	case "prowlarr":
		cl := prowlarrClient(inst)
		st, _, e := cl.SystemAPI.GetSystemStatus(ctx).Execute()
		cs.LatencyMS = time.Since(start).Milliseconds()
		if e != nil {
			cs.Error = trimErr(e.Error())
			break
		}
		cs.OK, cs.Version, cs.Detail = true, st.GetVersion(), st.GetInstanceName()
		if idx, _, ie := cl.IndexerAPI.ListIndexer(ctx).Execute(); ie == nil {
			cs.Stats = []connStat{{Label: "indexers", Value: len(idx)}}
		}
	case "whisparr":
		cl := whisparrClient(inst)
		st, _, e := cl.SystemAPI.GetSystemStatus(ctx).Execute()
		cs.LatencyMS = time.Since(start).Milliseconds()
		if e != nil {
			cs.Error = trimErr(e.Error())
			break
		}
		cs.OK, cs.Version, cs.Detail = true, st.GetVersion(), st.GetInstanceName()
		if movies, _, me := cl.MovieAPI.ListMovie(ctx).Execute(); me == nil {
			cs.Stats = []connStat{{Label: "items", Value: len(movies)}}
		}
	}
	return cs
}

// arrGroup builds an integration group for one *arr-family app.
func arrGroup(key, label string, defPort string, used bool, insts []arrInstance) integrationGroup {
	g := integrationGroup{
		Key: key, Label: label, Library: "github.com/devopsarr/" + key + "-go", Used: used,
	}
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, in := range insts {
		wg.Add(1)
		go func(in arrInstance) {
			defer wg.Done()
			cs := probeArr(in)
			mu.Lock()
			g.Instances = append(g.Instances, cs)
			mu.Unlock()
		}(in)
	}
	wg.Wait()
	sort.Slice(g.Instances, func(i, j int) bool { return g.Instances[i].Name < g.Instances[j].Name })
	g.Configured = len(g.Instances) > 0
	if !g.Configured {
		g.Note = "No instance discovered on this host."
	}
	return g
}

// containerNameFor returns the docker container/instance name for a role (via the
// inventory, same source the *arr cards use), so Plex/Seerr show their container name
// instead of the Go client name. Falls back to the URL's leading host label.
func containerNameFor(rawURL string, tags ...string) string {
	for _, t := range tags {
		if aps := inventory.ResolveAppdata(t); len(aps) > 0 && aps[0].Instance != "" {
			return aps[0].Instance
		}
	}
	if u, err := url.Parse(rawURL); err == nil {
		if h := u.Hostname(); h != "" {
			if i := strings.Index(h, "."); i > 0 {
				return h[:i] // seerr.privox.top -> seerr
			}
			return h
		}
	}
	return "instance"
}

// probePlexGo tests the configured Plex via plexgo (LukeHagar) — General.GetIdentity.
func probePlexGo(cfg plexConfig) connStatus {
	cs := connStatus{Name: containerNameFor(cfg.URL, "plex"), BaseURL: cfg.URL}
	start := time.Now()
	s := plexgo.New(plexgo.WithServerURL(cfg.URL), plexgo.WithSecurity(cfg.Token), plexgo.WithClient(arrHTTP))
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	res, err := s.General.GetIdentity(ctx)
	cs.LatencyMS = time.Since(start).Milliseconds()
	if err != nil {
		cs.Error = trimErr(err.Error())
		return cs
	}
	if res != nil && res.Object != nil && res.Object.MediaContainer != nil {
		if v := res.Object.MediaContainer.Version; v != nil {
			cs.Version = *v
		}
		if m := res.Object.MediaContainer.MachineIdentifier; m != nil {
			cs.Detail = *m
		}
	}
	cs.OK = res != nil && res.StatusCode == http.StatusOK
	cs.Recommended = cs.OK
	return cs
}

// plexGroup reports Plex connectivity. The whole Plex layer now runs on plexgo
// (it won the bake-off), so this probes the in-use client.
func plexGroup(cfg plexConfig) integrationGroup {
	g := integrationGroup{Key: "plex", Label: "Plex", Library: "github.com/LukeHagar/plexgo", Used: true}
	if cfg.URL == "" {
		g.Note = "Plex not configured (Settings → Plex)."
		return g
	}
	g.Configured = true
	g.Instances = []connStatus{probePlexGo(cfg)}
	// Pull the library breakdown (names + counts) — this also surfaces any data-fetch
	// error (e.g. auth that GetIdentity tolerates but GetSections rejects).
	libs, err := plexLibraries(cfg)
	if err != nil {
		g.Note = "Data fetch failed: " + trimErr(err.Error())
	} else {
		g.Libraries = libs
		movies, series := 0, 0
		for _, l := range libs {
			switch l.Type {
			case "movie":
				movies += l.Count
			case "show":
				series += l.Count
			}
		}
		g.Note = fmt.Sprintf("%d libraries · %d movies · %d series", len(libs), movies, series)
	}
	return g
}

// seerrGroup reports connectivity for every detected Jellyseerr/Overseerr/Seerr
// instance. Discovered containers awaiting an API key are shown as unconfigured.
func seerrGroup() integrationGroup {
	g := integrationGroup{Key: "seerr", Label: "Jellyseerr / Overseerr", Library: "github.com/devopsarr/seerr-go", Used: true}
	insts := mergedSeerrInstances()
	if len(insts) == 0 {
		g.Used = false
		g.Note = "No Jellyseerr/Overseerr detected — add one, then set its API key here."
		return g
	}
	g.Configured = true
	primaryName := primarySeerr().Name // the instance Discover requests are sent to
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, in := range insts {
		wg.Add(1)
		go func(in seerrConfig) {
			defer wg.Done()
			cs := connStatus{Name: in.Name, BaseURL: in.URL, Primary: in.Name != "" && in.Name == primaryName}
			if in.URL == "" {
				cs.Error = "needs URL + API key — click the gear to configure"
			} else if in.APIKey == "" {
				cs.Error = "URL detected — add its API key (click the gear)"
			} else {
				// /status is public (a wrong key still returns 200), so validate against
				// an authenticated endpoint — /request/count — which also gives us the
				// program-specific stats to show (requested / awaiting files / available).
				start := time.Now()
				var rc struct {
					Total      int `json:"total"`
					Pending    int `json:"pending"`
					Processing int `json:"processing"`
					Available  int `json:"available"`
				}
				err := seerrRawGET(in, "/request/count", &rc)
				cs.LatencyMS = time.Since(start).Milliseconds()
				if err != nil {
					cs.Error = trimErr(err.Error()) // 401 on a bad/missing API key
				} else {
					cs.OK = true
					if v, e := seerrStatus(in); e == nil {
						cs.Version = v
					}
					cs.Stats = []connStat{
						{Label: "requests", Value: rc.Total},
						{Label: "pending", Value: rc.Pending},
						{Label: "processing", Value: rc.Processing},
						{Label: "available", Value: rc.Available},
					}
				}
			}
			mu.Lock()
			g.Instances = append(g.Instances, cs)
			mu.Unlock()
		}(in)
	}
	wg.Wait()
	sort.Slice(g.Instances, func(i, j int) bool { // primary first, then by name
		if g.Instances[i].Primary != g.Instances[j].Primary {
			return g.Instances[i].Primary
		}
		return g.Instances[i].Name < g.Instances[j].Name
	})
	return g
}

// qbitGroup reports qBittorrent connectivity (the uploader's download-client lever).
func qbitGroup() integrationGroup {
	g := integrationGroup{Key: "qbit", Label: "qBittorrent", Library: "github.com/autobrr/go-qbittorrent", Used: true}
	full := resolveQbit(qbitConfig{}) // URL/user/pass from options (+ auto-discover)
	cs := connStatus{Name: "qbittorrent", BaseURL: full.URL}
	switch {
	case full.URL == "":
		g.Used = false
		cs.Error = "needs URL + WebUI login — click the gear to configure"
	case full.User == "":
		cs.Error = "URL detected — add the WebUI login (click the gear)"
	default:
		g.Configured = true
		start := time.Now()
		ver, stats, err := qbitProbe(full)
		cs.LatencyMS = time.Since(start).Milliseconds()
		if err != nil {
			cs.Error = trimErr(err.Error())
		} else {
			cs.OK, cs.Version, cs.Stats = true, ver, stats
		}
	}
	if full.URL != "" {
		g.Configured = true
	}
	g.Instances = []connStatus{cs}
	return g
}

// integrationsStatus reports live connection status for every client library.
func integrationsStatus(w http.ResponseWriter, _ *http.Request) {
	var groups []integrationGroup
	var mu sync.Mutex
	add := func(g integrationGroup) { mu.Lock(); groups = append(groups, g); mu.Unlock() }

	// Sonarr + Radarr (already wired into the Library) reuse the cached discovery.
	var sonarrI, radarrI []arrInstance
	for _, in := range arrInstancesCached() {
		if in.Kind == "sonarr" {
			sonarrI = append(sonarrI, in)
		} else if in.Kind == "radarr" {
			radarrI = append(radarrI, in)
		}
	}

	var wg sync.WaitGroup
	jobs := []func(){
		func() { add(arrGroup("sonarr", "Sonarr", "8989", true, sonarrI)) },
		func() { add(arrGroup("radarr", "Radarr", "7878", true, radarrI)) },
		func() { add(arrGroup("prowlarr", "Prowlarr", "9696", false, discoverArrApps(map[string]string{"prowlarr": "9696"}))) },
		func() { add(arrGroup("whisparr", "Whisparr", "6969", false, discoverArrApps(map[string]string{"whisparr": "6969"}))) },
		func() { add(plexGroup(loadOptions().Plex)) },
	}
	for _, j := range jobs {
		wg.Add(1)
		go func(j func()) { defer wg.Done(); j() }(j)
	}
	wg.Wait()

	groups = append(groups, seerrGroup())
	groups = append(groups, qbitGroup())

	order := map[string]int{"sonarr": 0, "radarr": 1, "prowlarr": 2, "whisparr": 3, "plex": 4, "seerr": 5, "qbit": 6}
	sort.Slice(groups, func(i, j int) bool { return order[groups[i].Key] < order[groups[j].Key] })

	// Never emit a nil slice (Go marshals nil → JSON null, which breaks .filter/.map).
	for i := range groups {
		if groups[i].Instances == nil {
			groups[i].Instances = []connStatus{}
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{"groups": groups})
}
