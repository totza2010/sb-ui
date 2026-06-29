package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/devopsarr/seerr-go/seerr"
)

// Seerr (Jellyseerr/Overseerr): discover titles not yet in the library and request
// them. Seerr already syncs with the *arr apps + Plex, so each discover result carries
// a `mediaInfo.status` telling us whether it's unknown (requestable), pending, or
// available — we don't diff against arr ourselves.

func seerrClient(cfg seerrConfig) *seerr.APIClient {
	c := seerr.NewConfiguration()
	c.Servers = seerr.ServerConfigurations{{URL: strings.TrimRight(cfg.URL, "/") + "/api/v1"}}
	c.AddDefaultHeader("X-Api-Key", cfg.APIKey)
	c.HTTPClient = arrHTTP
	return seerr.NewAPIClient(c)
}

func seerrCtx() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 30*time.Second)
}

// seerrStatus returns the Seerr version (connection test) — used by the Integrations page.
func seerrStatus(cfg seerrConfig) (string, error) {
	ctx, cancel := seerrCtx()
	defer cancel()
	res, _, err := seerrClient(cfg).PublicAPI.GetStatus(ctx).Execute()
	if err != nil {
		return "", err
	}
	return res.GetVersion(), nil
}

func strv(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func f32v(p *float32) float64 {
	if p == nil {
		return 0
	}
	return float64(*p)
}

// tmdbImg builds a browser-loadable TMDB image URL at the given size.
func tmdbImg(size string, p *string) string {
	if p == nil || *p == "" {
		return ""
	}
	return "https://image.tmdb.org/t/p/" + size + *p
}

func tmdbPoster(p *string) string { return tmdbImg("w342", p) }

// seerrItem is one discover result, unified across movie/tv.
type seerrItem struct {
	MediaType string  `json:"media_type"` // movie | tv
	TmdbID    int     `json:"tmdb_id"`
	Title     string  `json:"title"`
	Year      string  `json:"year"`
	Poster    string  `json:"poster"`
	Backdrop  string  `json:"backdrop,omitempty"` // hero rows only
	Overview  string  `json:"overview"`
	Vote      float64 `json:"vote"`
	Status    int     `json:"status"` // 0/1 requestable · 2/3 requested · 4/5 available
}

func movieToItem(m seerr.MovieResult) seerrItem {
	it := seerrItem{MediaType: "movie", TmdbID: int(m.Id), Title: m.Title, Poster: tmdbPoster(m.PosterPath), Overview: strv(m.Overview), Vote: f32v(m.VoteAverage)}
	if m.ReleaseDate != nil && len(*m.ReleaseDate) >= 4 {
		it.Year = (*m.ReleaseDate)[:4]
	}
	if m.MediaInfo != nil && m.MediaInfo.Status != nil {
		it.Status = int(*m.MediaInfo.Status)
	}
	return it
}

func tvToItem(t seerr.TvResult) seerrItem {
	it := seerrItem{MediaType: "tv", TmdbID: int(f32v(t.Id)), Title: strv(t.Name), Poster: tmdbPoster(t.PosterPath), Overview: strv(t.Overview), Vote: f32v(t.VoteAverage)}
	if t.FirstAirDate != nil && len(*t.FirstAirDate) >= 4 {
		it.Year = (*t.FirstAirDate)[:4]
	}
	if t.MediaInfo != nil && t.MediaInfo.Status != nil {
		it.Status = int(*t.MediaInfo.Status)
	}
	return it
}

// seerrDiscover returns a page of popular movies or TV from Seerr.
func seerrDiscover(w http.ResponseWriter, req *http.Request) {
	cfg := loadOptions().Seerr
	if cfg.URL == "" {
		http.Error(w, "Seerr not configured (Settings → Seerr)", http.StatusBadRequest)
		return
	}
	kind := req.URL.Query().Get("type")
	page, _ := strconv.Atoi(req.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}
	cl := seerrClient(cfg)
	ctx, cancel := seerrCtx()
	defer cancel()

	items := []seerrItem{}
	if kind == "tv" {
		res, _, err := cl.SearchAPI.GetDiscoverTv(ctx).Page(float32(page)).Execute()
		if err != nil {
			http.Error(w, "Seerr discover failed: "+err.Error(), http.StatusBadGateway)
			return
		}
		for _, t := range res.Results {
			items = append(items, tvToItem(t))
		}
	} else {
		res, _, err := cl.SearchAPI.GetDiscoverMovies(ctx).Page(float32(page)).Execute()
		if err != nil {
			http.Error(w, "Seerr discover failed: "+err.Error(), http.StatusBadGateway)
			return
		}
		for _, m := range res.Results {
			items = append(items, movieToItem(m))
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "page": page})
}

type seerrCast struct {
	Name      string `json:"name"`
	Character string `json:"character"`
	Profile   string `json:"profile"`
}

type seerrEpisode struct {
	Code string `json:"code"` // S03E02
	Name string `json:"name"`
	Date string `json:"date"`
}

type seerrSeason struct {
	Number   int    `json:"number"`
	Name     string `json:"name"`
	Episodes int    `json:"episodes"`
	Poster   string `json:"poster"`
	Date     string `json:"date"`
	Status   int    `json:"status"` // 0 missing · 4 partial · 5 complete (from Sonarr)
}

type seerrCompany struct {
	Name string `json:"name"`
	Logo string `json:"logo,omitempty"`
}

type seerrVideo struct {
	Name string `json:"name"`
	Key  string `json:"key"`
	Type string `json:"type"`
}

// seerrDetailResp is the unified, Overseerr-style detail payload.
type seerrDetailResp struct {
	MediaType   string        `json:"media_type"`
	TmdbID      int           `json:"tmdb_id"`
	ImdbID      string        `json:"imdb_id,omitempty"`
	Title       string        `json:"title"`
	Tagline     string        `json:"tagline"`
	Year        string        `json:"year"`
	Overview    string        `json:"overview"`
	Backdrop    string        `json:"backdrop"`
	Poster      string        `json:"poster"`
	Genres      []string      `json:"genres"`
	Vote        float64       `json:"vote"`
	VoteCount   int           `json:"vote_count"`
	Popularity  int           `json:"popularity"`
	Status      int           `json:"status"`      // mediaInfo: 0/1 requestable · 2/3 requested · 4/5 available
	StatusText  string        `json:"status_text"` // production status
	ReleaseDate string        `json:"release_date"`
	Language    string        `json:"language"`  // original language code
	Languages   string        `json:"languages"` // spoken languages
	Country     string        `json:"country"`
	Rating      string         `json:"rating,omitempty"`   // certification (e.g. TV-MA, [FR] 16)
	Homepage    string         `json:"homepage,omitempty"` // official site
	Runtime     int            `json:"runtime,omitempty"`
	Seasons     int            `json:"seasons,omitempty"`
	Episodes    int            `json:"episodes,omitempty"`
	Trailer     string         `json:"trailer,omitempty"`
	Videos      []seerrVideo   `json:"videos,omitempty"` // trailers/teasers/featurettes
	Creators    []string       `json:"creators,omitempty"`
	Studios     []seerrCompany `json:"studios,omitempty"`
	Networks    []seerrCompany `json:"networks,omitempty"`
	Tags        []string       `json:"tags,omitempty"`
	NextEpisode   *seerrEpisode  `json:"next_episode,omitempty"`
	LastEpisode   *seerrEpisode  `json:"last_episode,omitempty"`
	SeasonList    []seerrSeason  `json:"season_list,omitempty"`
	WatchFlatrate []seerrCompany `json:"watch_flatrate,omitempty"` // stream (logo+name)
	WatchBuy      []seerrCompany `json:"watch_buy,omitempty"`      // buy/rent
	Cast          []seerrCast    `json:"cast"`
}

func nstr(n seerr.NullableString) string {
	if v := n.Get(); v != nil {
		return *v
	}
	return ""
}

func pcCompanies(ps []seerr.ProductionCompany) []seerrCompany {
	out := []seerrCompany{}
	for _, p := range ps {
		if strv(p.Name) == "" {
			continue
		}
		logo := ""
		if l := p.LogoPath.Get(); l != nil {
			logo = tmdbImg("w154", l)
		}
		out = append(out, seerrCompany{Name: strv(p.Name), Logo: logo})
		if len(out) >= 6 {
			break
		}
	}
	return out
}

func epFrom(e *seerr.Episode) *seerrEpisode {
	if e == nil {
		return nil
	}
	sn, en := 0, 0
	if e.SeasonNumber != nil {
		sn = int(*e.SeasonNumber)
	}
	if e.EpisodeNumber != nil {
		en = int(*e.EpisodeNumber)
	}
	return &seerrEpisode{Code: fmt.Sprintf("S%02dE%02d", sn, en), Name: strv(e.Name), Date: nstr(e.AirDate)}
}

// seerrDetail fetches full movie/tv details for the Discover detail modal.
func seerrDetail(w http.ResponseWriter, req *http.Request) {
	cfg := loadOptions().Seerr
	if cfg.URL == "" {
		http.Error(w, "Seerr not configured", http.StatusBadRequest)
		return
	}
	kind := req.URL.Query().Get("type")
	id, _ := strconv.Atoi(req.URL.Query().Get("id"))
	if id == 0 {
		http.Error(w, "id required", http.StatusBadRequest)
		return
	}
	cl := seerrClient(cfg)
	ctx, cancel := seerrCtx()
	defer cancel()

	d := seerrDetailResp{TmdbID: id, Cast: []seerrCast{}, Genres: []string{}}
	if kind == "tv" {
		res, _, err := cl.TvAPI.GetTvByTvId(ctx, float32(id)).Execute()
		if err != nil {
			http.Error(w, "Seerr detail failed: "+err.Error(), http.StatusBadGateway)
			return
		}
		d.MediaType = "tv"
		d.Title, d.Tagline, d.Overview = strv(res.Name), strv(res.Tagline), strv(res.Overview)
		d.Backdrop, d.Poster = tmdbImg("w1280", res.BackdropPath), tmdbPoster(res.PosterPath)
		d.Vote, d.VoteCount, d.Popularity = f32v(res.VoteAverage), int(f32v(res.VoteCount)), int(f32v(res.Popularity))
		d.StatusText, d.Episodes, d.Language = strv(res.Status), int(f32v(res.NumberOfEpisodes)), strv(res.OriginalLanguage)
		d.ReleaseDate, d.Languages, d.Homepage = strv(res.FirstAirDate), strings.Join(res.Languages, ", "), strv(res.Homepage)
		if len(d.ReleaseDate) >= 4 {
			d.Year = d.ReleaseDate[:4]
		}
		for _, g := range res.Genres {
			d.Genres = append(d.Genres, strv(g.Name))
		}
		for _, c := range res.CreatedBy {
			d.Creators = append(d.Creators, strv(c.Name))
		}
		for _, k := range res.Keywords {
			d.Tags = append(d.Tags, strv(k.Name))
		}
		d.Studios, d.Networks = pcCompanies(res.ProductionCompanies), pcCompanies(res.Networks)
		if len(res.ProductionCountries) > 0 {
			d.Country = strv(res.ProductionCountries[0].Name)
		}
		if res.ContentRatings != nil {
			for _, r := range res.ContentRatings.Results {
				if strv(r.Iso31661) == "US" {
					d.Rating = strv(r.Rating)
				}
			}
			if d.Rating == "" && len(res.ContentRatings.Results) > 0 {
				d.Rating = strv(res.ContentRatings.Results[0].Rating)
			}
		}
		for _, s := range res.Seasons {
			if s.SeasonNumber == nil || *s.SeasonNumber <= 0 {
				continue
			}
			d.Seasons++
			d.SeasonList = append(d.SeasonList, seerrSeason{
				Number: int(*s.SeasonNumber), Name: strv(s.Name), Episodes: int(f32v(s.EpisodeCount)),
				Poster: tmdbPoster(s.PosterPath), Date: nstr(s.AirDate),
			})
		}
		d.NextEpisode, d.LastEpisode = epFrom(res.NextEpisodeToAir), epFrom(res.LastEpisodeToAir)
		if res.ExternalIds != nil {
			d.ImdbID = nstr(res.ExternalIds.ImdbId)
		}
		if res.MediaInfo != nil && res.MediaInfo.Status != nil {
			d.Status = int(*res.MediaInfo.Status)
		}
		d.Cast = seerrCastFrom(res.Credits)
	} else {
		res, _, err := cl.MoviesAPI.GetMovieByMovieId(ctx, float32(id)).Execute()
		if err != nil {
			http.Error(w, "Seerr detail failed: "+err.Error(), http.StatusBadGateway)
			return
		}
		d.MediaType = "movie"
		d.Title, d.Tagline, d.Overview = strv(res.Title), strv(res.Tagline), strv(res.Overview)
		d.Backdrop, d.Poster = tmdbImg("w1280", res.BackdropPath), tmdbPoster(res.PosterPath)
		d.Vote, d.VoteCount, d.Popularity = f32v(res.VoteAverage), int(f32v(res.VoteCount)), int(f32v(res.Popularity))
		d.StatusText, d.Runtime, d.Language = strv(res.Status), int(f32v(res.Runtime)), strv(res.OriginalLanguage)
		d.ReleaseDate, d.Homepage = strv(res.ReleaseDate), strv(res.Homepage)
		if len(d.ReleaseDate) >= 4 {
			d.Year = d.ReleaseDate[:4]
		}
		for _, g := range res.Genres {
			d.Genres = append(d.Genres, strv(g.Name))
		}
		d.Studios = pcCompanies(res.ProductionCompanies)
		if len(res.ProductionCountries) > 0 {
			d.Country = strv(res.ProductionCountries[0].Name)
		}
		langs := []string{}
		for _, l := range res.SpokenLanguages {
			if strv(l.Name) != "" {
				langs = append(langs, strv(l.Name))
			}
		}
		d.Languages = strings.Join(langs, ", ")
		if res.Credits != nil {
			for _, c := range res.Credits.Crew {
				if strv(c.Job) == "Director" && strv(c.Name) != "" {
					d.Creators = append(d.Creators, strv(c.Name))
				}
			}
		}
		for _, v := range res.RelatedVideos {
			if strv(v.Site) != "YouTube" || strv(v.Key) == "" {
				continue
			}
			d.Videos = append(d.Videos, seerrVideo{Name: strv(v.Name), Key: strv(v.Key), Type: strv(v.Type)})
			if d.Trailer == "" && strv(v.Type) == "Trailer" {
				d.Trailer = strv(v.Key)
			}
		}
		if d.Trailer == "" && len(d.Videos) > 0 {
			d.Trailer = d.Videos[0].Key
		}
		d.ImdbID = strv(res.ImdbId)
		if res.MediaInfo != nil && res.MediaInfo.Status != nil {
			d.Status = int(*res.MediaInfo.Status)
		}
		d.Cast = seerrCastFrom(res.Credits)
	}
	writeJSON(w, http.StatusOK, d)
}

func seerrCastFrom(cr *seerr.MovieDetailsCredits) []seerrCast {
	out := []seerrCast{}
	if cr == nil {
		return out
	}
	for _, c := range cr.Cast {
		profile := ""
		if p := c.ProfilePath.Get(); p != nil {
			profile = tmdbImg("w185", p)
		}
		out = append(out, seerrCast{Name: strv(c.Name), Character: strv(c.Character), Profile: profile})
		if len(out) >= 12 {
			break
		}
	}
	return out
}

// seerrRawGET hits a Seerr API path directly (raw JSON). Used for the
// /service/{sonarr|radarr}/{id} detail, whose profiles+rootFolders the generated
// seerr-go model drops (it types `profiles` as a single object and omits folders).
func seerrRawGET(cfg seerrConfig, path string, v any) error {
	ctx, cancel := seerrCtx()
	defer cancel()
	r, _ := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(cfg.URL, "/")+"/api/v1"+path, nil)
	r.Header.Set("X-Api-Key", cfg.APIKey)
	resp, err := arrHTTP.Do(r)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("seerr HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}
	return json.NewDecoder(resp.Body).Decode(v)
}

// request-options payload — mirrors Seerr's "Request Series/Movie" dialog: pick a
// destination server, quality profile, root folder (and seasons, client-side).
type reqProfile struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}
type reqFolder struct {
	ID   int    `json:"id"`
	Path string `json:"path"`
}
type reqServer struct {
	ID                   int          `json:"id"`
	Name                 string       `json:"name"`
	Is4k                 bool         `json:"is4k"`
	IsDefault            bool         `json:"is_default"`
	DefaultProfileID     int          `json:"default_profile_id"`
	DefaultRoot          string       `json:"default_root"`
	DefaultLangProfileID int          `json:"default_lang_profile_id"`
	Profiles             []reqProfile `json:"profiles"`
	RootFolders          []reqFolder  `json:"root_folders"`
	LangProfiles         []reqProfile `json:"lang_profiles"` // Sonarr only
}

