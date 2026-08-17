package api

import "testing"

func show(title, path, tvdb string) plexItem {
	return plexItem{RatingKey: tvdb, Title: title, SecType: "show", Section: "1",
		Path: path, IDs: map[string][]string{"tvdb": {tvdb}}}
}

func series(title, folder, tvdb string) arrEntry {
	return arrEntry{Kind: "sonarr", Instance: "main", Title: title, Folder: folder,
		HasFile: true, IDs: map[string][]string{"tvdb": {tvdb}}}
}

// The case this was built for: Sonarr moved a series to a different root folder. Plex
// still holds the old path, so id-only matching sees nothing wrong. Path-keyed matching
// must surface both halves and pair them into one move carrying both paths to rescan.
func TestReconcileDetectsRootFolderMove(t *testing.T) {
	entries := []arrEntry{series("IT: Welcome to Derry", "/mnt/unionfs/Media/TV/HD/IT - Welcome to Derry (2025)", "418424")}
	items := []plexItem{show("IT: Welcome to Derry", "/mnt/unionfs/Media/TV/IT - Welcome to Derry (2025)", "418424")}

	res := reconcile(entries, items)

	if len(res.Moves) != 1 {
		t.Fatalf("moves = %d, want 1 (%+v)", len(res.Moves), res)
	}
	m := res.Moves[0]
	if m.From != "/mnt/unionfs/Media/TV/IT - Welcome to Derry (2025)" {
		t.Errorf("from = %q, want the stale Plex path", m.From)
	}
	if m.To != "/mnt/unionfs/Media/TV/HD/IT - Welcome to Derry (2025)" {
		t.Errorf("to = %q, want the current arr path", m.To)
	}
	if len(res.MissingArr) != 1 || len(res.MissingSrv) != 1 {
		t.Errorf("want one half on each side, got missingArr=%d missingSrv=%d", len(res.MissingArr), len(res.MissingSrv))
	}
}

// Paths line up → nothing to report, regardless of a trailing slash on either side.
func TestReconcileCleanLibraryReportsNothing(t *testing.T) {
	entries := []arrEntry{series("Bad Sisters", "/mnt/unionfs/Media/TV/Bad Sisters/", "377155")}
	items := []plexItem{show("Bad Sisters", "/mnt/unionfs/Media/TV/Bad Sisters", "377155")}

	res := reconcile(entries, items)

	if res.Compared != 1 {
		t.Fatalf("compared = %d, want 1", res.Compared)
	}
	if len(res.Moves)+len(res.Mismatches)+len(res.MissingArr)+len(res.MissingSrv) != 0 {
		t.Errorf("clean library still reported findings: %+v", res)
	}
}

// Same folder, different title → Plex matched the folder to the wrong show.
func TestReconcileFlagsWrongMatch(t *testing.T) {
	entries := []arrEntry{series("Berlin", "/mnt/unionfs/Media/TV/Berlin (2023)", "421621")}
	items := []plexItem{show("Money Heist", "/mnt/unionfs/Media/TV/Berlin (2023)", "430619")}

	res := reconcile(entries, items)

	if len(res.Mismatches) != 1 {
		t.Fatalf("mismatches = %d, want 1 (%+v)", len(res.Mismatches), res)
	}
	if len(res.Moves) != 0 {
		t.Errorf("a wrong match is not a move: %+v", res.Moves)
	}
}

// An arr item with no files yet is not "missing from Plex" — Plex is right to not have it.
func TestReconcileIgnoresArrItemsWithoutFiles(t *testing.T) {
	e := series("Announced Show", "/mnt/unionfs/Media/TV/Announced Show", "999")
	e.HasFile = false

	res := reconcile([]arrEntry{e}, nil)

	if len(res.MissingArr) != 0 {
		t.Errorf("fileless arr item reported as missing: %+v", res.MissingArr)
	}
}

