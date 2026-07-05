package api

import (
	"context"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"sb-ui/internal/executor"

	"github.com/devopsarr/prowlarr-go/prowlarr"
	"github.com/devopsarr/radarr-go/radarr"
	"github.com/devopsarr/sonarr-go/sonarr"
	"github.com/devopsarr/whisparr-go/whisparr"
)

// arrHTTP is the shared client for all *arr API traffic. Reasonable timeout —
// ListSeries/ListMovie on big libraries can be a few MB.
var arrHTTP = &http.Client{Timeout: 45 * time.Second}

// arrBaseURL resolves a base URL reachable from wherever sb-ui actually runs —
// not a hardcoded docker IP.
//   - Local executor (sb-ui on the Saltbox host, the normal deployment): hit the
//     container directly over the docker network. Fast, and X-Api-Key auths straight
//     to the app, bypassing Traefik/Authelia.
//   - Remote executor (dev over SSH): the docker IP isn't routable from here, so use
//     the app's real public URL — taken from its Traefik Host rule (WebURL), which
//     reflects any inventory subdomain customisation. Falls back to the docker IP.
func arrBaseURL(inst arrInstance) string {
	if _, local := executor.Get().(executor.LocalExecutor); !local && inst.WebURL != "" {
		return inst.WebURL
	}
	return "http://" + inst.IP + ":" + inst.Port + inst.URLBase
}

// sonarrClient builds a typed Sonarr client for one instance.
func sonarrClient(inst arrInstance) *sonarr.APIClient {
	cfg := sonarr.NewConfiguration()
	cfg.Servers = sonarr.ServerConfigurations{{URL: arrBaseURL(inst)}}
	cfg.AddDefaultHeader("X-Api-Key", inst.APIKey)
	cfg.HTTPClient = arrHTTP
	return sonarr.NewAPIClient(cfg)
}

// radarrClient builds a typed Radarr client for one instance.
func radarrClient(inst arrInstance) *radarr.APIClient {
	cfg := radarr.NewConfiguration()
	cfg.Servers = radarr.ServerConfigurations{{URL: arrBaseURL(inst)}}
	cfg.AddDefaultHeader("X-Api-Key", inst.APIKey)
	cfg.HTTPClient = arrHTTP
	return radarr.NewAPIClient(cfg)
}

// prowlarrClient builds a typed Prowlarr client for one instance.
func prowlarrClient(inst arrInstance) *prowlarr.APIClient {
	cfg := prowlarr.NewConfiguration()
	cfg.Servers = prowlarr.ServerConfigurations{{URL: arrBaseURL(inst)}}
	cfg.AddDefaultHeader("X-Api-Key", inst.APIKey)
	cfg.HTTPClient = arrHTTP
	return prowlarr.NewAPIClient(cfg)
}

// whisparrClient builds a typed Whisparr client for one instance.
func whisparrClient(inst arrInstance) *whisparr.APIClient {
	cfg := whisparr.NewConfiguration()
	cfg.Servers = whisparr.ServerConfigurations{{URL: arrBaseURL(inst)}}
	cfg.AddDefaultHeader("X-Api-Key", inst.APIKey)
	cfg.HTTPClient = arrHTTP
	return whisparr.NewAPIClient(cfg)
}

func arrCtx() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 40*time.Second)
}

// poster URL helpers — MediaCover differs per package, so one each.
func sonarrPoster(imgs []sonarr.MediaCover) string {
	for _, im := range imgs {
		if string(im.GetCoverType()) == "poster" && im.GetRemoteUrl() != "" {
			return im.GetRemoteUrl()
		}
	}
	return ""
}

func radarrPoster(imgs []radarr.MediaCover) string {
	for _, im := range imgs {
		if string(im.GetCoverType()) == "poster" && im.GetRemoteUrl() != "" {
			return im.GetRemoteUrl()
		}
	}
	return ""
}

