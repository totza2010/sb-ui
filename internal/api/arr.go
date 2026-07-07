package api

import (
	"context"
	"encoding/json"
	"net/http"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"sb-ui/internal/executor"
	"sb-ui/internal/inventory"
)

// Unified *arr library: aggregate every Sonarr/Radarr instance into one list,
// grouping the same title (by tvdbId/tmdbId) across instances so a series held by
// 4 Sonarr instances shows once, with each instance's copy/files listed. Each
// instance is reached over the Docker bridge using the ApiKey from its config.xml.

type arrInstance struct {
	Kind    string // sonarr | radarr | prowlarr | whisparr
	Name    string // instance / container name
	IP      string // container IP on the docker network
	Port    string
	APIKey  string
	URLBase string
	WebURL  string // public URL from the container's Traefik Host rule (remote mode)
}

var xmlTagRE = func(tag string) *regexp.Regexp {
	return regexp.MustCompile(`(?s)<` + tag + `>\s*(.*?)\s*</` + tag + `>`)
}

func xmlTag(content, tag string) string {
	if m := xmlTagRE(tag).FindStringSubmatch(content); m != nil {
		return strings.TrimSpace(m[1])
	}
	return ""
}

// containerIP returns a container's IP on its (first) docker network, or "".
func containerIP(name string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	rc, out, _ := executor.Get().Run(ctx, []string{
		"docker", "inspect", "-f",
		`{{range .NetworkSettings.Networks}}{{.IPAddress}} {{end}}`, name,
	}, "")
	if rc != 0 {
		return ""
	}
	for _, f := range strings.Fields(out) {
		if f != "" {
			return f
		}
	}
	return ""
}

// traefikHostRE pulls the hostname out of a Traefik `Host(`x.domain`)` router rule.
var traefikHostRE = regexp.MustCompile("Host\\(`([^`]+)`\\)")

// plexVideoExtRE detects a media file path (so we scan its folder, not the file —
// Plex's targeted refresh works at directory granularity, like autoplow).
var plexVideoExtRE = regexp.MustCompile(`(?i)\.(mkv|mp4|avi|m4v|ts|mov|wmv|flv|webm|mpg|mpeg|m2ts|iso)$`)

// containerTsdURL returns a container's tailnet URL if it's exposed via tsdproxy
// (docker provider): label tsdproxy.enable=true, hostname from tsdproxy.name (or the
// container name). Mirrors containerWebHost but for the Tailscale proxy.
func containerTsdURL(name string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	rc, out, _ := executor.Get().Run(ctx, []string{
		"docker", "inspect", "-f",
		`{{index .Config.Labels "tsdproxy.enable"}}|{{index .Config.Labels "tsdproxy.name"}}`, name,
	}, "")
	if rc != 0 {
		return ""
	}
	parts := strings.SplitN(strings.TrimSpace(out), "|", 2)
	if len(parts) == 0 || !strings.EqualFold(parts[0], "true") {
		return ""
	}
	host := name
	if len(parts) == 2 && parts[1] != "" && parts[1] != "<no value>" {
		host = parts[1]
	}
	suffix := tailnetSuffix()
	if suffix == "" {
		return ""
	}
	return "https://" + host + "." + suffix
}

// containerWebHost returns a container's public hostname as actually served by
// Traefik — read straight from its router-rule label. This honours any subdomain
// customisation in the inventory (e.g. sonarr-ai) without re-rendering Jinja.
func containerWebHost(name string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	rc, out, _ := executor.Get().Run(ctx, []string{
		"docker", "inspect", "-f",
		`{{range .Config.Labels}}{{println .}}{{end}}`, name,
	}, "")
	if rc != 0 {
		return ""
	}
	if m := traefikHostRE.FindStringSubmatch(out); m != nil {
		return m[1]
	}
	return ""
}

var (
	arrCacheMu  sync.Mutex
	arrCacheVal []arrInstance
	arrCacheTS  time.Time
)

const arrCacheTTL = 5 * time.Minute // instances rarely change; avoids re-discovery on every file expand

