package api

// *arr webhook parsers — one autoscan webhook endpoint, many apps. Each parser
// decodes the payload into its own shape and, if its identifying object is present,
// returns the folders Plex should scan for the event (mirrors Cloudbox/autoscan's
// per-app triggers). Adding a new *arr = add one parser to arrParsers.

import (
	"encoding/json"
	"path"
	"strings"
)

// arrScan is a parsed webhook: which app, the eventType, and the folders to scan.
type arrScan struct {
	Source string   // sonarr / radarr / lidarr / readarr
	Event  string   // arr eventType (Download, Rename, …)
	Ref    string   // the root folder the *arr referenced (for the skipped-event log)
	Paths  []string // file/folder paths (plexScanKey collapses files to their folder)
}

var arrParsers = []func([]byte) (arrScan, bool){
	parseSonarr, parseRadarr, parseLidarr, parseReadarr,
}

// parseArrWebhook detects the *arr from the payload and returns the scan. The second
// value is false when no known *arr object was present (e.g. a generic body).
func parseArrWebhook(body []byte) (arrScan, bool) {
	for _, p := range arrParsers {
		if s, ok := p(body); ok {
			return s, true
		}
	}
	return arrScan{}, false
}

// eqFold reports whether s case-insensitively equals any of opts.
func eqFold(s string, opts ...string) bool {
	for _, o := range opts {
		if strings.EqualFold(s, o) {
			return true
		}
	}
	return false
}

// Sonarr — episode file → its folder; Rename uses renamedEpisodeFiles; else the series.
func parseSonarr(body []byte) (arrScan, bool) {
	var b struct {
		EventType   string `json:"eventType"`
		Series      struct{ Path string `json:"path"` } `json:"series"`
		EpisodeFile struct{ RelativePath string `json:"relativePath"` } `json:"episodeFile"`
		RenamedEpisodeFiles []struct {
			PreviousPath string `json:"previousPath"`
			RelativePath string `json:"relativePath"`
		} `json:"renamedEpisodeFiles"`
	}
	if json.Unmarshal(body, &b) != nil || b.Series.Path == "" {
		return arrScan{}, false
	}
	// Scan the episode's own folder, never the series root — scanning /Show would
	// re-list every season for a single new episode. Import + the immediate Rename
	// then resolve to the same season folder and coalesce into one scan.
	s := arrScan{Source: "sonarr", Event: b.EventType, Ref: b.Series.Path}
	switch {
	case eqFold(b.EventType, "Download", "EpisodeFileDelete"):
		if b.EpisodeFile.RelativePath != "" {
			s.Paths = append(s.Paths, path.Join(b.Series.Path, b.EpisodeFile.RelativePath))
		}
	case eqFold(b.EventType, "Rename"):
		for _, rf := range b.RenamedEpisodeFiles {
			if rf.PreviousPath != "" {
				s.Paths = append(s.Paths, rf.PreviousPath)
			}
			if rf.RelativePath != "" {
				s.Paths = append(s.Paths, path.Join(b.Series.Path, rf.RelativePath))
			}
		}
	case eqFold(b.EventType, "SeriesDelete"):
		s.Paths = append(s.Paths, b.Series.Path) // whole series removed → scan the show root
	}
	return s, true
}

// Radarr — movie file → its folder; else the movie folder.
func parseRadarr(body []byte) (arrScan, bool) {
	var b struct {
		EventType string `json:"eventType"`
		Movie     struct{ FolderPath string `json:"folderPath"` } `json:"movie"`
		MovieFile struct{ RelativePath string `json:"relativePath"` } `json:"movieFile"`
	}
	if json.Unmarshal(body, &b) != nil || b.Movie.FolderPath == "" {
		return arrScan{}, false
	}
	s := arrScan{Source: "radarr", Event: b.EventType, Ref: b.Movie.FolderPath}
	switch {
	case eqFold(b.EventType, "Download", "MovieFileDelete"):
		if b.MovieFile.RelativePath != "" {
			s.Paths = append(s.Paths, path.Join(b.Movie.FolderPath, b.MovieFile.RelativePath))
		} else {
			s.Paths = append(s.Paths, b.Movie.FolderPath)
		}
	case eqFold(b.EventType, "MovieDelete", "Rename"):
		s.Paths = append(s.Paths, b.Movie.FolderPath)
	}
	return s, true
}

// Lidarr — track files carry absolute paths (→ album folder); else the artist folder.
func parseLidarr(body []byte) (arrScan, bool) {
	var b struct {
		EventType  string `json:"eventType"`
		Artist     struct{ Path string `json:"path"` } `json:"artist"`
		TrackFiles []struct{ Path string `json:"path"` } `json:"trackFiles"`
	}
	if json.Unmarshal(body, &b) != nil || b.Artist.Path == "" {
		return arrScan{}, false
	}
	s := arrScan{Source: "lidarr", Event: b.EventType, Ref: b.Artist.Path}
	switch {
	case eqFold(b.EventType, "Download", "TrackFileDelete", "Rename", "Retag"):
		for _, tf := range b.TrackFiles { // album folders, not the whole artist
			if tf.Path != "" {
				s.Paths = append(s.Paths, tf.Path)
			}
		}
	case eqFold(b.EventType, "ArtistDelete"):
		s.Paths = append(s.Paths, b.Artist.Path)
	}
	return s, true
}

// Readarr — book files carry absolute paths (→ book folder); else the author folder.
func parseReadarr(body []byte) (arrScan, bool) {
	var b struct {
		EventType string `json:"eventType"`
		Author    struct{ Path string `json:"path"` } `json:"author"`
		BookFiles []struct{ Path string `json:"path"` } `json:"bookFiles"`
	}
	if json.Unmarshal(body, &b) != nil || b.Author.Path == "" {
		return arrScan{}, false
	}
	s := arrScan{Source: "readarr", Event: b.EventType, Ref: b.Author.Path}
	switch {
	case eqFold(b.EventType, "Download", "BookFileDelete", "Rename", "Retag"):
		for _, bf := range b.BookFiles { // book folders, not the whole author
			if bf.Path != "" {
				s.Paths = append(s.Paths, bf.Path)
			}
		}
	case eqFold(b.EventType, "AuthorDelete"):
		s.Paths = append(s.Paths, b.Author.Path)
	}
	return s, true
}
