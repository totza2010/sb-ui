package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// TMDb is the source of all Discover DISPLAY metadata (rich + canonical: trailers
// for TV too, watch providers, certifications, cast, seasons). Seerr is used only to
// submit requests. In-library status is matched against the *arr apps by id
// (movies + series by tmdbId; series also by tvdbId in the detail view).

func tmdbImgS(size, p string) string {
	if p == "" {
		return ""
	}
	return "https://image.tmdb.org/t/p/" + size + p
}

func tmdbGet(apiKey, path string, params url.Values, v any) error {
	if params == nil {
		params = url.Values{}
	}
	params.Set("api_key", apiKey)
	u := "https://api.themoviedb.org/3" + path + "?" + params.Encode()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := arrHTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 200))
		return fmt.Errorf("TMDb HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}
	return json.NewDecoder(resp.Body).Decode(v)
}

// ── in-library id index (tmdb for movies+series, tvdb for series) ──

// arrIDSet maps a title's external id to its real availability, Overseerr-style:
//
//	3 = Processing — in *arr but no files yet (requested/monitored/downloading)
//	4 = Partially available — some episodes have files (series only)
//	5 = Available — movie has a file / all aired episodes downloaded
//
// Mere presence in Sonarr/Radarr is NOT "available": a freshly-added series with
// zero episode files is status 3, so the UI shows it as still requestable.
type arrIDSet struct {
	Tmdb      map[int]int // tmdbId  -> status (movies + series)
	Tvdb      map[int]int // tvdbId  -> status (series)
	MovieTmdb []int       // library movie tmdbIds (for "For you" seeding)
	TvTmdb    []int       // library series tmdbIds
}