// Movies carry a file path; the containing folder is what must be compared.
func TestReconcileComparesMovieParentFolder(t *testing.T) {
	entries := []arrEntry{{Kind: "radarr", Instance: "main", Title: "Before", Folder: "/mnt/unionfs/Media/Movies/Before (2024)",
		HasFile: true, IDs: map[string][]string{"tmdb": {"1156593"}}}}
	items := []plexItem{{Title: "Before", SecType: "movie", Section: "2", IsFile: true,
		Path: "/mnt/unionfs/Media/Movies/Before (2024)/Before.2024.mkv", IDs: map[string][]string{"tmdb": {"1156593"}}}}

	res := reconcile(entries, items)

	if res.Compared != 1 || len(res.Mismatches) != 0 {
		t.Fatalf("movie file did not resolve to its folder: %+v", res)
	}
}

// Plex agents disagree about which provider they publish; falling back keeps a correct
// match from being reported as a mismatch.
func TestIdsAgreeFallsBackAcrossProviders(t *testing.T) {
	arr := map[string][]string{"tvdb": {"1234"}, "imdb": {"tt5555"}}
	plexImdbOnly := map[string][]string{"imdb": {"tt5555"}}
	if !idsAgree("sonarr", arr, plexImdbOnly) {
		t.Error("imdb fallback rejected a correct match")
	}
	plexWrong := map[string][]string{"tvdb": {"9999"}, "imdb": {"tt0000"}}
	if idsAgree("sonarr", arr, plexWrong) {
		t.Error("genuinely different ids accepted as a match")
	}
}

// The legacy Plex agent spells TMDB "themoviedb", which contains no "tmdb" — the regex
// alone misses it, which silently marks legacy-agent movies as absent from Plex.
func TestParseProviderIDHandlesLegacyAgents(t *testing.T) {
	for _, tc := range []struct{ guid, kind, want string }{
		{"com.plexapp.agents.themoviedb://1156593?lang=en", "tmdb", "1156593"},
		{"com.plexapp.agents.thetvdb://418424?lang=en", "tvdb", "418424"},
		{"tmdb://1156593", "tmdb", "1156593"},
		{"tvdb://418424", "tvdb", "418424"},
		{"imdb://tt5555", "imdb", "tt5555"},
		{"Show {tvdb-368166}", "tvdb", "368166"},
	} {
		ids := map[string][]string{}
		parseProviderID(tc.guid, ids)
		if got := ids[tc.kind]; len(got) != 1 || got[0] != tc.want {
			t.Errorf("%q → %s = %v, want [%s]", tc.guid, tc.kind, got, tc.want)
		}
	}
}

// A mapping for /mnt/media must not rewrite /mnt/mediabackup.
func TestMapArrPathRespectsPathBoundary(t *testing.T) {
	setOptForTest(t, optionsConfig{PathMappings: []pathMapping{{From: "/mnt/media", To: "/data"}}})
	if got := mapArrPath("/mnt/mediabackup/TV"); got != "/mnt/mediabackup/TV" {
		t.Errorf("crossed a path boundary: %q", got)
	}
	if got := mapArrPath("/mnt/media/TV"); got != "/data/TV" {
		t.Errorf("mapping not applied: %q", got)
	}
	if got := mapArrPath("/mnt/media"); got != "/data" {
		t.Errorf("exact-root mapping not applied: %q", got)
	}
}

// A library rooted at /Media/TV must not claim /Media/TV-UHD — otherwise a scan is sent
// to a library that doesn't contain the path and Plex silently indexes nothing.
func TestPlexSectionForPathRespectsBoundary(t *testing.T) {
	secs := []plexSection{
		{Key: "2", Title: "TV", Locations: []string{"/mnt/unionfs/Media/TV"}},
		{Key: "7", Title: "TV-HD", Locations: []string{"/mnt/unionfs/Media/TV/HD"}},
	}
	for _, tc := range []struct{ path, want string }{
		{"/mnt/unionfs/Media/TV/Some Show", "2"},
		{"/mnt/unionfs/Media/TV/HD/Some Show", "7"}, // longest match wins
		{"/mnt/unionfs/Media/TV", "2"},
		{"/mnt/unionfs/Media/TV-UHD/Some Show", ""}, // no library covers this
	} {
		got, _, ok := sectionForPathIn(secs, tc.path)
		if tc.want == "" {
			if ok {
				t.Errorf("%q matched section %q, want no match", tc.path, got)
			}
			continue
		}
		if !ok || got != tc.want {
			t.Errorf("%q → %q (ok=%v), want %q", tc.path, got, ok, tc.want)
		}
	}
}