// arrInstancesCached returns the discovered instances, rebuilding at most every
// arrCacheTTL — discovery does a config.xml read + docker inspect per instance, so
// caching keeps the library load and every file drill-down snappy.
func arrInstancesCached() []arrInstance {
	arrCacheMu.Lock()
	defer arrCacheMu.Unlock()
	if arrCacheVal != nil && time.Since(arrCacheTS) < arrCacheTTL {
		return arrCacheVal
	}
	arrCacheVal = arrInstances()
	arrCacheTS = time.Now()
	return arrCacheVal
}

// arrInstances discovers every running Sonarr/Radarr instance with a readable
// config.xml + reachable container.
func arrInstances() []arrInstance {
	return discoverArrApps(map[string]string{"sonarr": "8989", "radarr": "7878"})
}

// discoverArrApps discovers every instance of the given *arr-family apps (kind →
// default port) by reading each one's config.xml (ApiKey/Port/UrlBase) and its
// container IP. Parallel per instance. Works for sonarr/radarr/prowlarr/whisparr —
// they all share the *arr config.xml + v3 API shape.
func discoverArrApps(defPort map[string]string) []arrInstance {
	type cand struct{ kind, name, path string }
	var cands []cand
	for kind := range defPort {
		for _, ap := range inventory.ResolveAppdata(kind) {
			cands = append(cands, cand{kind, ap.Instance, ap.Path})
		}
	}

	out := make([]arrInstance, len(cands))
	var wg sync.WaitGroup
	for i, c := range cands {
		wg.Add(1)
		go func(i int, c cand) {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			content, err := executor.Get().ReadFile(ctx, c.path+"/config.xml")
			cancel()
			if err != nil {
				return
			}
			key := xmlTag(content, "ApiKey")
			if key == "" {
				return
			}
			ip := containerIP(c.name)
			if ip == "" {
				return
			}
			port := xmlTag(content, "Port")
			if port == "" {
				port = defPort[c.kind]
			}
			// Only needed for remote mode (where the docker IP isn't routable from
			// the sb-ui process); skip the extra inspect when running on the host.
			// Prefer the Traefik public URL, fall back to the tsdproxy tailnet URL.
			web := ""
			if _, local := executor.Get().(executor.LocalExecutor); !local {
				if h := containerWebHost(c.name); h != "" {
					web = "https://" + h
				} else if u := containerTsdURL(c.name); u != "" {
					web = u
				}
			}
			out[i] = arrInstance{
				Kind: c.kind, Name: c.name, IP: ip, Port: port,
				APIKey: key, URLBase: strings.TrimRight(xmlTag(content, "UrlBase"), "/"),
				WebURL: web,
			}
		}(i, c)
	}
	wg.Wait()

	res := out[:0]
	for _, in := range out {
		if in.APIKey != "" {
			res = append(res, in)
		}
	}
	return res
}

// arrSem bounds concurrent *arr API calls so a burst (library fan-out + hover
// prefetch) never floods SSH sessions / the arr backends.
var arrSem = make(chan struct{}, 6)

// ── Plex availability (match by the tvdb-/tmdb- id embedded in the path) ────────
// arr root folders (TV-UHD, TV-AI, …) and Plex library roots (tvuhd, …) don't line
// up, but both paths carry "{tvdb-N}"/"{tmdb-N}" and our items are keyed by the same
// id — so we match on that id rather than the full path.

var (
	// matches "tvdb-368166", "tvdb://368166" (Plex Guid), etc.
	plexTvdbRE = regexp.MustCompile(`(?i)tvdb[-:/]+(\d+)`)
	plexTmdbRE = regexp.MustCompile(`(?i)tmdb[-:/]+(\d+)`)
)

type plexIDSet struct {
	Tvdb    map[string]bool
	Tmdb    map[string]bool
	ShowKey map[string]string // tvdbId → Plex show ratingKey (for episode-level checks)
}

var (
	plexIDMu  sync.Mutex
	plexIDVal *plexIDSet
	plexIDTS  time.Time
)

const plexIDTTL = 15 * time.Minute

func plexIDsCached() plexIDSet {
	plexIDMu.Lock()
	defer plexIDMu.Unlock()
	if plexIDVal != nil && time.Since(plexIDTS) < plexIDTTL {
		return *plexIDVal
	}
	v := plexMediaIDs()
	plexIDVal = &v
	plexIDTS = time.Now()
	return v
}