// arrSendRaw issues a raw method+body request to the *arr v3 API. The typed
// CommandAPI can't carry command-specific params (seriesId/episodeIds/seasonNumber),
// so commands and read-modify-write updates go through here. Returns (statusOK, body).
func arrSendRaw(inst arrInstance, method, path, body string) (bool, string) {
	ctx, cancel := arrCtx()
	defer cancel()
	var rdr io.Reader
	if body != "" {
		rdr = strings.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, arrBaseURL(inst)+"/api/v3/"+path, rdr)
	if err != nil {
		return false, err.Error()
	}
	req.Header.Set("X-Api-Key", inst.APIKey)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := arrHTTP.Do(req)
	if err != nil {
		return false, err.Error()
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode >= 200 && resp.StatusCode < 300, string(b)
}

// arrGetRaw fetches a *arr v3 endpoint as a raw JSON string (for the few read-
// modify-write flows that need the untyped body).
func arrGetRaw(inst arrInstance, path string) (bool, string) {
	return arrSendRaw(inst, http.MethodGet, path, "")
}

// ── typed file → arrFile mapping (shared by sonarr episode files + radarr movie files) ──

func mediaFrom(resolution, vcodec, dr, acodec, alang, subs, rt string, achan float64) *arrMedia {
	if vcodec == "" && resolution == "" && acodec == "" {
		return nil
	}
	return &arrMedia{
		Resolution: resolution, VideoCodec: vcodec, DynamicRange: dr, AudioCodec: acodec,
		AudioChannels: achan, AudioLanguages: alang, Subtitles: subs, Runtime: rt,
	}
}

func fillArrFile(a *arrFile, id int, relPath, fpath string, size int64, dateAdded time.Time, releaseGroup string, langs []string, quality string, mi *arrMedia) {
	a.FileID = id
	a.HasFile = true
	a.Quality = quality
	a.Size = size
	a.Path = relPath
	if a.Path == "" {
		a.Path = fpath
	}
	a.FullPath = fpath
	a.ReleaseGroup = releaseGroup
	if !dateAdded.IsZero() {
		a.DateAdded = dateAdded.Format(time.RFC3339)
	}
	a.Languages = strings.Join(langs, ", ")
	a.Media = mi
}

// seriesStatus maps a Sonarr series to an Overseerr-style availability code based
// on its episode-file statistics: 5 when every monitored aired episode is on disk,
// 4 when only some are, 3 when the series exists but has no files yet.
func seriesStatus(s *sonarr.SeriesResource) int {
	stat, ok := s.GetStatisticsOk()
	if !ok || stat == nil {
		return 3
	}
	fc := stat.GetEpisodeFileCount()
	ec := stat.GetEpisodeCount() // monitored + aired episodes
	switch {
	case fc <= 0:
		return 3
	case ec > 0 && fc >= ec:
		return 5
	default:
		return 4
	}
}

// arrSetImportsEnabled toggles each Sonarr/Radarr's "Completed Download Handling"
// (auto-import). Turning it off stops *arr from importing finished downloads into the
// media root while an upload is moving that root — without disabling the download
// clients (so grabbing/downloading carries on). enabled=false blocks imports.
func arrSetImportsEnabled(enabled bool) {
	for _, inst := range arrInstancesCached() {
		ctx, cancel := arrCtx()
		switch inst.Kind {
		case "sonarr":
			cl := sonarrClient(inst)
			if cfg, _, err := cl.DownloadClientConfigAPI.GetDownloadClientConfig(ctx).Execute(); err == nil && cfg.GetEnableCompletedDownloadHandling() != enabled {
				cfg.SetEnableCompletedDownloadHandling(enabled)
				_, _, _ = cl.DownloadClientConfigAPI.UpdateDownloadClientConfig(ctx, strconv.Itoa(int(cfg.GetId()))).DownloadClientConfigResource(*cfg).Execute()
			}
		case "radarr":
			cl := radarrClient(inst)
			if cfg, _, err := cl.DownloadClientConfigAPI.GetDownloadClientConfig(ctx).Execute(); err == nil && cfg.GetEnableCompletedDownloadHandling() != enabled {
				cfg.SetEnableCompletedDownloadHandling(enabled)
				_, _, _ = cl.DownloadClientConfigAPI.UpdateDownloadClientConfig(ctx, strconv.Itoa(int(cfg.GetId()))).DownloadClientConfigResource(*cfg).Execute()
			}
		}
		cancel()
	}
}

// arrImportsStatus reads back how many *arr instances currently have auto-import
// (Completed Download Handling) turned off — for the block self-test.
func arrImportsStatus() (blocked, total int) {
	for _, inst := range arrInstancesCached() {
		ctx, cancel := arrCtx()
		var cfg interface{ GetEnableCompletedDownloadHandling() bool }
		switch inst.Kind {
		case "sonarr":
			if c, _, err := sonarrClient(inst).DownloadClientConfigAPI.GetDownloadClientConfig(ctx).Execute(); err == nil {
				cfg = c
			}
		case "radarr":
			if c, _, err := radarrClient(inst).DownloadClientConfigAPI.GetDownloadClientConfig(ctx).Execute(); err == nil {
				cfg = c
			}
		}
		cancel()
		if cfg != nil {
			total++
			if !cfg.GetEnableCompletedDownloadHandling() {
				blocked++
			}
		}
	}
	return
}

// yearStr renders a non-zero year as a 4-digit string ("" for 0).
func yearStr(y int32) string {
	if y <= 0 {
		return ""
	}
	return strconv.Itoa(int(y))
}

// sonarrLibItems builds discover items straight from a Sonarr library (title, poster,
// rating, per-series availability) — far cheaper than fetching all of TMDb and
// filtering, when the user only wants in-library / partial titles.
func sonarrLibItems(inst arrInstance) []seerrItem {
	ctx, cancel := arrCtx()
	defer cancel()
	series, _, err := sonarrClient(inst).SeriesAPI.ListSeries(ctx).Execute()
	if err != nil {
		return nil
	}
	out := make([]seerrItem, 0, len(series))
	for i := range series {
		s := &series[i]
		tmdb := int(s.GetTmdbId())
		if tmdb == 0 {
			continue // detail view is tmdb-keyed
		}
		rat := s.GetRatings()
		out = append(out, seerrItem{
			MediaType: "tv", TmdbID: tmdb, Title: s.GetTitle(), Year: yearStr(s.GetYear()),
			Poster: sonarrPoster(s.GetImages()), Overview: s.GetOverview(),
			Vote: rat.GetValue(), Status: seriesStatus(s),
		})
	}
	return out
}

// radarrLibItems is the Radarr counterpart (movies are 5 = available when they have a file).
func radarrLibItems(inst arrInstance) []seerrItem {
	ctx, cancel := arrCtx()
	defer cancel()
	movies, _, err := radarrClient(inst).MovieAPI.ListMovie(ctx).Execute()
	if err != nil {
		return nil
	}
	out := make([]seerrItem, 0, len(movies))
	for i := range movies {
		m := &movies[i]
		tmdb := int(m.GetTmdbId())
		if tmdb == 0 {
			continue
		}
		st := 3
		if m.GetHasFile() {
			st = 5
		}
		vote := 0.0
		if r := m.GetRatings(); r.Tmdb != nil {
			tc := r.GetTmdb()
			vote = tc.GetValue()
		}
		out = append(out, seerrItem{
			MediaType: "movie", TmdbID: tmdb, Title: m.GetTitle(), Year: yearStr(m.GetYear()),
			Poster: radarrPoster(m.GetImages()), Overview: m.GetOverview(),
			Vote: vote, Status: st,
		})
	}
	return out
}

// sonarrSeasonStatus finds the matching series in any Sonarr instance and returns a
// per-season availability map (seasonNumber -> 0 missing · 4 partial · 5 complete)
// plus the overall series status. (nil, 0) when the series isn't in any Sonarr.
func sonarrSeasonStatus(tvdbID, tmdbID int) (map[int]int, int) {
	for _, inst := range arrInstancesCached() {
		if inst.Kind != "sonarr" {
			continue
		}
		ctx, cancel := arrCtx()
		r := sonarrClient(inst).SeriesAPI.ListSeries(ctx)
		if tvdbID > 0 {
			r = r.TvdbId(int32(tvdbID))
		}
		series, _, err := r.Execute()
		cancel()
		if err != nil {
			continue
		}
		for i := range series {
			if !(tvdbID > 0 && int(series[i].GetTvdbId()) == tvdbID) && !(tmdbID > 0 && int(series[i].GetTmdbId()) == tmdbID) {
				continue
			}
			m := map[int]int{}
			for _, sn := range series[i].GetSeasons() {
				st := 0
				if stat, ok := sn.GetStatisticsOk(); ok && stat != nil {
					fc := stat.GetEpisodeFileCount()
					ec := stat.GetEpisodeCount()
					switch {
					case fc <= 0:
						st = 0
					case ec > 0 && fc >= ec:
						st = 5
					default:
						st = 4
					}
				}
				m[int(sn.GetSeasonNumber())] = st
			}
			return m, seriesStatus(&series[i])
		}
	}
	return nil, 0
}

func applySonarrFile(a *arrFile, f sonarr.EpisodeFileResource) {
	mi := f.GetMediaInfo()
	qm := f.GetQuality()
	ql := qm.GetQuality()
	var langs []string
	for _, l := range f.GetLanguages() {
		if l.GetName() != "" {
			langs = append(langs, l.GetName())
		}
	}
	fillArrFile(a, int(f.GetId()), f.GetRelativePath(), f.GetPath(), f.GetSize(), f.GetDateAdded(),
		f.GetReleaseGroup(), langs, ql.GetName(),
		mediaFrom(mi.GetResolution(), mi.GetVideoCodec(), mi.GetVideoDynamicRange(), mi.GetAudioCodec(),
			mi.GetAudioLanguages(), mi.GetSubtitles(), mi.GetRunTime(), mi.GetAudioChannels()))
}

func applyRadarrFile(a *arrFile, f radarr.MovieFileResource) {
	mi := f.GetMediaInfo()
	qm := f.GetQuality()
	ql := qm.GetQuality()
	var langs []string
	for _, l := range f.GetLanguages() {
		if l.GetName() != "" {
			langs = append(langs, l.GetName())
		}
	}
	fillArrFile(a, int(f.GetId()), f.GetRelativePath(), f.GetPath(), f.GetSize(), f.GetDateAdded(),
		f.GetReleaseGroup(), langs, ql.GetName(),
		mediaFrom(mi.GetResolution(), mi.GetVideoCodec(), mi.GetVideoDynamicRange(), mi.GetAudioCodec(),
			mi.GetAudioLanguages(), mi.GetSubtitles(), mi.GetRunTime(), mi.GetAudioChannels()))
}
