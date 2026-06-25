package api

import (
	"context"
	"io"
	"net/http"
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