func resetPlexDirs() { // called from putOptions when Plex config changes
	plexIDMu.Lock()
	plexIDVal = nil
	plexIDMu.Unlock()
}

func addPlexIDs(set *plexIDSet, s string) {
	if m := plexTvdbRE.FindStringSubmatch(s); m != nil {
		set.Tvdb[m[1]] = true
	}
	if m := plexTmdbRE.FindStringSubmatch(s); m != nil {
		set.Tmdb[m[1]] = true
	}
}

// arrPlexRefresh triggers a targeted Plex scan of one arr path (folder or file).
// The arr path is mapped to its Plex equivalent first, then the matching section is
// scanned with ?path= — Plex picks up just that path (autoplow-style).
func arrPlexRefresh(w http.ResponseWriter, req *http.Request) {
	var b struct {
		Path string `json:"path"`
	}
	_ = json.NewDecoder(req.Body).Decode(&b)
	b.Path = strings.TrimSpace(b.Path)
	if b.Path == "" {
		http.Error(w, "path required", http.StatusBadRequest)
		return
	}
	cfg := loadOptions().Plex
	if cfg.URL == "" {
		http.Error(w, "Plex not configured", http.StatusBadRequest)
		return
	}
	plexPath := mapArrPath(b.Path)
	// Plex scans at directory granularity — for a file path, refresh its folder
	// (autoplow does the same). The folder rescan picks up that specific file.
	if plexVideoExtRE.MatchString(plexPath) {
		plexPath = path.Dir(plexPath)
	}
	secID, _, ok := plexSectionForPath(cfg, plexPath)
	if !ok {
		http.Error(w, "no Plex section matches "+plexPath+" — add a path mapping", http.StatusBadRequest)
		return
	}
	if err := plexRefreshPath(cfg, secID, plexPath); err != nil {
		http.Error(w, "Plex refresh failed: "+err.Error(), http.StatusBadGateway)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "section": secID, "path": plexPath})
}

func itemInPlex(ids plexIDSet, it *arrItem) bool {
	if it.Kind == "sonarr" {
		return ids.Tvdb[it.Key]
	}
	return ids.Tmdb[it.Key]
}

// arrPathmapSuggest lists arr root folders + Plex section roots so the user can
// pair mismatched roots (e.g. /Media/TV-UHD → /Media/tvuhd) into path mappings.
func arrPathmapSuggest(w http.ResponseWriter, _ *http.Request) {
	arrRoots := map[string]bool{}
	for _, inst := range arrInstancesCached() {
		ctx, cancel := arrCtx()
		if inst.Kind == "sonarr" {
			rfs, _, err := sonarrClient(inst).RootFolderAPI.ListRootFolder(ctx).Execute()
			if err == nil {
				for _, r := range rfs {
					if r.GetPath() != "" {
						arrRoots[r.GetPath()] = true
					}
				}
			}
		} else {
			rfs, _, err := radarrClient(inst).RootFolderAPI.ListRootFolder(ctx).Execute()
			if err == nil {
				for _, r := range rfs {
					if r.GetPath() != "" {
						arrRoots[r.GetPath()] = true
					}
				}
			}
		}
		cancel()
	}

	plexRoots := map[string]bool{}
	for _, s := range plexSections(loadOptions().Plex) {
		for _, loc := range s.Locations {
			if loc != "" {
				plexRoots[loc] = true
			}
		}
	}

	keys := func(m map[string]bool) []string {
		out := make([]string, 0, len(m))
		for k := range m {
			out = append(out, k)
		}
		sort.Strings(out)
		return out
	}
	writeJSON(w, http.StatusOK, map[string]any{"arr_roots": keys(arrRoots), "plex_roots": keys(plexRoots)})
}

