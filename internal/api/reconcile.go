package api

import (
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// arr ↔ Plex reconcile, modelled on autoplow's matcharr.
//
// The join key is the PATH (not the provider id, the way the Library's in-Plex badge
// works). That inversion is the whole point: matching on ids alone can't see that a title
// moved, because Plex still holds the same id — it just points at the old folder. Keying
// on paths splits the world into three useful buckets:
//
//	mismatch    — same path, disagreeing ids   → Plex matched the folder to the wrong title
//	missingArr  — arr has files, Plex has no item at that path → Plex never scanned it
//	missingSrv  — Plex holds a path arr no longer knows        → stale entry
//
// A moved root folder produces a missingArr (the new path) and a missingSrv (the old one)
// for the same title, so pairing them by provider id recovers both paths to rescan — and
// it does so statelessly, which means moves that happened while we weren't running are
// still caught.

// arrEntry is the reconcile-focused view of one arr item: just the path and the ids. The
// Library builds a much richer object for display; this stays deliberately narrow.
type arrEntry struct {
	Kind     string `json:"kind"` // sonarr | radarr
	Instance string `json:"instance"`
	ItemID   int    `json:"item_id"`
	Title    string `json:"title"`
	Folder   string `json:"folder"`
	HasFile  bool   `json:"has_file"`
	ExtID    string `json:"ext_id"` // tvdb (sonarr) / tmdb (radarr) — lets the UI drill to per-episode Plex state
	IDs      map[string][]string
}

// extID returns the id the item is keyed by, for the per-file Plex check in /arr/files.
func (e arrEntry) extID() string {
	if v := e.IDs[primaryIDType(e.Kind)]; len(v) > 0 {
		return v[0]
	}
	return ""
}

// arrEntries inventories every configured Sonarr/Radarr instance.
func arrEntries() []arrEntry {
	var mu sync.Mutex
	var out []arrEntry
	var wg sync.WaitGroup
	for _, inst := range arrInstancesCached() {
		wg.Add(1)
		go func(inst arrInstance) {
			defer wg.Done()
			ctx, cancel := arrCtx()
			defer cancel()
			arrSem <- struct{}{}
			defer func() { <-arrSem }()
			if inst.Kind == "sonarr" {
				series, _, err := sonarrClient(inst).SeriesAPI.ListSeries(ctx).Execute()
				if err != nil {
					return
				}
				local := make([]arrEntry, 0, len(series))
				for i := range series {
					s := series[i]
					st := s.GetStatistics()
					ids := map[string][]string{}
					if v := s.GetTvdbId(); v > 0 {
						addProviderID(ids, "tvdb", strconv.Itoa(int(v)))
					}
					if v := s.GetImdbId(); v != "" {
						addProviderID(ids, "imdb", v)
					}
					if v := s.GetTmdbId(); v > 0 {
						addProviderID(ids, "tmdb", strconv.Itoa(int(v)))
					}
					local = append(local, arrEntry{
						Kind: "sonarr", Instance: inst.Name, ItemID: int(s.GetId()), Title: s.GetTitle(),
						Folder: s.GetPath(), HasFile: st.GetEpisodeFileCount() > 0, IDs: ids,
					})
				}
				mu.Lock()
				out = append(out, local...)
				mu.Unlock()
				return
			}
			movies, _, err := radarrClient(inst).MovieAPI.ListMovie(ctx).Execute()
			if err != nil {
				return
			}
			local := make([]arrEntry, 0, len(movies))
			for i := range movies {
				m := movies[i]
				ids := map[string][]string{}
				if v := m.GetTmdbId(); v > 0 {
					addProviderID(ids, "tmdb", strconv.Itoa(int(v)))
				}
				if v := m.GetImdbId(); v != "" {
					addProviderID(ids, "imdb", v)
				}
				local = append(local, arrEntry{
					Kind: "radarr", Instance: inst.Name, ItemID: int(m.GetId()), Title: m.GetTitle(),
					Folder: m.GetPath(), HasFile: m.GetHasFile(), IDs: ids,
				})
			}
			mu.Lock()
			out = append(out, local...)
			mu.Unlock()
		}(inst)
	}
	wg.Wait()
	return out
}

type recMismatch struct {
	Arr      arrEntry `json:"arr"`
	PlexKey  string   `json:"plex_key"`
	PlexName string   `json:"plex_title"`
	Path     string   `json:"path"`
	Expected string   `json:"expected"`
	Actual   string   `json:"actual"`
}

type recMove struct {
	Title    string `json:"title"`
	Kind     string `json:"kind"`
	Instance string `json:"instance"`
	ItemID   int    `json:"item_id"`
	ExtID    string `json:"ext_id"` // lets the UI drill in and scan one episode
	From     string `json:"from"`   // Plex's stale path
	To       string `json:"to"`     // arr's current path
}

type recPlexEntry struct {
	Title   string `json:"title"`
	Path    string `json:"path"`
	Section string `json:"section"`
}

type reconcileResult struct {
	Compared   int            `json:"compared"`
	Mismatches []recMismatch  `json:"mismatches"`
	MissingArr []arrEntry     `json:"missing_arr"`
	MissingSrv []recPlexEntry `json:"missing_srv"`
	Moves      []recMove      `json:"moves"`
}

// primaryIDType is the id an arr instance is authoritative for.
func primaryIDType(kind string) string {
	if kind == "sonarr" {
		return "tvdb"
	}
	return "tmdb"
}

// idsAgree reports whether the Plex item carries the arr item's identity. The primary id
// wins; when Plex simply doesn't publish that provider (older agents often expose only
// one), imdb and then the other provider are accepted as evidence rather than reporting a
// false mismatch.
func idsAgree(kind string, arrIDs, plexIDs map[string][]string) bool {
	primary := primaryIDType(kind)
	if overlaps(arrIDs[primary], plexIDs[primary]) {
		return true
	}
	if overlaps(arrIDs["imdb"], plexIDs["imdb"]) {
		return true
	}
	other := "tmdb"
	if primary == "tmdb" {
		other = "tvdb"
	}
	return overlaps(arrIDs[other], plexIDs[other])
}

func overlaps(a, b []string) bool {
	for _, x := range a {
		for _, y := range b {
			if x != "" && x == y {
				return true
			}
		}
	}
	return false
}

// sharesAnyID is the looser test used to pair a moved title's two halves.
func sharesAnyID(a, b map[string][]string) bool {
	for _, k := range []string{"tvdb", "tmdb", "imdb"} {
		if overlaps(a[k], b[k]) {
			return true
		}
	}
	return false
}

// reconcile compares the arr inventory against the Plex inventory by path.
func reconcile(entries []arrEntry, items []plexItem) reconcileResult {
	res := reconcileResult{Mismatches: []recMismatch{}, MissingArr: []arrEntry{}, MissingSrv: []recPlexEntry{}, Moves: []recMove{}}
	byPath := map[string]*plexItem{}
	for i := range items {
		if p := items[i].matchPath(); p != "" {
			byPath[p] = &items[i]
		}
	}
	matched := map[string]bool{}

	for _, e := range entries {
		if e.Folder == "" {
			continue
		}
		p := normPath(mapArrPath(e.Folder))
		it := byPath[p]
		if it == nil {
			if e.HasFile { // no files yet → Plex is right not to have it
				res.MissingArr = append(res.MissingArr, e)
			}
			continue
		}
		matched[p] = true
		res.Compared++
		if idsAgree(e.Kind, e.IDs, it.IDs) {
			continue
		}
		res.Mismatches = append(res.Mismatches, recMismatch{
			Arr: e, PlexKey: it.RatingKey, PlexName: it.Title, Path: p,
			Expected: primaryIDType(e.Kind) + ":" + strings.Join(e.IDs[primaryIDType(e.Kind)], ","),
			Actual:   flattenIDs(it.IDs),
		})
	}

	for i := range items {
		p := items[i].matchPath()
		if p == "" || matched[p] {
			continue
		}
		res.MissingSrv = append(res.MissingSrv, recPlexEntry{Title: items[i].Title, Path: items[i].Path, Section: items[i].Section})
	}

	res.Moves = pairMoves(res.MissingArr, items, matched)
	return res
}

// pairMoves matches each unscanned arr path to a stale Plex path for the same title —
// that pair is a move, and both paths need a scan (the old one so Plex drops it, the new
// one so Plex picks it up).
func pairMoves(missingArr []arrEntry, items []plexItem, matched map[string]bool) []recMove {
	moves := []recMove{}
	used := map[string]bool{}
	for _, e := range missingArr {
		for i := range items {
			p := items[i].matchPath()
			if p == "" || matched[p] || used[p] {
				continue
			}
			if !sharesAnyID(e.IDs, items[i].IDs) {
				continue
			}
			used[p] = true
			moves = append(moves, recMove{
				Title: e.Title, Kind: e.Kind, Instance: e.Instance, ItemID: e.ItemID, ExtID: e.extID(),
				From: items[i].Path, To: e.Folder,
			})
			break
		}
	}
	return moves
}

// reconcileRun does a live reconcile; when fix is set it also queues a scan of every
// moved title's old and new path, which is what makes Plex drop the stale entry and pick
// the media up at its new home.
func reconcileRun(fix bool) (reconcileResult, []plexItem, int) {
	items := plexItems(loadOptions().Plex)
	res := reconcile(arrEntries(), items)
	if !fix || len(res.Moves) == 0 {
		return res, items, 0
	}
	paths := make([]string, 0, len(res.Moves)*2)
	for _, m := range res.Moves {
		paths = append(paths, m.From, mapArrPath(m.To))
	}
	return res, items, autoscanSvc().Enqueue("reconcile", "move", paths...)
}

// missingRoot summarises unmatched arr items by their parent root. Hundreds of individual
// rows are noise; when a whole root is unmatched the real cause is almost always that the
// root isn't a Plex library or needs a path mapping, which only shows up when grouped.
type missingRoot struct {
	Kind      string     `json:"kind"`
	Instance  string     `json:"instance"`
	Root      string     `json:"root"`
	Count     int        `json:"count"`
	Section   string     `json:"section"`    // Plex library covering this root ("" = none)
	Covered   bool       `json:"covered"`    // false → scanning can never work here
	SecCount  int        `json:"sec_count"`  // items that library currently holds
	SecNested bool       `json:"sec_nested"` // its root sits inside another library's root
	Items     []arrEntry `json:"items"`      // so the UI can drill in and scan a single episode
}

// sectionHealth summarises why scanning a library may be futile: an empty library, or
// one nested inside another (Plex lets the outer library claim the shared subtree, so the
// inner one stays empty and targeted scans do nothing).
func sectionHealth(cfg plexConfig, items []plexItem) (count map[string]int, nested map[string]bool) {
	count, nested = map[string]int{}, map[string]bool{}
	for _, it := range items {
		count[it.Section]++
	}
	secs := plexSections(cfg)
	for _, a := range secs {
		for _, la := range a.Locations {
			la = strings.TrimRight(la, "/")
			for _, b := range secs {
				if b.Key == a.Key {
					continue
				}
				for _, lb := range b.Locations {
					if lb = strings.TrimRight(lb, "/"); lb != "" && strings.HasPrefix(la, lb+"/") {
						nested[a.Key] = true
					}
				}
			}
		}
	}
	return count, nested
}

func groupMissingRoots(entries []arrEntry, items []plexItem) []missingRoot {
	type key struct{ kind, inst, root string }
	idx := map[key]*missingRoot{}
	order := []key{}
	for _, e := range entries {
		e.ExtID = e.extID()
		root := parentDir(normPath(e.Folder))
		k := key{e.Kind, e.Instance, root}
		g := idx[k]
		if g == nil {
			g = &missingRoot{Kind: e.Kind, Instance: e.Instance, Root: root}
			idx[k] = g
			order = append(order, k)
		}
		g.Count++
		if len(g.Items) < 500 { // keep the payload sane on very large libraries
			g.Items = append(g.Items, e)
		}
	}
	// Flag roots no Plex library covers. Those can't be fixed by scanning at all — the
	// library is missing or needs a path mapping — so the UI can say that instead of
	// offering a button that only ever fails.
	cfg := loadOptions().Plex
	secCount, secNested := sectionHealth(cfg, items)
	out := make([]missingRoot, 0, len(idx))
	for _, k := range order {
		g := idx[k]
		sort.Slice(g.Items, func(i, j int) bool { return strings.ToLower(g.Items[i].Title) < strings.ToLower(g.Items[j].Title) })
		if cfg.URL != "" {
			key, title, ok := plexSectionForPath(cfg, mapArrPath(g.Root))
			g.Section, g.Covered = title, ok
			g.SecCount, g.SecNested = secCount[key], secNested[key]
		}
		out = append(out, *g)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Count > out[j].Count })
	return out
}

func reconcileHandler(w http.ResponseWriter, req *http.Request) {
	res, items, queued := reconcileRun(req.URL.Query().Get("fix") == "1")
	writeJSON(w, http.StatusOK, map[string]any{
		"compared": res.Compared, "mismatches": res.Mismatches,
		"missing_arr": len(res.MissingArr), "missing_roots": groupMissingRoots(res.MissingArr, items),
		"missing_srv": res.MissingSrv, "moves": res.Moves, "queued": queued,
	})
}

func flattenIDs(ids map[string][]string) string {
	parts := make([]string, 0, len(ids))
	for _, k := range []string{"tvdb", "tmdb", "imdb"} {
		if v := ids[k]; len(v) > 0 {
			parts = append(parts, k+":"+strings.Join(v, ","))
		}
	}
	return strings.Join(parts, " ")
}