// seerrServer is the trimmed /service/{kind} list entry. Parsed raw because the
// generated seerr-go models mark several fields required and reject Seerr's payload.
type seerrServer struct {
	ID                      int    `json:"id"`
	Name                    string `json:"name"`
	Is4k                    bool   `json:"is4k"`
	IsDefault               bool   `json:"isDefault"`
	ActiveProfileID         int    `json:"activeProfileId"`
	ActiveDirectory         string `json:"activeDirectory"`
	ActiveLanguageProfileID int    `json:"activeLanguageProfileId"`
}

// seerrServerDetail captures the bits of /service/{kind}/{id} we need (profiles,
// rootFolders, languageProfiles) — also raw, since the codegen drops them.
type seerrServerDetail struct {
	Profiles         []reqProfile `json:"profiles"`
	RootFolders      []reqFolder  `json:"rootFolders"`
	LanguageProfiles []reqProfile `json:"languageProfiles"`
}

func (s reqServer) withDetail(cfg seerrConfig, kind string) reqServer {
	s.Profiles, s.RootFolders, s.LangProfiles = []reqProfile{}, []reqFolder{}, []reqProfile{}
	var d seerrServerDetail
	if err := seerrRawGET(cfg, "/service/"+kind+"/"+strconv.Itoa(s.ID), &d); err == nil {
		if d.Profiles != nil {
			s.Profiles = d.Profiles
		}
		if d.RootFolders != nil {
			s.RootFolders = d.RootFolders
		}
		if d.LanguageProfiles != nil {
			s.LangProfiles = d.LanguageProfiles
		}
	}
	return s
}