// arrPlexDebug surfaces the Plex id index + arr keys + how many arr titles match,
// so a count/format mismatch is obvious. Bypasses the cache (always fresh).
func arrPlexDebug(w http.ResponseWriter, _ *http.Request) {
	cfg := loadOptions().Plex

	// Per-section diagnostic via plexgo: section type + how many ids it yields.
	var diag []map[string]any
	for _, s := range plexSections(cfg) {
		diag = append(diag, map[string]any{
			"section": s.Title, "type": s.Type, "locations": s.Locations,
		})
	}

	ids := plexMediaIDs() // fresh, no cache — reflects current code immediately
	sample := func(m map[string]bool, n int) []string {
		out := make([]string, 0, n)
		for k := range m {
			out = append(out, k)
			if len(out) >= n {
				break
			}
		}
		return out
	}

	var arrTvdb, arrTmdb, matched []string
	tvMatch, mvMatch := 0, 0
	for _, inst := range arrInstancesCached() {
		ctx, cancel := arrCtx()
		if inst.Kind == "sonarr" {
			series, _, err := sonarrClient(inst).SeriesAPI.ListSeries(ctx).Execute()
			cancel()
			if err != nil {
				continue
			}
			for _, s := range series {
				k := strconv.Itoa(int(s.GetTvdbId()))
				if len(arrTvdb) < 8 {
					arrTvdb = append(arrTvdb, k)
				}
				if ids.Tvdb[k] {
					tvMatch++
					if len(matched) < 10 {
						matched = append(matched, "tvdb:"+k)
					}
				}
			}
		} else {
			movies, _, err := radarrClient(inst).MovieAPI.ListMovie(ctx).Execute()
			cancel()
			if err != nil {
				continue
			}
			for _, m := range movies {
				k := strconv.Itoa(int(m.GetTmdbId()))
				if len(arrTmdb) < 8 {
					arrTmdb = append(arrTmdb, k)
				}
				if ids.Tmdb[k] {
					mvMatch++
					if len(matched) < 10 {
						matched = append(matched, "tmdb:"+k)
					}
				}
			}
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"plex_url_set":     cfg.URL != "",
		"plex_tvdb_count":  len(ids.Tvdb),
		"plex_tmdb_count":  len(ids.Tmdb),
		"sample_plex_tvdb": sample(ids.Tvdb, 8),
		"sample_plex_tmdb": sample(ids.Tmdb, 8),
		"sample_arr_tvdb":  arrTvdb,
		"sample_arr_tmdb":  arrTmdb,
		"matched_series":   tvMatch,
		"matched_movies":   mvMatch,
		"matched_sample":   matched,
		"sections_diag":    diag,
	})
}

// inPlex reports whether an arr folder path is covered by the Plex path index.
func inPlex(dirs map[string]bool, folder string) bool {
	if folder == "" || len(dirs) == 0 {
		return false
	}
	return dirs[strings.TrimRight(folder, "/")]
}

func clearArrFileCache(kind, instance string, id int) {
	ck := kind + "|" + instance + "|" + strconv.Itoa(id)
	arrFileMu.Lock()
	delete(arrFileCache, ck)
	arrFileMu.Unlock()
}