// bumpStatus records the highest status seen for an id across instances.
func bumpStatus(m map[int]int, id, st int) {
	if id > 0 && st > m[id] {
		m[id] = st
	}
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

var (
	arrIDMu  sync.Mutex
	arrIDVal *arrIDSet
	arrIDTS  time.Time
)

func arrLibIDs() arrIDSet {
	arrIDMu.Lock()
	defer arrIDMu.Unlock()
	if arrIDVal != nil && time.Since(arrIDTS) < 5*time.Minute {
		return *arrIDVal
	}
	s := arrIDSet{Tmdb: map[int]int{}, Tvdb: map[int]int{}}
	for _, inst := range arrInstancesCached() {
		ctx, cancel := arrCtx()
		if inst.Kind == "sonarr" {
			if series, _, err := sonarrClient(inst).SeriesAPI.ListSeries(ctx).Execute(); err == nil {
				for i := range series {
					st := seriesStatus(&series[i])
					if id := int(series[i].GetTmdbId()); id > 0 {
						bumpStatus(s.Tmdb, id, st)
						s.TvTmdb = append(s.TvTmdb, id)
					}
					bumpStatus(s.Tvdb, int(series[i].GetTvdbId()), st)
				}
			}
		} else if inst.Kind == "radarr" {
			if movies, _, err := radarrClient(inst).MovieAPI.ListMovie(ctx).Execute(); err == nil {
				for i := range movies {
					st := 3 // added but no file yet
					if movies[i].GetHasFile() {
						st = 5
					}
					if id := int(movies[i].GetTmdbId()); id > 0 {
						bumpStatus(s.Tmdb, id, st)
						s.MovieTmdb = append(s.MovieTmdb, id)
					}
				}
			}
		}
		cancel()
	}
	arrIDVal, arrIDTS = &s, time.Now()
	return s
}

func resetArrLibIDs() {
	arrIDMu.Lock()
	arrIDVal = nil
	arrIDMu.Unlock()
}

// ── discover ──

type tmdbResult struct {
	ID           int     `json:"id"`
	MediaType    string  `json:"media_type"` // present on /trending/all, /search/multi
	Title        string  `json:"title"`
	Name         string  `json:"name"`
	PosterPath   string  `json:"poster_path"`
	BackdropPath string  `json:"backdrop_path"`
	Overview     string  `json:"overview"`
	VoteAverage  float64 `json:"vote_average"`
	ReleaseDate  string  `json:"release_date"`
	FirstAirDate string  `json:"first_air_date"`
}

type tmdbListResp struct {
	Page       int          `json:"page"`
	TotalPages int          `json:"total_pages"`
	Results    []tmdbResult `json:"results"`
}

// mapItem turns a TMDb result into a unified item, defaulting media type to defMT
// when the endpoint doesn't carry one, and flagging in-library titles.
func mapItem(r tmdbResult, defMT string, lib arrIDSet) seerrItem {
	mt := defMT
	if r.MediaType == "movie" || r.MediaType == "tv" {
		mt = r.MediaType
	}
	it := seerrItem{MediaType: mt, TmdbID: r.ID, Poster: tmdbImgS("w342", r.PosterPath), Backdrop: tmdbImgS("w1280", r.BackdropPath), Overview: r.Overview, Vote: r.VoteAverage}
	if mt == "tv" {
		it.Title = r.Name
		if len(r.FirstAirDate) >= 4 {
			it.Year = r.FirstAirDate[:4]
		}
	} else {
		it.Title = r.Title
		if len(r.ReleaseDate) >= 4 {
			it.Year = r.ReleaseDate[:4]
		}
	}
	it.Status = lib.Tmdb[r.ID] // 0 if absent; 3/4/5 by real file availability
	return it
}

// tmdbList fetches one TMDb list endpoint and maps it to items (movie/tv only).
func tmdbList(apiKey, path, defMT string, params url.Values, lib arrIDSet) ([]seerrItem, error) {
	var lr tmdbListResp
	if err := tmdbGet(apiKey, path, params, &lr); err != nil {
		return nil, err
	}
	out := []seerrItem{}
	for _, r := range lr.Results {
		mt := r.MediaType
		if mt == "" {
			mt = defMT
		}
		if mt != "movie" && mt != "tv" {
			continue // skip people, etc.
		}
		if r.PosterPath == "" {
			continue
		}
		out = append(out, mapItem(r, defMT, lib))
	}
	return out, nil
}

// discoverLibrary serves the Explorer's availability filter (in-library / partial)
// straight from Sonarr/Radarr, so the UI doesn't have to page through all of TMDb and
// filter client-side. Returns every library title of the type with its status; the
// frontend slices "in" (complete) vs "partial". No TMDb call needed.
func discoverLibrary(w http.ResponseWriter, req *http.Request) {
	mt := req.URL.Query().Get("type")
	byTmdb := map[int]*seerrItem{}
	for _, inst := range arrInstancesCached() {
		var batch []seerrItem
		switch {
		case mt == "tv" && inst.Kind == "sonarr":
			batch = sonarrLibItems(inst)
		case mt == "movie" && inst.Kind == "radarr":
			batch = radarrLibItems(inst)
		default:
			continue
		}
		for i := range batch {
			it := batch[i]
			if cur, ok := byTmdb[it.TmdbID]; ok {
				if it.Status > cur.Status { // same title in two instances: keep best availability
					cur.Status = it.Status
				}
				continue
			}
			byTmdb[it.TmdbID] = &it
		}
	}
	items := make([]seerrItem, 0, len(byTmdb))
	for _, it := range byTmdb {
		items = append(items, *it)
	}
	sort.Slice(items, func(i, j int) bool { // newest first, then title
		if items[i].Year != items[j].Year {
			return items[i].Year > items[j].Year
		}
		return items[i].Title < items[j].Title
	})
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

// tmdbDiscover returns a paginated category (popular movies/TV) for the grid view.
func tmdbDiscover(w http.ResponseWriter, req *http.Request) {
	cfg := loadOptions().Tmdb
	if cfg.APIKey == "" {
		http.Error(w, "TMDb not configured (Settings → Discover)", http.StatusBadRequest)
		return
	}
	page := req.URL.Query().Get("page")
	if page == "" {
		page = "1"
	}
	path, mt := "/movie/popular", "movie"
	if req.URL.Query().Get("type") == "tv" {
		path, mt = "/tv/popular", "tv"
	}
	items, err := tmdbList(cfg.APIKey, path, mt, url.Values{"page": {page}}, arrLibIDs())
	if err != nil {
		http.Error(w, "TMDb discover failed: "+err.Error(), http.StatusBadGateway)
		return
	}
	p, _ := strconv.Atoi(page)
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "page": p})
}