// requestOptions returns the configured *arr servers (with quality profiles, root
// folders, and language profiles) for the media type, so the request dialog can
// offer the same choices Seerr does. Everything is fetched raw to dodge the
// generated client's lossy/over-strict models.
func requestOptions(w http.ResponseWriter, req *http.Request) {
	cfg := loadOptions().Seerr
	if cfg.URL == "" {
		http.Error(w, "Seerr not configured", http.StatusBadRequest)
		return
	}
	kind := "radarr"
	if req.URL.Query().Get("type") == "tv" {
		kind = "sonarr"
	}
	var list []seerrServer
	if err := seerrRawGET(cfg, "/service/"+kind, &list); err != nil {
		http.Error(w, "Seerr service list failed: "+err.Error(), http.StatusBadGateway)
		return
	}
	servers := []reqServer{}
	for _, s := range list {
		rs := reqServer{ID: s.ID, Name: s.Name, Is4k: s.Is4k, IsDefault: s.IsDefault,
			DefaultProfileID: s.ActiveProfileID, DefaultRoot: s.ActiveDirectory, DefaultLangProfileID: s.ActiveLanguageProfileID}
		servers = append(servers, rs.withDetail(cfg, kind))
	}
	writeJSON(w, http.StatusOK, map[string]any{"servers": servers, "users": seerrUsers(cfg)})
}