// arrCommand runs an action (refresh/search/rename/monitor/unmonitor) on one item
// in one instance — the working buttons from the detail view.
func arrCommand(w http.ResponseWriter, req *http.Request) {
	var b struct {
		Kind      string `json:"kind"`
		Instance  string `json:"instance"`
		ID        int    `json:"id"`
		Action    string `json:"action"`
		EpisodeID int    `json:"episode_id"`
		FileID    int    `json:"file_id"`
		Season    *int   `json:"season"`
	}
	if err := json.NewDecoder(req.Body).Decode(&b); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	inst := arrInstanceByName(b.Kind, b.Instance)
	if inst == nil {
		http.Error(w, "instance not found", http.StatusNotFound)
		return
	}
	id := strconv.Itoa(b.ID)
	sonarr := b.Kind == "sonarr"

	var ok bool
	var out string
	switch b.Action {
	case "refresh":
		if sonarr {
			ok, out = arrSendRaw(*inst, "POST", "command", `{"name":"RefreshSeries","seriesId":`+id+`}`)
		} else {
			ok, out = arrSendRaw(*inst, "POST", "command", `{"name":"RefreshMovie","movieIds":[`+id+`]}`)
		}
	case "search":
		if sonarr {
			ok, out = arrSendRaw(*inst, "POST", "command", `{"name":"SeriesSearch","seriesId":`+id+`}`)
		} else {
			ok, out = arrSendRaw(*inst, "POST", "command", `{"name":"MoviesSearch","movieIds":[`+id+`]}`)
		}
	case "rename":
		if sonarr {
			ok, out = arrSendRaw(*inst, "POST", "command", `{"name":"RenameSeries","seriesIds":[`+id+`]}`)
		} else {
			ok, out = arrSendRaw(*inst, "POST", "command", `{"name":"RenameMovie","movieIds":[`+id+`]}`)
		}
		clearArrFileCache(b.Kind, b.Instance, b.ID)
	case "monitor", "unmonitor":
		obj := "series/" + id
		if !sonarr {
			obj = "movie/" + id
		}
		gok, gout := arrGetRaw(*inst, obj)
		if !gok {
			http.Error(w, "fetch failed", http.StatusBadGateway)
			return
		}
		var m map[string]any
		if json.Unmarshal([]byte(gout), &m) != nil {
			http.Error(w, "parse failed", http.StatusBadGateway)
			return
		}
		m["monitored"] = b.Action == "monitor"
		body, _ := json.Marshal(m)
		ok, out = arrSendRaw(*inst, "PUT", obj, string(body))
	case "episodeSearch": // sonarr only
		ok, out = arrSendRaw(*inst, "POST", "command", `{"name":"EpisodeSearch","episodeIds":[`+strconv.Itoa(b.EpisodeID)+`]}`)
	case "seasonSearch": // sonarr only
		if b.Season == nil {
			http.Error(w, "season required", http.StatusBadRequest)
			return
		}
		ok, out = arrSendRaw(*inst, "POST", "command", `{"name":"SeasonSearch","seriesId":`+id+`,"seasonNumber":`+strconv.Itoa(*b.Season)+`}`)
	case "deleteFile":
		ep := "episodefile/" + strconv.Itoa(b.FileID)
		if !sonarr {
			ep = "moviefile/" + strconv.Itoa(b.FileID)
		}
		ok, out = arrSendRaw(*inst, "DELETE", ep, "")
		clearArrFileCache(b.Kind, b.Instance, b.ID)
	case "seasonMonitor", "seasonUnmonitor": // sonarr only
		if b.Season == nil {
			http.Error(w, "season required", http.StatusBadRequest)
			return
		}
		gok, gout := arrGetRaw(*inst, "series/"+id)
		if !gok {
			http.Error(w, "fetch failed", http.StatusBadGateway)
			return
		}
		var m map[string]any
		if json.Unmarshal([]byte(gout), &m) != nil {
			http.Error(w, "parse failed", http.StatusBadGateway)
			return
		}
		if seasons, ok := m["seasons"].([]any); ok {
			for _, s := range seasons {
				if sm, ok := s.(map[string]any); ok {
					if n, ok := sm["seasonNumber"].(float64); ok && int(n) == *b.Season {
						sm["monitored"] = b.Action == "seasonMonitor"
					}
				}
			}
		}
		body, _ := json.Marshal(m)
		ok, out = arrSendRaw(*inst, "PUT", "series/"+id, string(body))
	default:
		http.Error(w, "unknown action", http.StatusBadRequest)
		return
	}
	if !ok {
		http.Error(w, "command failed: "+strings.TrimSpace(out), http.StatusBadGateway)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// arrProfiles maps qualityProfileId → name for one instance.
func arrProfiles(inst arrInstance) map[int]string {
	m := map[int]string{}
	arrSem <- struct{}{}
	defer func() { <-arrSem }()
	ctx, cancel := arrCtx()
	defer cancel()
	if inst.Kind == "sonarr" {
		profs, _, err := sonarrClient(inst).QualityProfileAPI.ListQualityProfile(ctx).Execute()
		if err != nil {
			return m
		}
		for _, p := range profs {
			m[int(p.GetId())] = p.GetName()
		}
	} else {
		profs, _, err := radarrClient(inst).QualityProfileAPI.ListQualityProfile(ctx).Execute()
		if err != nil {
			return m
		}
		for _, p := range profs {
			m[int(p.GetId())] = p.GetName()
		}
	}
	return m
}

type arrCopy struct {
	Instance string `json:"instance"`
	ItemID   int    `json:"item_id"` // seriesId / movieId within that instance
	Profile  string `json:"profile"`
	Files    int    `json:"files"`
	Size     int64  `json:"size"`
	HasFile  bool   `json:"has_file"`
	InPlex   bool   `json:"in_plex"`          // this copy's folder is in Plex
	Folder   string `json:"folder,omitempty"` // arr folder (for targeted Plex refresh)
}

type arrItem struct {
	Kind      string    `json:"kind"` // sonarr | radarr
	Key       string    `json:"key"`  // tvdbId / tmdbId
	Title     string    `json:"title"`
	Year      int       `json:"year"`
	Poster    string    `json:"poster"`    // external image URL (browser-loadable)
	Overview  string    `json:"overview"`  //
	Status    string    `json:"status"`    // continuing/ended | announced/released
	Network   string    `json:"network"`   // network (sonarr) / studio (radarr)
	Runtime   int       `json:"runtime"`   // minutes
	Rating    float64   `json:"rating"`    //
	Monitored bool      `json:"monitored"` //
	Genres    []string  `json:"genres"`    //
	Seasons   int       `json:"seasons"`   // sonarr season count
	Episodes  int       `json:"episodes"`  // sonarr episode count (have-file)
	InPlex    bool      `json:"in_plex"`   // any copy is in Plex
	Copies    []arrCopy `json:"copies"`
}

func arrInstanceByName(kind, name string) *arrInstance {
	for _, i := range arrInstancesCached() {
		if i.Kind == kind && i.Name == name {
			return &i
		}
	}
	return nil
}

// arrLibrary fans out to every instance and returns titles grouped across them.
func arrLibrary(w http.ResponseWriter, _ *http.Request) {
	insts := arrInstancesCached()
	var mu sync.Mutex
	groups := map[string]*arrItem{}
	// add merges a copy into its group, filling group metadata the first time.
	add := func(meta arrItem, c arrCopy) {
		if meta.Key == "0" || meta.Key == "" {
			meta.Key = meta.Kind + "-" + meta.Title // fall back to title
		}
		key := meta.Kind + ":" + meta.Key
		mu.Lock()
		g := groups[key]
		if g == nil {
			m := meta
			groups[key] = &m
			g = &m
		} else if g.Poster == "" && meta.Poster != "" {
			g.Poster, g.Overview, g.Status = meta.Poster, meta.Overview, meta.Status
			g.Network, g.Runtime, g.Rating, g.Genres = meta.Network, meta.Runtime, meta.Rating, meta.Genres
		}
		g.Copies = append(g.Copies, c)
		mu.Unlock()
	}

	var wg sync.WaitGroup
	for _, inst := range insts {
		wg.Add(1)
		go func(inst arrInstance) {
			defer wg.Done()
			profiles := arrProfiles(inst)
			ctx, cancel := arrCtx()
			defer cancel()
			arrSem <- struct{}{}
			if inst.Kind == "sonarr" {
				series, _, err := sonarrClient(inst).SeriesAPI.ListSeries(ctx).Execute()
				<-arrSem
				if err != nil {
					return
				}
				for i := range series {
					s := series[i]
					st := s.GetStatistics()
					rat := s.GetRatings()
					add(arrItem{
						Kind: "sonarr", Key: strconv.Itoa(int(s.GetTvdbId())), Title: s.GetTitle(), Year: int(s.GetYear()),
						Poster: sonarrPoster(s.GetImages()), Overview: s.GetOverview(), Status: string(s.GetStatus()),
						Network: s.GetNetwork(), Runtime: int(s.GetRuntime()), Rating: rat.GetValue(),
						Monitored: s.GetMonitored(), Genres: s.GetGenres(),
						Seasons: int(st.GetSeasonCount()), Episodes: int(st.GetEpisodeCount()),
					}, arrCopy{
						Instance: inst.Name, ItemID: int(s.GetId()), Profile: profiles[int(s.GetQualityProfileId())],
						Files: int(st.GetEpisodeFileCount()), Size: st.GetSizeOnDisk(),
						HasFile: st.GetEpisodeFileCount() > 0, Folder: s.GetPath(),
					})
				}
			} else {
				movies, _, err := radarrClient(inst).MovieAPI.ListMovie(ctx).Execute()
				<-arrSem
				if err != nil {
					return
				}
				for i := range movies {
					m := movies[i]
					files := 0
					if m.GetHasFile() {
						files = 1
					}
					rat := m.GetRatings()
					tmdb := rat.GetTmdb()
					add(arrItem{
						Kind: "radarr", Key: strconv.Itoa(int(m.GetTmdbId())), Title: m.GetTitle(), Year: int(m.GetYear()),
						Poster: radarrPoster(m.GetImages()), Overview: m.GetOverview(), Status: string(m.GetStatus()),
						Network: m.GetStudio(), Runtime: int(m.GetRuntime()), Rating: tmdb.GetValue(),
						Monitored: m.GetMonitored(), Genres: m.GetGenres(),
					}, arrCopy{
						Instance: inst.Name, ItemID: int(m.GetId()), Profile: profiles[int(m.GetQualityProfileId())],
						Files: files, Size: m.GetSizeOnDisk(), HasFile: m.GetHasFile(), Folder: m.GetPath(),
					})
				}
			}
		}(inst)
	}
	wg.Wait()

	pids := plexIDsCached() // match in-Plex by the tvdb/tmdb id (paths roots differ)
	items := make([]*arrItem, 0, len(groups))
	for _, g := range groups {
		sort.Slice(g.Copies, func(i, j int) bool { return g.Copies[i].Instance < g.Copies[j].Instance })
		g.InPlex = itemInPlex(pids, g)
		for i := range g.Copies {
			g.Copies[i].InPlex = g.InPlex
		}
		items = append(items, g)
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].Kind != items[j].Kind {
			return items[i].Kind < items[j].Kind
		}
		return strings.ToLower(items[i].Title) < strings.ToLower(items[j].Title)
	})

	names := make([]map[string]string, 0, len(insts))
	for _, i := range insts {
		names = append(names, map[string]string{"kind": i.Kind, "name": i.Name})
	}

	// Pre-warm file caches in the background (bounded by arrSem) so expands open
	// instantly. Response is sent first — this never blocks the page.
	go prewarmArrFiles(items, insts)

	writeJSON(w, http.StatusOK, map[string]any{"items": items, "instances": names})
}

