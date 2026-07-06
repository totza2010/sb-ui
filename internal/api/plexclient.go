package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"
	"sync"
	"time"

	plexgo "github.com/LukeHagar/plexgo"
	"github.com/LukeHagar/plexgo/models/components"
	"github.com/LukeHagar/plexgo/models/operations"
)

// plexgo (github.com/LukeHagar/plexgo) is the primary Plex client — it won the
// connection bake-off and serves items / episodes / sessions / identity. Two
// deliberate, documented exceptions:
//
//  1. SECTIONS LIST — plexgo CODEGEN BUG (v0.28.6): the spec (and Plex) define
//     `hidden` as an INTEGER (`"hidden": 123` in the official example; `"hidden":0`
//     live), but plexgo's generated LibrarySection mistypes it as `Hidden *bool`
//     (librarysection.go:138). So decoding the (correct) integer fails with "cannot
//     unmarshal number into Go value of type bool". Plex/the spec are right — plexgo's
//     Go type is wrong. We do NOT convert int→bool (lossy, and we never use `hidden`);
//     instead we read /library/sections with a minimal raw parse of only the fields we
//     need (key/title/type/Location.path — all strings), skipping `hidden` entirely.
//
//  2. TARGETED SCAN — done with our own autoplow-style GET .../refresh?path=
//     (plexScan) for full control over per-file/folder scanning.

func plexAPI(cfg plexConfig) *plexgo.PlexAPI {
	return plexgo.New(
		plexgo.WithServerURL(cfg.URL),
		plexgo.WithSecurity(cfg.Token),
		plexgo.WithClient(arrHTTP),
		// JSON so the item models' custom UnmarshalJSON path is used.
		plexgo.WithAccepts(components.AcceptsApplicationJSON),
	)
}

func plexCtx() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 60*time.Second)
}

// mcMetadata pulls the item list out of plexgo's shared metadata container.
func mcMetadata(m *components.MediaContainerWithMetadata) []components.Metadata {
	if m == nil || m.MediaContainer == nil {
		return nil
	}
	return m.MediaContainer.Metadata
}

// ── raw HTTP (only for the two documented plexgo exceptions above) ──

func plexRawGET(cfg plexConfig, p string) ([]byte, error) {
	u := strings.TrimRight(cfg.URL, "/") + p
	if strings.Contains(p, "?") {
		u += "&"
	} else {
		u += "?"
	}
	u += "X-Plex-Token=" + url.QueryEscape(cfg.Token)
	ctx, cancel := plexCtx()
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := arrHTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body[:min(len(body), 300)])))
	}
	return body, nil
}

func plexRawJSON(cfg plexConfig, p string, v any) error {
	body, err := plexRawGET(cfg, p)
	if err != nil {
		return err
	}
	return json.Unmarshal(body, v)
}

// ── sections (plexgo GetSections bug workaround — see file header) ──

func plexSections(cfg plexConfig) []plexSection {
	secs, _ := plexSectionsErr(cfg)
	return secs
}

func plexSectionsErr(cfg plexConfig) ([]plexSection, error) {
	if cfg.URL == "" {
		return nil, fmt.Errorf("Plex URL not set")
	}
	var r struct {
		MediaContainer struct {
			Directory []struct {
				Key      string `json:"key"`
				Title    string `json:"title"`
				Type     string `json:"type"`
				Location []struct {
					Path string `json:"path"`
				} `json:"Location"`
			} `json:"Directory"`
		} `json:"MediaContainer"`
	}
	if err := plexRawJSON(cfg, "/library/sections", &r); err != nil {
		return nil, err
	}
	var out []plexSection
	for _, d := range r.MediaContainer.Directory {
		s := plexSection{Key: d.Key, Title: d.Title, Type: d.Type}
		for _, l := range d.Location {
			if l.Path != "" {
				s.Locations = append(s.Locations, l.Path)
			}
		}
		out = append(out, s)
	}
	return out, nil
}

// plexLibInfo is one Plex library's summary (Integrations page).
type plexLibInfo struct {
	Title     string   `json:"title"`
	Type      string   `json:"type"`
	Count     int      `json:"count"`
	Locations []string `json:"locations,omitempty"`
}

