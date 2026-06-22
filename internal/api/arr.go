package api

import (
	"context"
	"encoding/json"
	"net/http"
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
	Kind    string // "sonarr" | "radarr"
	Name    string // instance / container name
	IP      string // container IP on the docker network
	Port    string
	APIKey  string
	URLBase string
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
// config.xml + reachable container. Discovery is parallel per instance.
func arrInstances() []arrInstance {
	defPort := map[string]string{"sonarr": "8989", "radarr": "7878"}
	type cand struct{ kind, name, path string }
	var cands []cand
	for _, kind := range []string{"sonarr", "radarr"} {
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
			out[i] = arrInstance{
				Kind: c.kind, Name: c.name, IP: ip, Port: port,
				APIKey: key, URLBase: strings.TrimRight(xmlTag(content, "UrlBase"), "/"),
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

// arrGet calls a *arr v3 API endpoint on an instance.
func arrGet(inst arrInstance, path string) (int, string) {
	arrSem <- struct{}{}
	defer func() { <-arrSem }()
	url := "http://" + inst.IP + ":" + inst.Port + inst.URLBase + "/api/v3/" + path
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	rc, out, _ := executor.Get().Run(ctx, []string{"curl", "-fsS", "-H", "X-Api-Key: " + inst.APIKey, url}, "")
	return rc, out
}

// arrSend issues a method+body request to a *arr v3 endpoint (POST/PUT). Body is
// piped via stdin to avoid shell quoting.
func arrSend(inst arrInstance, method, path, body string) (int, string) {
	arrSem <- struct{}{}
	defer func() { <-arrSem }()
	url := "http://" + inst.IP + ":" + inst.Port + inst.URLBase + "/api/v3/" + path
	args := []string{"curl", "-fsS", "-X", method, "-H", "X-Api-Key: " + inst.APIKey}
	if body != "" {
		args = append(args, "-H", "Content-Type: application/json", "--data", "@-")
	}
	args = append(args, url)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	rc, out, _ := executor.Get().Run(ctx, args, body)
	return rc, out
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

	var rc int
	var out string
	switch b.Action {
	case "refresh":
		if sonarr {
			rc, out = arrSend(*inst, "POST", "command", `{"name":"RefreshSeries","seriesId":`+id+`}`)
		} else {
			rc, out = arrSend(*inst, "POST", "command", `{"name":"RefreshMovie","movieIds":[`+id+`]}`)
		}
	case "search":
		if sonarr {
			rc, out = arrSend(*inst, "POST", "command", `{"name":"SeriesSearch","seriesId":`+id+`}`)
		} else {
			rc, out = arrSend(*inst, "POST", "command", `{"name":"MoviesSearch","movieIds":[`+id+`]}`)
		}
	case "rename":
		if sonarr {
			rc, out = arrSend(*inst, "POST", "command", `{"name":"RenameSeries","seriesIds":[`+id+`]}`)
		} else {
			rc, out = arrSend(*inst, "POST", "command", `{"name":"RenameMovie","movieIds":[`+id+`]}`)
		}
		clearArrFileCache(b.Kind, b.Instance, b.ID)
	case "monitor", "unmonitor":
		obj := "series/" + id
		if !sonarr {
			obj = "movie/" + id
		}
		grc, gout := arrGet(*inst, obj)
		if grc != 0 {
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
		rc, out = arrSend(*inst, "PUT", obj, string(body))
	case "episodeSearch": // sonarr only
		rc, out = arrSend(*inst, "POST", "command", `{"name":"EpisodeSearch","episodeIds":[`+strconv.Itoa(b.EpisodeID)+`]}`)
	case "seasonSearch": // sonarr only
		if b.Season == nil {
			http.Error(w, "season required", http.StatusBadRequest)
			return
		}
		rc, out = arrSend(*inst, "POST", "command", `{"name":"SeasonSearch","seriesId":`+id+`,"seasonNumber":`+strconv.Itoa(*b.Season)+`}`)
	case "deleteFile":
		ep := "episodefile/" + strconv.Itoa(b.FileID)
		if !sonarr {
			ep = "moviefile/" + strconv.Itoa(b.FileID)
		}
		rc, out = arrSend(*inst, "DELETE", ep, "")
		clearArrFileCache(b.Kind, b.Instance, b.ID)
	case "seasonMonitor", "seasonUnmonitor": // sonarr only
		if b.Season == nil {
			http.Error(w, "season required", http.StatusBadRequest)
			return
		}
		grc, gout := arrGet(*inst, "series/"+id)
		if grc != 0 {
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
		rc, out = arrSend(*inst, "PUT", "series/"+id, string(body))
	default:
		http.Error(w, "unknown action", http.StatusBadRequest)
		return
	}
	if rc != 0 {
		http.Error(w, "command failed: "+strings.TrimSpace(out), http.StatusBadGateway)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// arrProfiles maps qualityProfileId → name for one instance.
func arrProfiles(inst arrInstance) map[int]string {
	m := map[int]string{}
	rc, out := arrGet(inst, "qualityprofile")
	if rc != 0 {
		return m
	}
	var profs []struct {
		ID   int    `json:"id"`
		Name string `json:"name"`
	}
	_ = json.Unmarshal([]byte(out), &profs)
	for _, p := range profs {
		m[p.ID] = p.Name
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
	Copies    []arrCopy `json:"copies"`
}

// imgPoster returns the browser-loadable poster URL from an arr images array.
func imgPoster(images []struct {
	CoverType string `json:"coverType"`
	RemoteURL string `json:"remoteUrl"`
	URL       string `json:"url"`
}) string {
	for _, im := range images {
		if im.CoverType == "poster" && im.RemoteURL != "" {
			return im.RemoteURL
		}
	}
	return ""
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

	type imgT = []struct {
		CoverType string `json:"coverType"`
		RemoteURL string `json:"remoteUrl"`
		URL       string `json:"url"`
	}

	var wg sync.WaitGroup
	for _, inst := range insts {
		wg.Add(1)
		go func(inst arrInstance) {
			defer wg.Done()
			profiles := arrProfiles(inst)
			if inst.Kind == "sonarr" {
				rc, out := arrGet(inst, "series")
				if rc != 0 {
					return
				}
				var series []struct {
					ID               int      `json:"id"`
					Title            string   `json:"title"`
					Year             int      `json:"year"`
					TvdbID           int      `json:"tvdbId"`
					QualityProfileID int      `json:"qualityProfileId"`
					Overview         string   `json:"overview"`
					Status           string   `json:"status"`
					Network          string   `json:"network"`
					Runtime          int      `json:"runtime"`
					Monitored        bool     `json:"monitored"`
					Genres           []string `json:"genres"`
					Images           imgT     `json:"images"`
					Ratings          struct {
						Value float64 `json:"value"`
					} `json:"ratings"`
					Statistics struct {
						SeasonCount      int   `json:"seasonCount"`
						EpisodeCount     int   `json:"episodeCount"`
						EpisodeFileCount int   `json:"episodeFileCount"`
						SizeOnDisk       int64 `json:"sizeOnDisk"`
					} `json:"statistics"`
				}
				_ = json.Unmarshal([]byte(out), &series)
				for _, s := range series {
					add(arrItem{
						Kind: "sonarr", Key: strconv.Itoa(s.TvdbID), Title: s.Title, Year: s.Year,
						Poster: imgPoster(s.Images), Overview: s.Overview, Status: s.Status,
						Network: s.Network, Runtime: s.Runtime, Rating: s.Ratings.Value,
						Monitored: s.Monitored, Genres: s.Genres,
						Seasons: s.Statistics.SeasonCount, Episodes: s.Statistics.EpisodeCount,
					}, arrCopy{
						Instance: inst.Name, ItemID: s.ID, Profile: profiles[s.QualityProfileID],
						Files: s.Statistics.EpisodeFileCount, Size: s.Statistics.SizeOnDisk,
						HasFile: s.Statistics.EpisodeFileCount > 0,
					})
				}
			} else {
				rc, out := arrGet(inst, "movie")
				if rc != 0 {
					return
				}
				var movies []struct {
					ID               int      `json:"id"`
					Title            string   `json:"title"`
					Year             int      `json:"year"`
					TmdbID           int      `json:"tmdbId"`
					QualityProfileID int      `json:"qualityProfileId"`
					Overview         string   `json:"overview"`
					Status           string   `json:"status"`
					Studio           string   `json:"studio"`
					Runtime          int      `json:"runtime"`
					Monitored        bool     `json:"monitored"`
					Genres           []string `json:"genres"`
					Images           imgT     `json:"images"`
					Ratings          struct {
						Tmdb struct {
							Value float64 `json:"value"`
						} `json:"tmdb"`
					} `json:"ratings"`
					HasFile    bool  `json:"hasFile"`
					SizeOnDisk int64 `json:"sizeOnDisk"`
				}
				_ = json.Unmarshal([]byte(out), &movies)
				for _, m := range movies {
					files := 0
					if m.HasFile {
						files = 1
					}
					add(arrItem{
						Kind: "radarr", Key: strconv.Itoa(m.TmdbID), Title: m.Title, Year: m.Year,
						Poster: imgPoster(m.Images), Overview: m.Overview, Status: m.Status,
						Network: m.Studio, Runtime: m.Runtime, Rating: m.Ratings.Tmdb.Value,
						Monitored: m.Monitored, Genres: m.Genres,
					}, arrCopy{
						Instance: inst.Name, ItemID: m.ID, Profile: profiles[m.QualityProfileID],
						Files: files, Size: m.SizeOnDisk, HasFile: m.HasFile,
					})
				}
			}
		}(inst)
	}
	wg.Wait()

	items := make([]*arrItem, 0, len(groups))
	for _, g := range groups {
		sort.Slice(g.Copies, func(i, j int) bool { return g.Copies[i].Instance < g.Copies[j].Instance })
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
			go fetchArrFiles(inst, it.Kind, strconv.Itoa(c.ItemID))
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
	Quality      string    `json:"quality,omitempty"`
	Size         int64     `json:"size"`
	Path         string    `json:"path,omitempty"`      // relative
	FullPath     string    `json:"full_path,omitempty"` // absolute
	ReleaseGroup string    `json:"release_group,omitempty"`
	Languages    string    `json:"languages,omitempty"`
	DateAdded    string    `json:"date_added,omitempty"`
	Media        *arrMedia `json:"media,omitempty"`
}

// arrRawFile is the episodeFile/movieFile shape both Sonarr and Radarr return.
type arrRawFile struct {
	ID           int    `json:"id"`
	RelativePath string `json:"relativePath"`
	Path         string `json:"path"`
	Size         int64  `json:"size"`
	DateAdded    string `json:"dateAdded"`
	ReleaseGroup string `json:"releaseGroup"`
	Languages    []struct {
		Name string `json:"name"`
	} `json:"languages"`
	Quality struct {
		Quality struct {
			Name string `json:"name"`
		} `json:"quality"`
	} `json:"quality"`
	MediaInfo struct {
		Resolution        string  `json:"resolution"`
		VideoCodec        string  `json:"videoCodec"`
		VideoDynamicRange string  `json:"videoDynamicRange"`
		AudioCodec        string  `json:"audioCodec"`
		AudioChannels     float64 `json:"audioChannels"`
		AudioLanguages    string  `json:"audioLanguages"`
		Subtitles         string  `json:"subtitles"`
		RunTime           string  `json:"runTime"`
	} `json:"mediaInfo"`
}

// applyFile copies a raw episode/movie file's details onto an arrFile.
func applyFile(a *arrFile, f arrRawFile) {
	a.FileID = f.ID
	a.HasFile = true
	a.Quality = f.Quality.Quality.Name
	a.Size = f.Size
	a.Path = f.RelativePath
	if a.Path == "" {
		a.Path = f.Path
	}
	a.FullPath = f.Path
	a.ReleaseGroup = f.ReleaseGroup
	a.DateAdded = f.DateAdded
	langs := make([]string, 0, len(f.Languages))
	for _, l := range f.Languages {
		if l.Name != "" {
			langs = append(langs, l.Name)
		}
	}
	a.Languages = strings.Join(langs, ", ")
	mi := f.MediaInfo
	if mi.VideoCodec != "" || mi.Resolution != "" || mi.AudioCodec != "" {
		a.Media = &arrMedia{
			Resolution: mi.Resolution, VideoCodec: mi.VideoCodec, DynamicRange: mi.VideoDynamicRange,
			AudioCodec: mi.AudioCodec, AudioChannels: mi.AudioChannels, AudioLanguages: mi.AudioLanguages,
			Subtitles: mi.Subtitles, Runtime: mi.RunTime,
		}
	}
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

func fetchArrFiles(inst arrInstance, kind, id string) ([]arrFile, bool) {
	ck := kind + "|" + inst.Name + "|" + id
	arrFileMu.Lock()
	if e, ok := arrFileCache[ck]; ok && time.Since(e.ts) < arrFileTTL {
		arrFileMu.Unlock()
		return e.files, true
	}
	arrFileMu.Unlock()

	var files []arrFile
	if kind == "sonarr" {
		// One call returns every episode + its file (incl. missing episodes) — the
		// data the Prismarr-style collapsible season/episode view needs.
		rc, out := arrGet(inst, "episode?seriesId="+id+"&includeEpisodeFile=true")
		if rc != 0 {
			return nil, false
		}
		var eps []struct {
			ID            int        `json:"id"`
			SeasonNumber  int        `json:"seasonNumber"`
			EpisodeNumber int        `json:"episodeNumber"`
			Title         string     `json:"title"`
			AirDate       string     `json:"airDate"`
			Monitored     bool       `json:"monitored"`
			HasFile       bool       `json:"hasFile"`
			EpisodeFile   arrRawFile `json:"episodeFile"`
		}
		_ = json.Unmarshal([]byte(out), &eps)
		for i := range eps {
			e := eps[i]
			sn, en := e.SeasonNumber, e.EpisodeNumber
			af := arrFile{Season: &sn, Episode: &en, EpisodeID: e.ID, Title: e.Title, AirDate: e.AirDate, Monitored: e.Monitored}
			if e.HasFile {
				applyFile(&af, e.EpisodeFile)
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
		rc, out := arrGet(inst, "moviefile?movieId="+id)
		if rc != 0 {
			return nil, false
		}
		var raw []arrRawFile
		_ = json.Unmarshal([]byte(out), &raw)
		for _, r := range raw {
			var af arrFile
			applyFile(&af, r)
			files = append(files, af)
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
	if id == "" {
		http.Error(w, "id required", http.StatusBadRequest)
		return
	}
	inst := arrInstanceByName(kind, name)
	if inst == nil {
		http.Error(w, "instance not found", http.StatusNotFound)
		return
	}
	files, ok := fetchArrFiles(*inst, kind, id)
	if !ok {
		http.Error(w, "fetch failed", http.StatusBadGateway)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"files": files})
}