func prewarmArrFiles(items []*arrItem, insts []arrInstance) {
	byName := map[string]arrInstance{}
	for _, in := range insts {
		byName[in.Kind+"|"+in.Name] = in
	}
	n := 0
	for _, it := range items {
		for _, c := range it.Copies {
			if !c.HasFile {
				continue
			}
			inst, ok := byName[it.Kind+"|"+c.Instance]
			if !ok {
				continue
			}
			if n++; n > 1000 { // safety cap for huge libraries
				return
			}
			go fetchArrFiles(inst, it.Kind, strconv.Itoa(c.ItemID), it.Key)
		}
	}
}

type arrMedia struct {
	Resolution     string  `json:"resolution,omitempty"`
	VideoCodec     string  `json:"video_codec,omitempty"`
	DynamicRange   string  `json:"dynamic_range,omitempty"`
	AudioCodec     string  `json:"audio_codec,omitempty"`
	AudioChannels  float64 `json:"audio_channels,omitempty"`
	AudioLanguages string  `json:"audio_languages,omitempty"`
	Subtitles      string  `json:"subtitles,omitempty"`
	Runtime        string  `json:"runtime,omitempty"`
}

type arrFile struct {
	Season       *int      `json:"season,omitempty"`
	Episode      *int      `json:"episode,omitempty"`
	EpisodeID    int       `json:"episode_id,omitempty"` // for per-episode search
	FileID       int       `json:"file_id,omitempty"`    // for delete (episodefile/moviefile id)
	Title        string    `json:"title,omitempty"`
	AirDate      string    `json:"air_date,omitempty"`
	Monitored    bool      `json:"monitored"`
	HasFile      bool      `json:"has_file"`
	InPlex       bool      `json:"in_plex"` // this file present in Plex (per-episode)
	Quality      string    `json:"quality,omitempty"`
	Size         int64     `json:"size"`
	Path         string    `json:"path,omitempty"`      // relative
	FullPath     string    `json:"full_path,omitempty"` // absolute
	ReleaseGroup string    `json:"release_group,omitempty"`
	Languages    string    `json:"languages,omitempty"`
	DateAdded    string    `json:"date_added,omitempty"`
	Media        *arrMedia `json:"media,omitempty"`
}

