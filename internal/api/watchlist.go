package api

import (
	"encoding/json"
	"net/http"

	"sb-ui/internal/store"
)

// Watchlist — a simple sb-ui-stored list of titles the user flagged (no Plex/Seerr
// dependency). Each entry carries enough to display without re-fetching from TMDb.

const watchlistRel = "cache/watchlist.json"

func loadWatchlist() []seerrItem {
	var wl []seerrItem
	store.ReadJSON(watchlistRel, &wl)
	if wl == nil {
		wl = []seerrItem{}
	}
	return wl
}

// getWatchlist returns the watchlist, refreshing in-library status from the *arr apps.
func getWatchlist(w http.ResponseWriter, _ *http.Request) {
	items := loadWatchlist()
	lib := arrLibIDs()
	for i := range items {
		items[i].Status = lib.Tmdb[items[i].TmdbID]
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

// toggleWatchlist adds the title if absent, removes it if present.
func toggleWatchlist(w http.ResponseWriter, req *http.Request) {
	var b seerrItem
	if json.NewDecoder(req.Body).Decode(&b) != nil || b.TmdbID == 0 || (b.MediaType != "movie" && b.MediaType != "tv") {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	b.Backdrop = "" // don't store heavy hero art
	list := loadWatchlist()
	out := make([]seerrItem, 0, len(list)+1)
	found := false
	for _, it := range list {
		if it.MediaType == b.MediaType && it.TmdbID == b.TmdbID {
			found = true
			continue // drop (remove)
		}
		out = append(out, it)
	}
	action := "removed"
	if !found {
		out = append([]seerrItem{b}, out...) // newest first
		action = "added"
	}
	store.WriteJSON(watchlistRel, out)
	writeJSON(w, http.StatusOK, map[string]any{"action": action})
}