type discoverSection struct {
	Key   string      `json:"key"`
	Title string      `json:"title"`
	Items []seerrItem `json:"items"`
}

// discoverHome assembles the Discover landing page: trending heroes + several
// carousels (trending, popular, top-rated, upcoming) + a library-based "For you".
func discoverHome(w http.ResponseWriter, _ *http.Request) {
	cfg := loadOptions().Tmdb
	if cfg.APIKey == "" {
		http.Error(w, "TMDb not configured (Settings → Discover)", http.StatusBadRequest)
		return
	}
	lib := arrLibIDs()
	key := cfg.APIKey

	type job struct {
		key, title, path, defMT string
	}
	jobs := []job{
		{"trending", "Trending this week", "/trending/all/week", ""},
		{"popular-movies", "Popular movies", "/movie/popular", "movie"},
		{"popular-tv", "Popular TV", "/tv/popular", "tv"},
		{"top-movies", "Top rated movies", "/movie/top_rated", "movie"},
		{"top-tv", "Top rated TV", "/tv/top_rated", "tv"},
		{"upcoming", "Upcoming movies", "/movie/upcoming", "movie"},
	}
	secs := make([]discoverSection, len(jobs))
	var wg sync.WaitGroup
	for i, j := range jobs {
		wg.Add(1)
		go func(i int, j job) {
			defer wg.Done()
			items, _ := tmdbList(key, j.path, j.defMT, nil, lib)
			secs[i] = discoverSection{Key: j.key, Title: j.title, Items: items}
		}(i, j)
	}

	var heroMovie, heroTV *seerrItem
	var forYou []seerrItem
	wg.Add(3)
	go func() {
		defer wg.Done()
		if items, _ := tmdbList(key, "/trending/movie/week", "movie", nil, lib); len(items) > 0 {
			heroMovie = &items[0]
		}
	}()
	go func() {
		defer wg.Done()
		if items, _ := tmdbList(key, "/trending/tv/week", "tv", nil, lib); len(items) > 0 {
			heroTV = &items[0]
		}
	}()
	go func() {
		defer wg.Done()
		forYou = tmdbForYou(key, lib)
	}()
	wg.Wait()

	out := []discoverSection{}
	if len(forYou) > 0 {
		out = append(out, discoverSection{Key: "for-you", Title: "For you · based on your library", Items: forYou})
	}
	out = append(out, secs...)
	writeJSON(w, http.StatusOK, map[string]any{"hero_movie": heroMovie, "hero_tv": heroTV, "sections": out})
}

// tmdbForYou seeds recommendations from a random sample of the library, excluding
// titles already in it.
func tmdbForYou(apiKey string, lib arrIDSet) []seerrItem {
	type seed struct {
		id int
		mt string
	}
	seeds := []seed{}
	add := func(ids []int, mt string, n int) {
		for i := 0; i < len(ids) && i < n; i++ {
			seeds = append(seeds, seed{ids[rand.Intn(len(ids))], mt})
		}
	}
	add(lib.MovieTmdb, "movie", 3)
	add(lib.TvTmdb, "tv", 3)
	if len(seeds) == 0 {
		return nil
	}
	seen := map[int]bool{}
	var mu sync.Mutex
	out := []seerrItem{}
	var wg sync.WaitGroup
	for _, s := range seeds {
		wg.Add(1)
		go func(s seed) {
			defer wg.Done()
			path := "/" + s.mt + "/" + strconv.Itoa(s.id) + "/recommendations"
			items, _ := tmdbList(apiKey, path, s.mt, nil, lib)
			mu.Lock()
			for _, it := range items {
				if it.Status >= 4 || seen[it.TmdbID] {
					continue
				}
				seen[it.TmdbID] = true
				out = append(out, it)
			}
			mu.Unlock()
		}(s)
	}
	wg.Wait()
	if len(out) > 20 {
		out = out[:20]
	}
	return out
}