// Server-side cache of per-item file lists — a drill-down only hits the arr API
// once per arrFileTTL, so re-expands (and prefetch-then-click) are instant.
var (
	arrFileMu    sync.Mutex
	arrFileCache = map[string]arrFileEntry{}
)

type arrFileEntry struct {
	files []arrFile
	ts    time.Time
}

const arrFileTTL = 5 * time.Minute

func fetchArrFiles(inst arrInstance, kind, id, extID string) ([]arrFile, bool) {
	ck := kind + "|" + inst.Name + "|" + id
	arrFileMu.Lock()
	if e, ok := arrFileCache[ck]; ok && time.Since(e.ts) < arrFileTTL {
		arrFileMu.Unlock()
		return e.files, true
	}
	arrFileMu.Unlock()

	idN, _ := strconv.Atoi(id)
	arrSem <- struct{}{}
	ctx, cancel := arrCtx()
	var files []arrFile
	if kind == "sonarr" {
		// One call returns every episode + its file (incl. missing episodes) — the
		// data the Prismarr-style collapsible season/episode view needs.
		eps, _, err := sonarrClient(inst).EpisodeAPI.ListEpisode(ctx).SeriesId(int32(idN)).IncludeEpisodeFile(true).Execute()
		cancel()
		<-arrSem
		if err != nil {
			return nil, false
		}
		for i := range eps {
			e := eps[i]
			sn, en := int(e.GetSeasonNumber()), int(e.GetEpisodeNumber())
			af := arrFile{Season: &sn, Episode: &en, EpisodeID: int(e.GetId()), Title: e.GetTitle(), AirDate: e.GetAirDate(), Monitored: e.GetMonitored()}
			if e.GetHasFile() {
				ef := e.GetEpisodeFile()
				applySonarrFile(&af, ef)
			}
			files = append(files, af)
		}
		sort.Slice(files, func(i, j int) bool {
			if *files[i].Season != *files[j].Season {
				return *files[i].Season < *files[j].Season
			}
			return *files[i].Episode < *files[j].Episode
		})
	} else {
		raw, _, err := radarrClient(inst).MovieFileAPI.ListMovieFile(ctx).MovieId([]int32{int32(idN)}).Execute()
		cancel()
		<-arrSem
		if err != nil {
			return nil, false
		}
		for i := range raw {
			var af arrFile
			applyRadarrFile(&af, raw[i])
			files = append(files, af)
		}
	}

	// Per-file Plex presence: sonarr → basename match against the show's Plex
	// episodes; radarr → the movie's tmdb is in Plex.
	if extID != "" && loadOptions().Plex.URL != "" {
		if kind == "sonarr" {
			if bn := plexShowEpisodeBasenames(extID); len(bn) > 0 {
				for i := range files {
					if files[i].HasFile && bn[path.Base(files[i].Path)] {
						files[i].InPlex = true
					}
				}
			}
		} else if plexIDsCached().Tmdb[extID] {
			for i := range files {
				files[i].InPlex = true
			}
		}
	}

	arrFileMu.Lock()
	arrFileCache[ck] = arrFileEntry{files: files, ts: time.Now()}
	arrFileMu.Unlock()
	return files, true
}

// arrFiles drills into one item on one instance and lists its files (cached).
func arrFiles(w http.ResponseWriter, req *http.Request) {
	kind := req.URL.Query().Get("kind")
	name := req.URL.Query().Get("instance")
	id := req.URL.Query().Get("id")
	ext := req.URL.Query().Get("ext") // tvdb/tmdb id for per-file Plex check
	if id == "" {
		http.Error(w, "id required", http.StatusBadRequest)
		return
	}
	inst := arrInstanceByName(kind, name)
	if inst == nil {
		http.Error(w, "instance not found", http.StatusNotFound)
		return
	}
	files, ok := fetchArrFiles(*inst, kind, id, ext)
	if !ok {
		http.Error(w, "fetch failed", http.StatusBadGateway)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"files": files})
}