// plexLibraries returns each library with its item count, counted via plexgo
// ListContent (size=0 returns just the container totals).
func plexLibraries(cfg plexConfig) ([]plexLibInfo, error) {
	secs, err := plexSectionsErr(cfg)
	if err != nil {
		return nil, err
	}
	api := plexAPI(cfg)
	out := make([]plexLibInfo, 0, len(secs))
	for _, s := range secs {
		li := plexLibInfo{Title: s.Title, Type: s.Type, Locations: s.Locations}
		ctx, cancel := plexCtx()
		res, cerr := api.Content.ListContent(ctx, operations.ListContentRequest{
			SectionID:          s.Key,
			XPlexContainerSize: plexgo.Pointer(0),
		})
		cancel()
		if cerr == nil && res.MediaContainerWithMetadata != nil && res.MediaContainerWithMetadata.MediaContainer != nil {
			mc := res.MediaContainerWithMetadata.MediaContainer
			if mc.TotalSize != nil {
				li.Count = int(*mc.TotalSize)
			} else if mc.Size != nil {
				li.Count = int(*mc.Size)
			}
		}
		out = append(out, li)
	}
	return out, nil
}

// plexActiveStreams returns the number of current playback sessions (plexgo).
func plexActiveStreams(cfg plexConfig) int {
	if cfg.URL == "" {
		return 0
	}
	ctx, cancel := plexCtx()
	defer cancel()
	res, err := plexAPI(cfg).Status.ListSessions(ctx)
	if err != nil || res.Object == nil || res.Object.MediaContainer == nil {
		return 0
	}
	if res.Object.MediaContainer.Size != nil {
		return int(*res.Object.MediaContainer.Size)
	}
	return len(res.Object.MediaContainer.Metadata)
}

// ── targeted scan (our own autoplow-style code — plexgo route not used here) ──

// plexScan triggers a Plex scan of one path within a section (empty path = whole
// section). Autoplow-style: GET /library/sections/{id}/refresh?path=<path>.
func plexScan(cfg plexConfig, sectionKey, plexPath string) error {
	p := "/library/sections/" + sectionKey + "/refresh"
	if plexPath != "" {
		p += "?path=" + url.QueryEscape(plexPath)
	}
	_, err := plexRawGET(cfg, p)
	return err
}

func plexRefreshPath(cfg plexConfig, sectionKey, plexPath string) error {
	return plexScan(cfg, sectionKey, plexPath)
}

// plexRefreshAll scans every section (post-upload).
func plexRefreshAll(cfg plexConfig) {
	for _, s := range plexSections(cfg) {
		_ = plexScan(cfg, s.Key, "")
	}
}

// plexSectionScanning reports whether Plex currently has a library scan running for
// the section (or any library scan when Plex omits the section id). Used to detect
// scan completion — GET /activities lists in-progress tasks.
func plexSectionScanning(cfg plexConfig, sectionKey string) bool {
	var r struct {
		MediaContainer struct {
			Activity []struct {
				Type    string `json:"type"`
				Context struct {
					LibrarySectionID string `json:"librarySectionID"`
				} `json:"Context"`
			} `json:"Activity"`
		} `json:"MediaContainer"`
	}
	if plexRawJSON(cfg, "/activities", &r) != nil {
		return false
	}
	for _, a := range r.MediaContainer.Activity {
		if strings.HasPrefix(a.Type, "library.") &&
			(a.Context.LibrarySectionID == "" || a.Context.LibrarySectionID == sectionKey) {
			return true
		}
	}
	return false
}

// plexSectionForPath returns the section whose root location is the longest prefix
// of the given (Plex-side) path.
func plexSectionForPath(cfg plexConfig, p string) (string, bool) {
	best, key := -1, ""
	for _, s := range plexSections(cfg) {
		for _, loc := range s.Locations {
			if loc != "" && len(loc) > best && strings.HasPrefix(p, loc) {
				best, key = len(loc), s.Key
			}
		}
	}
	return key, best >= 0
}

// ── items / episodes (plexgo — the primary client) ──

// plexMediaIDs collects tvdb/tmdb ids from every Plex section via plexgo
// Content.ListContent (includeGuids = /library/sections/{key}/all?includeGuids=1).
func plexMediaIDs() plexIDSet {
	set := plexIDSet{Tvdb: map[string]bool{}, Tmdb: map[string]bool{}, ShowKey: map[string]string{}}
	cfg := loadOptions().Plex
	if cfg.URL == "" {
		return set
	}
	api := plexAPI(cfg)
	for _, s := range plexSections(cfg) {
		ctx, cancel := plexCtx()
		res, err := api.Content.ListContent(ctx, operations.ListContentRequest{
			SectionID:           s.Key,
			IncludeGuids:        components.BoolIntTrue.ToPointer(),
			XPlexContainerStart: plexgo.Pointer(0),
			XPlexContainerSize:  plexgo.Pointer(100000),
		})
		cancel()
		if err != nil {
			continue
		}
		for _, m := range mcMetadata(res.MediaContainerWithMetadata) {
			itemTvdb := ""
			for _, g := range m.Guids {
				addPlexIDs(&set, g.GetID())
				if mm := plexTvdbRE.FindStringSubmatch(g.GetID()); mm != nil {
					itemTvdb = mm[1]
				}
			}
			for _, md := range m.Media {
				for _, pt := range md.Part {
					if pt.File != nil {
						addPlexIDs(&set, *pt.File)
					}
				}
			}
			if s.Type == "show" && itemTvdb != "" && m.RatingKey != nil {
				set.ShowKey[itemTvdb] = *m.RatingKey
			}
		}
	}
	return set
}