// discoverGenres returns TMDb's genre list for the genre dropdown.
func discoverGenres(w http.ResponseWriter, req *http.Request) {
	cfg := loadOptions().Tmdb
	if cfg.APIKey == "" {
		http.Error(w, "TMDb not configured", http.StatusBadRequest)
		return
	}
	t := "movie"
	if req.URL.Query().Get("type") == "tv" {
		t = "tv"
	}
	var r struct {
		Genres []struct {
			ID   int    `json:"id"`
			Name string `json:"name"`
		} `json:"genres"`
	}
	if err := tmdbGet(cfg.APIKey, "/genre/"+t+"/list", nil, &r); err != nil {
		http.Error(w, "TMDb genres failed: "+err.Error(), http.StatusBadGateway)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"genres": r.Genres})
}

// discoverExplore runs TMDb /discover with filters (genre, year range, min rating,
// sort) — the Explorer view.
func discoverExplore(w http.ResponseWriter, req *http.Request) {
	cfg := loadOptions().Tmdb
	if cfg.APIKey == "" {
		http.Error(w, "TMDb not configured", http.StatusBadRequest)
		return
	}
	q := req.URL.Query()
	t := "movie"
	if q.Get("type") == "tv" {
		t = "tv"
	}
	dateField := "primary_release_date"
	if t == "tv" {
		dateField = "first_air_date"
	}
	p := url.Values{}
	if pg := q.Get("page"); pg != "" {
		p.Set("page", pg)
	} else {
		p.Set("page", "1")
	}
	switch q.Get("sort") {
	case "rating":
		p.Set("sort_by", "vote_average.desc")
		p.Set("vote_count.gte", "200")
	case "release":
		p.Set("sort_by", dateField+".desc")
	case "release.asc":
		p.Set("sort_by", dateField+".asc")
	default:
		p.Set("sort_by", "popularity.desc")
	}
	if g := q.Get("genres"); g != "" {
		p.Set("with_genres", g)
	}
	if v := q.Get("vote_min"); v != "" {
		p.Set("vote_average.gte", v)
		p.Set("vote_count.gte", "50") // avoid obscure high-rated noise
	}
	if y := q.Get("year_min"); y != "" {
		p.Set(dateField+".gte", y+"-01-01")
	}
	if y := q.Get("year_max"); y != "" {
		p.Set(dateField+".lte", y+"-12-31")
	}

	var lr tmdbListResp
	if err := tmdbGet(cfg.APIKey, "/discover/"+t, p, &lr); err != nil {
		http.Error(w, "TMDb explore failed: "+err.Error(), http.StatusBadGateway)
		return
	}
	lib := arrLibIDs()
	items := []seerrItem{}
	for _, r := range lr.Results {
		if r.PosterPath == "" {
			continue
		}
		items = append(items, mapItem(r, t, lib))
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "page": lr.Page, "total_pages": lr.TotalPages})
}

// discoverCollection returns all movies in a TMDb collection (franchise).
func discoverCollection(w http.ResponseWriter, req *http.Request) {
	cfg := loadOptions().Tmdb
	if cfg.APIKey == "" {
		http.Error(w, "TMDb not configured", http.StatusBadRequest)
		return
	}
	id := req.URL.Query().Get("id")
	if id == "" {
		http.Error(w, "id required", http.StatusBadRequest)
		return
	}
	var r struct {
		Name  string       `json:"name"`
		Parts []tmdbResult `json:"parts"`
	}
	if err := tmdbGet(cfg.APIKey, "/collection/"+id, nil, &r); err != nil {
		http.Error(w, "TMDb collection failed: "+err.Error(), http.StatusBadGateway)
		return
	}
	lib := arrLibIDs()
	items := []seerrItem{}
	for _, p := range r.Parts {
		if p.PosterPath == "" {
			continue
		}
		items = append(items, mapItem(p, "movie", lib))
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Year < items[j].Year })
	writeJSON(w, http.StatusOK, map[string]any{"name": r.Name, "items": items})
}