type reqUser struct {
	ID    int    `json:"id"`
	Name  string `json:"name"`
	Email string `json:"email"`
}

// seerrUsers lists Seerr accounts so the dialog can offer "Request As" (admins can
// file a request on another user's behalf). Best-effort: empty on any failure.
func seerrUsers(cfg seerrConfig) []reqUser {
	out := []reqUser{}
	var ur struct {
		Results []struct {
			ID          int    `json:"id"`
			DisplayName string `json:"displayName"`
			Username    string `json:"username"`
			Email       string `json:"email"`
		} `json:"results"`
	}
	if err := seerrRawGET(cfg, "/user?take=200&sort=displayname", &ur); err != nil {
		return out
	}
	for _, u := range ur.Results {
		name := u.DisplayName
		if name == "" {
			name = u.Username
		}
		if name == "" {
			name = u.Email
		}
		out = append(out, reqUser{ID: u.ID, Name: name, Email: u.Email})
	}
	return out
}

// seerrRequest submits a request to Seerr (which routes it to the right *arr),
// honouring the dialog's server / profile / root-folder / season choices.
func seerrRequest(w http.ResponseWriter, req *http.Request) {
	cfg := loadOptions().Seerr
	if cfg.URL == "" {
		http.Error(w, "Seerr not configured", http.StatusBadRequest)
		return
	}
	var b struct {
		MediaType         string `json:"media_type"` // movie | tv
		TmdbID            int    `json:"tmdb_id"`
		TvdbID            int    `json:"tvdb_id"`
		ServerID          *int   `json:"server_id"` // pointer: 0 is a valid server id
		ProfileID         int    `json:"profile_id"`
		RootFolder        string `json:"root_folder"`
		LanguageProfileID int    `json:"language_profile_id"`
		Is4k              bool   `json:"is4k"`
		UserID            int    `json:"user_id"` // "Request As" (0 => the API key's user)
		Seasons           []int  `json:"seasons"` // empty => all (tv only)
	}
	if json.NewDecoder(req.Body).Decode(&b) != nil || b.TmdbID == 0 || (b.MediaType != "movie" && b.MediaType != "tv") {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	body := *seerr.NewCreateRequestRequest(b.MediaType, float32(b.TmdbID))
	if b.ServerID != nil {
		sid := float32(*b.ServerID)
		body.ServerId = &sid
	}
	if b.ProfileID > 0 {
		pid := float32(b.ProfileID)
		body.ProfileId = &pid
	}
	if b.RootFolder != "" {
		rf := b.RootFolder
		body.RootFolder = &rf
	}
	if b.LanguageProfileID > 0 {
		lp := float32(b.LanguageProfileID)
		body.LanguageProfileId = &lp
	}
	if b.Is4k {
		t := true
		body.Is4k = &t
	}
	if b.UserID > 0 {
		uid := float32(b.UserID)
		body.UserId = *seerr.NewNullableFloat32(&uid)
	}
	if b.MediaType == "tv" {
		if len(b.Seasons) > 0 {
			fs := make([]float32, len(b.Seasons))
			for i, n := range b.Seasons {
				fs[i] = float32(n)
			}
			seasons := seerr.ArrayOfFloat32AsCreateRequestRequestSeasons(&fs)
			body.Seasons = &seasons
		} else {
			all := "all" // every season; Seerr resolves the tvdb id for Sonarr itself
			seasons := seerr.StringAsCreateRequestRequestSeasons(&all)
			body.Seasons = &seasons
		}
		if b.TvdbID > 0 {
			tv := float32(b.TvdbID)
			body.TvdbId = &tv
		}
	}
	ctx, cancel := seerrCtx()
	defer cancel()
	if _, _, err := seerrClient(cfg).RequestAPI.CreateRequest(ctx).CreateRequestRequest(body).Execute(); err != nil {
		http.Error(w, "request failed: "+err.Error(), http.StatusBadGateway)
		return
	}
	resetArrLibIDs() // reflect the new request on next status refresh
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