// plexShowEpisodeBasenames returns the basenames of every episode file Plex has for
// a show (by tvdb id), via plexgo GetAllItemLeaves on the show's ratingKey.
func plexShowEpisodeBasenames(tvdb string) map[string]bool {
	out := map[string]bool{}
	key := plexIDsCached().ShowKey[tvdb]
	if key == "" {
		return out
	}
	ctx, cancel := plexCtx()
	defer cancel()
	res, err := plexAPI(loadOptions().Plex).Library.GetAllItemLeaves(ctx, operations.GetAllItemLeavesRequest{Ids: key})
	if err != nil {
		return out
	}
	for _, m := range mcMetadata(res.MediaContainerWithMetadata) {
		for _, md := range m.Media {
			for _, pt := range md.Part {
				if pt.File != nil && *pt.File != "" {
					out[path.Base(*pt.File)] = true
				}
			}
		}
	}
	return out
}

// ── transcode limiting (uploader: free CPU/disk for the upload) ──────────────────

// plexKillTranscodes terminates every active *transcoding* session (direct-play
// streams are left alone), returning how many it stopped.
func plexKillTranscodes(cfg plexConfig) int {
	if cfg.URL == "" {
		return 0
	}
	var r struct {
		MediaContainer struct {
			Metadata []struct {
				Session struct {
					ID string `json:"id"`
				} `json:"Session"`
				TranscodeSession *struct {
					VideoDecision string `json:"videoDecision"`
					AudioDecision string `json:"audioDecision"`
				} `json:"TranscodeSession"`
			} `json:"Metadata"`
		} `json:"MediaContainer"`
	}
	if plexRawJSON(cfg, "/status/sessions", &r) != nil {
		return 0
	}
	killed := 0
	for _, m := range r.MediaContainer.Metadata {
		if m.TranscodeSession == nil || m.Session.ID == "" {
			continue // direct play (no transcode) → leave it alone
		}
		if m.TranscodeSession.VideoDecision != "transcode" && m.TranscodeSession.AudioDecision != "transcode" {
			continue // direct stream (copy) → not a real transcode
		}
		_, _ = plexRawGET(cfg, "/status/sessions/terminate?sessionId="+url.QueryEscape(m.Session.ID)+
			"&reason="+url.QueryEscape("Server is uploading — transcoding paused, please retry shortly"))
		killed++
	}
	return killed
}

// plexTranscodeCount counts active transcoding sessions (for the block self-test).
func plexTranscodeCount(cfg plexConfig) int {
	if cfg.URL == "" {
		return 0
	}
	var r struct {
		MediaContainer struct {
			Metadata []struct {
				TranscodeSession *struct {
					VideoDecision string `json:"videoDecision"`
					AudioDecision string `json:"audioDecision"`
				} `json:"TranscodeSession"`
			} `json:"Metadata"`
		} `json:"MediaContainer"`
	}
	if plexRawJSON(cfg, "/status/sessions", &r) != nil {
		return 0
	}
	n := 0
	for _, m := range r.MediaContainer.Metadata {
		if m.TranscodeSession != nil && (m.TranscodeSession.VideoDecision == "transcode" || m.TranscodeSession.AudioDecision == "transcode") {
			n++
		}
	}
	return n
}

// A background killer keeps terminating transcodes for the whole upload (new ones can
// start mid-run), started on apply and stopped on restore.
var (
	plexKillMu   sync.Mutex
	plexKillStop chan struct{}
)

func startPlexTranscodeKill(cfg plexConfig) {
	plexKillMu.Lock()
	defer plexKillMu.Unlock()
	if plexKillStop != nil {
		return // already running
	}
	stop := make(chan struct{})
	plexKillStop = stop
	go func() {
		for {
			plexKillTranscodes(cfg)
			select {
			case <-stop:
				return
			case <-time.After(20 * time.Second):
			}
		}
	}()
}

func stopPlexTranscodeKill() {
	plexKillMu.Lock()
	defer plexKillMu.Unlock()
	if plexKillStop != nil {
		close(plexKillStop)
		plexKillStop = nil
	}
}