type tmdbSuggestion struct {
	ID      int    `json:"id"`
	Name    string `json:"name"`
	Image   string `json:"image"`
	KnownFor string `json:"known_for,omitempty"`
}

// discoverCollectionSearch suggests collections by name.
func discoverCollectionSearch(w http.ResponseWriter, req *http.Request) {
	cfg := loadOptions().Tmdb
	if cfg.APIKey == "" {
		http.Error(w, "TMDb not configured", http.StatusBadRequest)
		return
	}
	q := strings.TrimSpace(req.URL.Query().Get("q"))
	out := []tmdbSuggestion{}
	if q != "" {
		var r struct {
			Results []struct {
				ID         int    `json:"id"`
				Name       string `json:"name"`
				PosterPath string `json:"poster_path"`
			} `json:"results"`
		}
		if err := tmdbGet(cfg.APIKey, "/search/collection", url.Values{"query": {q}}, &r); err == nil {
			for i, c := range r.Results {
				out = append(out, tmdbSuggestion{ID: c.ID, Name: c.Name, Image: tmdbImgS("w92", c.PosterPath)})
				if i >= 9 {
					break
				}
			}
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"results": out})
}

// discoverPersonSearch suggests actors/people by name.
func discoverPersonSearch(w http.ResponseWriter, req *http.Request) {
	cfg := loadOptions().Tmdb
	if cfg.APIKey == "" {
		http.Error(w, "TMDb not configured", http.StatusBadRequest)
		return
	}
	q := strings.TrimSpace(req.URL.Query().Get("q"))
	out := []tmdbSuggestion{}
	if q != "" {
		var r struct {
			Results []struct {
				ID                 int    `json:"id"`
				Name               string `json:"name"`
				ProfilePath        string `json:"profile_path"`
				KnownForDepartment string `json:"known_for_department"`
			} `json:"results"`
		}
		if err := tmdbGet(cfg.APIKey, "/search/person", url.Values{"query": {q}}, &r); err == nil {
			for i, p := range r.Results {
				out = append(out, tmdbSuggestion{ID: p.ID, Name: p.Name, Image: tmdbImgS("w185", p.ProfilePath), KnownFor: p.KnownForDepartment})
				if i >= 9 {
					break
				}
			}
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"results": out})
}

// discoverPerson returns a person's filmography (movies + TV they appear in).
func discoverPerson(w http.ResponseWriter, req *http.Request) {
	cfg := loadOptions().Tmdb
	if cfg.APIKey == "" {
		http.Error(w, "TMDb not configured", http.StatusBadRequest)
		return
	}
	id := req.URL.Query().Get("id")
	if id == "" {
		http.Error(w, "id required", http.StatusBadRequest)
		return
	}
	var r struct {
		Cast []tmdbResult `json:"cast"`
	}
	if err := tmdbGet(cfg.APIKey, "/person/"+id+"/combined_credits", nil, &r); err != nil {
		http.Error(w, "TMDb person failed: "+err.Error(), http.StatusBadGateway)
		return
	}
	lib := arrLibIDs()
	seen := map[string]bool{}
	items := []seerrItem{}
	for _, c := range r.Cast {
		if c.PosterPath == "" || (c.MediaType != "movie" && c.MediaType != "tv") {
			continue
		}
		it := mapItem(c, c.MediaType, lib)
		k := it.MediaType + "-" + strconv.Itoa(it.TmdbID)
		if seen[k] {
			continue
		}
		seen[k] = true
		items = append(items, it)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Year > items[j].Year })
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

