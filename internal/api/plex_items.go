package api

import (
	"strings"

	plexgo "github.com/LukeHagar/plexgo"
	"github.com/LukeHagar/plexgo/models/components"
	"github.com/LukeHagar/plexgo/models/operations"
)

// Per-item Plex inventory (path + provider ids) — the input to the arr↔Plex reconcile.
//
// Modelled on autoplow's matcharr: the PATH is the join key and the provider ids are the
// payload we verify. That inversion (we used to match on ids alone) is what makes a moved
// folder visible: it shows up as a Plex item at a path arr no longer knows, plus an arr
// path Plex has never seen. See reconcile.go.
//
// Paths come from two places because Plex only inlines them for movies:
//   - movies: Media[].Part[].File, present in the section listing
//   - shows:  Location[].Path, which the listing omits → batched /library/metadata/{ids}

type plexItem struct {
	RatingKey string
	Title     string
	Section   string // section key
	SecType   string // "movie" | "show"
	Path      string
	IsFile    bool                // Path is a file (movies) → compare its parent folder
	IDs       map[string][]string // "tvdb"/"tmdb"/"imdb" → values (an item can carry several)
}

// matchPath is the key both sides of the reconcile are indexed by: the containing folder,
// without a trailing slash.
func (it plexItem) matchPath() string {
	p := normPath(it.Path)
	if p == "" {
		return ""
	}
	if it.IsFile {
		return normPath(parentDir(p))
	}
	return p
}

func normPath(p string) string {
	p = strings.TrimRight(strings.TrimSpace(p), "/")
	return p
}

func parentDir(p string) string {
	if i := strings.LastIndexByte(p, '/'); i > 0 {
		return p[:i]
	}
	return p
}

// addProviderID records a provider id, de-duplicated.
func addProviderID(ids map[string][]string, kind, val string) {
	val = strings.TrimSpace(val)
	if kind == "" || val == "" {
		return
	}
	for _, v := range ids[kind] {
		if v == val {
			return
		}
	}
	ids[kind] = append(ids[kind], val)
}

// parseProviderID pulls a provider id out of a Plex GUID.
//
// Both shapes matter: the modern agent emits "tvdb://12345", while the legacy agents emit
// "com.plexapp.agents.thetvdb://12345?lang=en". Note that legacy TMDB is spelled
// "themoviedb", which contains no "tmdb" — a plain tmdb regex silently misses every
// legacy-agent movie, so the agent names are matched explicitly.
func parseProviderID(guid string, ids map[string][]string) {
	g := strings.TrimSpace(guid)
	if g == "" {
		return
	}
	take := func(sep string) string {
		_, rest, ok := strings.Cut(g, sep)
		if !ok {
			return ""
		}
		return strings.SplitN(rest, "?", 2)[0]
	}
	switch {
	case strings.Contains(g, "themoviedb://"):
		addProviderID(ids, "tmdb", take("themoviedb://"))
	case strings.Contains(g, "thetvdb://"):
		addProviderID(ids, "tvdb", take("thetvdb://"))
	case strings.HasPrefix(g, "tmdb://"):
		addProviderID(ids, "tmdb", take("tmdb://"))
	case strings.HasPrefix(g, "tvdb://"):
		addProviderID(ids, "tvdb", take("tvdb://"))
	case strings.Contains(g, "imdb://"): // covers both "imdb://" and the legacy agent
		addProviderID(ids, "imdb", take("imdb://"))
	default:
		// Fall back to the loose forms that show up inside file/folder names,
		// e.g. "Show {tvdb-368166}".
		if m := plexTvdbRE.FindStringSubmatch(g); m != nil {
			addProviderID(ids, "tvdb", m[1])
		}
		if m := plexTmdbRE.FindStringSubmatch(g); m != nil {
			addProviderID(ids, "tmdb", m[1])
		}
	}
}

// plexItems inventories every movie/show section with its path and provider ids.
func plexItems(cfg plexConfig) []plexItem {
	if cfg.URL == "" {
		return nil
	}
	api := plexAPI(cfg)
	var out []plexItem
	for _, s := range plexSections(cfg) {
		if s.Type != "movie" && s.Type != "show" {
			continue
		}
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
		start := len(out)
		for _, m := range mcMetadata(res.MediaContainerWithMetadata) {
			it := plexItem{Title: m.Title, Section: s.Key, SecType: s.Type, IDs: map[string][]string{}}
			if m.RatingKey != nil {
				it.RatingKey = *m.RatingKey
			}
			for _, g := range m.Guids {
				parseProviderID(g.GetID(), it.IDs)
			}
			for _, md := range m.Media { // movies carry their file here
				for _, pt := range md.Part {
					if pt.File != nil && *pt.File != "" {
						it.Path, it.IsFile = *pt.File, true
						parseProviderID(*pt.File, it.IDs) // ids embedded in the filename
						break
					}
				}
				if it.Path != "" {
					break
				}
			}
			out = append(out, it)
		}
		if s.Type == "show" { // shows need the folder fetched separately
			keys := make([]string, 0, len(out)-start)
			for _, it := range out[start:] {
				if it.RatingKey != "" {
					keys = append(keys, it.RatingKey)
				}
			}
			locs := plexShowLocations(cfg, keys)
			for i := start; i < len(out); i++ {
				if p, ok := locs[out[i].RatingKey]; ok && p != "" {
					out[i].Path, out[i].IsFile = p, false
					parseProviderID(p, out[i].IDs)
				}
			}
		}
	}
	return out
}

// plexShowLocations batch-fetches show folder paths — Plex omits Location from the section
// listing, but /library/metadata accepts a comma-separated list of rating keys.
func plexShowLocations(cfg plexConfig, ratingKeys []string) map[string]string {
	const batch = 100
	out := map[string]string{}
	for i := 0; i < len(ratingKeys); i += batch {
		end := min(i+batch, len(ratingKeys))
		var r struct {
			MediaContainer struct {
				Metadata []struct {
					RatingKey string `json:"ratingKey"`
					Location  []struct {
						Path string `json:"path"`
					} `json:"Location"`
				} `json:"Metadata"`
			} `json:"MediaContainer"`
		}
		if err := plexRawJSON(cfg, "/library/metadata/"+strings.Join(ratingKeys[i:end], ","), &r); err != nil {
			continue // best-effort: a failed batch just leaves those shows without a path
		}
		for _, m := range r.MediaContainer.Metadata {
			if len(m.Location) > 0 && m.Location[0].Path != "" {
				out[m.RatingKey] = m.Location[0].Path
			}
		}
	}
	return out
}