// discoverSearch runs a TMDb multi search (movies + TV).
func discoverSearch(w http.ResponseWriter, req *http.Request) {
	cfg := loadOptions().Tmdb
	if cfg.APIKey == "" {
		http.Error(w, "TMDb not configured", http.StatusBadRequest)
		return
	}
	q := strings.TrimSpace(req.URL.Query().Get("q"))
	if q == "" {
		writeJSON(w, http.StatusOK, map[string]any{"items": []seerrItem{}})
		return
	}
	items, err := tmdbList(cfg.APIKey, "/search/multi", "", url.Values{"query": {q}}, arrLibIDs())
	if err != nil {
		http.Error(w, "TMDb search failed: "+err.Error(), http.StatusBadGateway)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

// ── detail ──

type tmdbName struct {
	Name string `json:"name"`
}

type tmdbCompanyRaw struct {
	Name    string `json:"name"`
	LogoPath string `json:"logo_path"`
}

type tmdbProvider struct {
	ProviderName string `json:"provider_name"`
	LogoPath     string `json:"logo_path"`
}

type tmdbEpisodeRaw struct {
	Name          string `json:"name"`
	SeasonNumber  int    `json:"season_number"`
	EpisodeNumber int    `json:"episode_number"`
	AirDate       string `json:"air_date"`
}

type tmdbDetailRaw struct {
	ID               int     `json:"id"`
	Title            string  `json:"title"`
	Name             string  `json:"name"`
	Tagline          string  `json:"tagline"`
	Overview         string  `json:"overview"`
	BackdropPath     string  `json:"backdrop_path"`
	PosterPath       string  `json:"poster_path"`
	Status           string  `json:"status"`
	Homepage         string  `json:"homepage"`
	OriginalLanguage string  `json:"original_language"`
	ReleaseDate      string  `json:"release_date"`
	FirstAirDate     string  `json:"first_air_date"`
	Runtime          int     `json:"runtime"`
	EpisodeRunTime   []int   `json:"episode_run_time"`
	VoteAverage      float64 `json:"vote_average"`
	VoteCount        int     `json:"vote_count"`
	Popularity       float64 `json:"popularity"`
	NumberOfEpisodes int     `json:"number_of_episodes"`
	Genres           []tmdbName
	ProductionCompanies []tmdbCompanyRaw `json:"production_companies"`
	ProductionCountries []tmdbName       `json:"production_countries"`
	SpokenLanguages     []struct {
		EnglishName string `json:"english_name"`
		Name        string `json:"name"`
	} `json:"spoken_languages"`
	Networks  []tmdbCompanyRaw `json:"networks"`
	CreatedBy []tmdbName       `json:"created_by"`
	Seasons   []struct {
		SeasonNumber int    `json:"season_number"`
		Name         string `json:"name"`
		EpisodeCount int    `json:"episode_count"`
		PosterPath   string `json:"poster_path"`
		AirDate      string `json:"air_date"`
	} `json:"seasons"`
	NextEpisodeToAir *tmdbEpisodeRaw `json:"next_episode_to_air"`
	LastEpisodeToAir *tmdbEpisodeRaw `json:"last_episode_to_air"`
	Videos           struct {
		Results []struct {
			Name string `json:"name"`
			Key  string `json:"key"`
			Site string `json:"site"`
			Type string `json:"type"`
		} `json:"results"`
	} `json:"videos"`
	Credits struct {
		Cast []struct {
			Name        string `json:"name"`
			Character   string `json:"character"`
			ProfilePath string `json:"profile_path"`
		} `json:"cast"`
		Crew []struct {
			Name string `json:"name"`
			Job  string `json:"job"`
		} `json:"crew"`
	} `json:"credits"`
	Keywords struct {
		Keywords []tmdbName `json:"keywords"` // movie
		Results  []tmdbName `json:"results"`  // tv
	} `json:"keywords"`
	ExternalIds struct {
		ImdbID string `json:"imdb_id"`
		TvdbID int    `json:"tvdb_id"`
	} `json:"external_ids"`
	ContentRatings struct {
		Results []struct {
			Iso31661 string `json:"iso_3166_1"`
			Rating   string `json:"rating"`
		} `json:"results"`
	} `json:"content_ratings"` // tv
	ReleaseDates struct {
		Results []struct {
			Iso31661     string `json:"iso_3166_1"`
			ReleaseDates []struct {
				Certification string `json:"certification"`
			} `json:"release_dates"`
		} `json:"results"`
	} `json:"release_dates"` // movie
	WatchProviders struct {
		Results map[string]struct {
			Flatrate []tmdbProvider `json:"flatrate"`
			Buy      []tmdbProvider `json:"buy"`
			Rent     []tmdbProvider `json:"rent"`
		} `json:"results"`
	} `json:"watch/providers"`
}

func tmdbCompanies(cs []tmdbCompanyRaw) []seerrCompany {
	out := []seerrCompany{}
	for _, c := range cs {
		if c.Name == "" {
			continue
		}
		out = append(out, seerrCompany{Name: c.Name, Logo: tmdbImgS("w154", c.LogoPath)})
		if len(out) >= 6 {
			break
		}
	}
	return out
}

func tmdbProviders(ps []tmdbProvider) []seerrCompany {
	out := []seerrCompany{}
	for _, p := range ps {
		out = append(out, seerrCompany{Name: p.ProviderName, Logo: tmdbImgS("w92", p.LogoPath)})
	}
	return out
}

// tmdbDetail fetches full movie/tv metadata from TMDb for the detail modal.
func tmdbDetail(w http.ResponseWriter, req *http.Request) {
	cfg := loadOptions().Tmdb
	if cfg.APIKey == "" {
		http.Error(w, "TMDb not configured", http.StatusBadRequest)
		return
	}
	kind := req.URL.Query().Get("type")
	id, _ := strconv.Atoi(req.URL.Query().Get("id"))
	if id == 0 {
		http.Error(w, "id required", http.StatusBadRequest)
		return
	}
	tv := kind == "tv"
	path := "/movie/" + strconv.Itoa(id)
	appnd := "videos,credits,keywords,external_ids,release_dates,watch/providers"
	if tv {
		path = "/tv/" + strconv.Itoa(id)
		appnd = "videos,credits,keywords,external_ids,content_ratings,watch/providers"
	}
	var r tmdbDetailRaw
	if err := tmdbGet(cfg.APIKey, path, url.Values{"append_to_response": {appnd}}, &r); err != nil {
		http.Error(w, "TMDb detail failed: "+err.Error(), http.StatusBadGateway)
		return
	}

	d := seerrDetailResp{
		MediaType: "movie", TmdbID: id, Tagline: r.Tagline, Overview: r.Overview,
		Backdrop: tmdbImgS("w1280", r.BackdropPath), Poster: tmdbImgS("w342", r.PosterPath),
		StatusText: r.Status, Homepage: r.Homepage, Language: r.OriginalLanguage,
		Vote: r.VoteAverage, VoteCount: r.VoteCount, Popularity: int(r.Popularity),
		ImdbID: r.ExternalIds.ImdbID, Genres: []string{}, Cast: []seerrCast{},
	}
	for _, g := range r.Genres {
		d.Genres = append(d.Genres, g.Name)
	}
	if len(r.ProductionCountries) > 0 {
		d.Country = r.ProductionCountries[0].Name
	}
	langs := []string{}
	for _, l := range r.SpokenLanguages {
		if l.EnglishName != "" {
			langs = append(langs, l.EnglishName)
		}
	}
	d.Languages = strings.Join(langs, ", ")
	d.Studios = tmdbCompanies(r.ProductionCompanies)
	for i := range r.Credits.Cast {
		c := r.Credits.Cast[i]
		d.Cast = append(d.Cast, seerrCast{Name: c.Name, Character: c.Character, Profile: tmdbImgS("w185", c.ProfilePath)})
		if len(d.Cast) >= 12 {
			break
		}
	}
	for _, v := range r.Videos.Results {
		if v.Site != "YouTube" || v.Key == "" {
			continue
		}
		d.Videos = append(d.Videos, seerrVideo{Name: v.Name, Key: v.Key, Type: v.Type})
		if d.Trailer == "" && v.Type == "Trailer" {
			d.Trailer = v.Key
		}
	}
	if d.Trailer == "" && len(d.Videos) > 0 {
		d.Trailer = d.Videos[0].Key
	}
	if wp, ok := r.WatchProviders.Results["US"]; ok {
		d.WatchFlatrate = tmdbProviders(wp.Flatrate)
		d.WatchBuy = tmdbProviders(append(wp.Buy, wp.Rent...))
	}

	lib := arrLibIDs()
	if tv {
		d.MediaType = "tv"
		d.Title = r.Name
		d.ReleaseDate = r.FirstAirDate
		d.Episodes = r.NumberOfEpisodes
		for _, c := range r.CreatedBy {
			d.Creators = append(d.Creators, c.Name)
		}
		for _, k := range r.Keywords.Results {
			d.Tags = append(d.Tags, k.Name)
		}
		d.Networks = tmdbCompanies(r.Networks)
		for _, s := range r.Seasons {
			if s.SeasonNumber <= 0 {
				continue
			}
			d.Seasons++
			d.SeasonList = append(d.SeasonList, seerrSeason{
				Number: s.SeasonNumber, Name: s.Name, Episodes: s.EpisodeCount,
				Poster: tmdbImgS("w185", s.PosterPath), Date: s.AirDate,
			})
		}
		if e := r.NextEpisodeToAir; e != nil {
			d.NextEpisode = &seerrEpisode{Code: fmt.Sprintf("S%02dE%02d", e.SeasonNumber, e.EpisodeNumber), Name: e.Name, Date: e.AirDate}
		}
		if e := r.LastEpisodeToAir; e != nil {
			d.LastEpisode = &seerrEpisode{Code: fmt.Sprintf("S%02dE%02d", e.SeasonNumber, e.EpisodeNumber), Name: e.Name, Date: e.AirDate}
		}
		for _, cr := range r.ContentRatings.Results {
			if cr.Iso31661 == "US" {
				d.Rating = cr.Rating
			}
		}
		if d.Rating == "" && len(r.ContentRatings.Results) > 0 {
			d.Rating = r.ContentRatings.Results[0].Rating
		}
		// Per-season availability from Sonarr (which seasons have files); falls back
		// to the cached library status if the series isn't found in any Sonarr.
		if seasonSt, overall := sonarrSeasonStatus(r.ExternalIds.TvdbID, id); overall > 0 {
			d.Status = overall
			for i := range d.SeasonList {
				d.SeasonList[i].Status = seasonSt[d.SeasonList[i].Number]
			}
		} else if st := maxInt(lib.Tvdb[r.ExternalIds.TvdbID], lib.Tmdb[id]); st > 0 {
			d.Status = st
		}
	} else {
		d.Title = r.Title
		d.ReleaseDate = r.ReleaseDate
		d.Runtime = r.Runtime
		for _, k := range r.Keywords.Keywords {
			d.Tags = append(d.Tags, k.Name)
		}
		for _, c := range r.Credits.Crew {
			if c.Job == "Director" && c.Name != "" {
				d.Creators = append(d.Creators, c.Name)
			}
		}
		for _, rd := range r.ReleaseDates.Results {
			if rd.Iso31661 != "US" {
				continue
			}
			for _, x := range rd.ReleaseDates {
				if x.Certification != "" {
					d.Rating = x.Certification
				}
			}
		}
		d.Status = lib.Tmdb[id]
	}
	if len(d.ReleaseDate) >= 4 {
		d.Year = d.ReleaseDate[:4]
	}
	writeJSON(w, http.StatusOK, d)
}
