package api

import (
	"encoding/json"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"

	"sb-ui/internal/store"
)

// Central options (Options page). Plex integration closes the cloudplow loop:
// throttle uploads while people are streaming, and refresh the Plex library after
// an upload finishes (replacing a separate autoscan).

type plexConfig struct {
	URL             string `json:"url"`               // e.g. http://localhost:32400
	Token           string `json:"token"`             // X-Plex-Token
	Throttle        bool   `json:"throttle"`          // pause uploads while streaming
	MaxStreams      int    `json:"max_streams"`       // allowed concurrent streams before pausing
	ScanAfterUpload bool   `json:"scan_after_upload"` // refresh libraries when an upload finishes
}

// pathMapping translates an arr file path to its Plex-side equivalent (their
// library roots can differ, e.g. arr /Media/TV-UHD vs Plex /Media/tvuhd).
type pathMapping struct {
	From string `json:"from"` // arr path prefix
	To   string `json:"to"`   // Plex path prefix
}

// seerrConfig points at a Jellyseerr/Overseerr instance — used ONLY to submit
// requests (its core job). Discover/detail metadata comes from TMDB directly.
// Multiple instances (jellyseerr, seerr, …) are stored in cache/seerr_instances.json;
// this legacy single entry is migrated into that list on first use (see seerr.go).
type seerrConfig struct {
	Name    string `json:"name,omitempty"`    // container/instance name (multi-instance)
	URL     string `json:"url"`               // e.g. https://requests.example.com
	APIKey  string `json:"api_key"`           // X-Api-Key
	Default bool   `json:"default,omitempty"` // the instance used for Discover requests
}

// tmdbConfig holds a TMDb v3 API key — the source of all Discover display metadata.
type tmdbConfig struct {
	APIKey string `json:"api_key"`
}

// autoscanConfig drives the built-in autoscan (docs/autoscan-plan.md): a debounced
// Plex partial-scan service fed by arr webhooks / manual triggers / post-upload.
// Path rewriting reuses the top-level PathMappings (mapArrPath).
type autoscanConfig struct {
	Enabled      bool   `json:"enabled"`
	DelaySec     int    `json:"delay_sec"`     // debounce window before a path is scanned; default 5
	ScanGapSec   int    `json:"scan_gap_sec"`  // min gap between consecutive scans (rate limit); default 3
	OnUpload     bool   `json:"on_upload"`     // scan the moved paths after an uploader run
	WebhookToken string `json:"webhook_token"` // shared secret embedded in the arr webhook URL
	LogSkipped   bool   `json:"log_skipped"`   // also record webhook events we don't scan (debug)
	// Anchors — absolute files that must exist before a scan is sent (autoplow-style).
	// If any is missing the mount is considered down and the scan is held, so Plex
	// won't remove items when a rclone mount drops.
	Anchors []string `json:"anchors"`
	// Completion detection — poll Plex /activities so a scan only shows Completed once
	// Plex has actually finished (not just when the refresh was triggered).
	WaitCompletion bool `json:"wait_completion"`
	IdleSec        int  `json:"idle_sec"`    // no scan activity for this long = done (default 30)
	TimeoutSec     int  `json:"timeout_sec"` // give up waiting after this (default 300)
	// Filtering (autoplow-style) — drop events that don't warrant a Plex scan.
	ExcludeExts  []string `json:"exclude_exts"`  // file extensions to ignore (srt, nfo, …)
	ExcludePaths []string `json:"exclude_paths"` // path prefixes to ignore
	IncludePaths []string `json:"include_paths"` // if set, only paths under one of these scan
	// WebhookEvents — which *arr Connection triggers "Wire & test" enables (canonical
	// keys: import, upgrade, rename, delete). Empty = import+upgrade+rename.
	WebhookEvents []string `json:"webhook_events"`
}

// teldriveServer is one Postgres host. Instances usually share a server and differ only
// by database, so the shared block is filled once and each instance names its database;
// an instance that genuinely lives elsewhere overrides the whole block.
type teldriveServer struct {
	Host     string `json:"host"`
	Port     int    `json:"port"`
	User     string `json:"user"`
	Password string `json:"password"`
	SSLMode  string `json:"sslmode"` // disable | prefer | require | verify-full; "" = disable
}

func (s teldriveServer) filled() bool { return strings.TrimSpace(s.Host) != "" }

// dsn renders a connection string. Built through net/url rather than concatenated so a
// password containing @ : / ? or # can't corrupt the URL — a very easy way to produce a
// connection that silently points somewhere else.
func (s teldriveServer) dsn(database string) string {
	port := s.Port
	if port == 0 {
		port = 5432
	}
	ssl := strings.TrimSpace(s.SSLMode)
	if ssl == "" {
		ssl = "disable"
	}
	u := url.URL{
		Scheme:   "postgres",
		Host:     net.JoinHostPort(strings.TrimSpace(s.Host), strconv.Itoa(port)),
		Path:     "/" + strings.TrimPrefix(strings.TrimSpace(database), "/"),
		RawQuery: url.Values{"sslmode": {ssl}}.Encode(),
	}
	if s.User != "" {
		u.User = url.UserPassword(s.User, s.Password)
	}
	return u.String()
}

// teldriveDB is one teldrive instance's database. Remote matches the name in rclone.conf
// so findings can be attributed back to an account.
type teldriveDB struct {
	Remote   string `json:"remote"`
	Database string `json:"database"`
	// OwnServer switches this instance off the shared block onto Server below.
	OwnServer bool           `json:"own_server"`
	Server    teldriveServer `json:"server,omitempty"`
	// DSN, when set, overrides everything above — an escape hatch for connection strings
	// the structured fields can't express.
	DSN string `json:"dsn,omitempty"`
	// MaxPartBytes is the largest a single part can be — set it to the biggest chunk size
	// this instance was ever configured with. It drives the audit's only assumption-free
	// test: if size > parts * MaxPartBytes the file is short whatever the chunking was.
	// 0 = Telegram's 2 GiB limit, which is safe but rarely sharp enough to catch anything.
	MaxPartBytes int64 `json:"max_part_bytes"`
	Disabled     bool  `json:"disabled"` // keep the settings but skip it when scanning
}

type teldriveConfig struct {
	Shared teldriveServer `json:"shared"`
	DBs    []teldriveDB   `json:"dbs"`
	// Legacy single-instance fields, folded into DBs on load.
	DSN          string `json:"dsn,omitempty"`
	MaxPartBytes int64  `json:"max_part_bytes,omitempty"`
}

// teldriveDBs returns the configured instances with DSN resolved, migrating the old
// single-DSN shape.
func (c teldriveConfig) teldriveDBs() []teldriveDB {
	if len(c.DBs) == 0 && strings.TrimSpace(c.DSN) != "" {
		return []teldriveDB{{Remote: "teldrive", DSN: c.DSN, MaxPartBytes: c.MaxPartBytes}}
	}
	out := make([]teldriveDB, 0, len(c.DBs))
	for _, db := range c.DBs {
		db.DSN = c.resolveDSN(db)
		out = append(out, db)
	}
	return out
}

// resolveDSN picks the explicit DSN, else this instance's own server, else the shared one.
func (c teldriveConfig) resolveDSN(db teldriveDB) string {
	if s := strings.TrimSpace(db.DSN); s != "" {
		return s
	}
	srv := c.Shared
	if db.OwnServer {
		srv = db.Server
	}
	if !srv.filled() {
		return ""
	}
	return srv.dsn(db.Database)
}

type optionsConfig struct {
	Plex         plexConfig     `json:"plex"`
	PathMappings []pathMapping  `json:"path_mappings"`
	Seerr        seerrConfig    `json:"seerr"`
	Tmdb         tmdbConfig     `json:"tmdb"`
	Qbit         qbitConn       `json:"qbit"`     // qBittorrent WebUI (used by the uploader's block module)
	Autoscan     autoscanConfig `json:"autoscan"` // built-in autoscan service
	Teldrive     teldriveConfig `json:"teldrive"` // teldrive Postgres (integrity audit)
}

// mapArrPath rewrites an arr path to the Plex path using the longest matching
// prefix mapping. Returns the path unchanged when nothing matches.
func mapArrPath(p string) string {
	best := -1
	out := p
	for _, m := range loadOptions().PathMappings {
		if m.From != "" && strings.HasPrefix(p, m.From) && len(m.From) > best {
			best = len(m.From)
			out = m.To + p[len(m.From):]
		}
	}
	return out
}

const optionsRel = "cache/options.json"

var (
	optMu     sync.Mutex
	optCfg    optionsConfig
	optLoaded bool
)

func loadOptions() optionsConfig {
	optMu.Lock()
	defer optMu.Unlock()
	if !optLoaded {
		store.ReadJSON(optionsRel, &optCfg)
		optLoaded = true
	}
	return optCfg
}

func getOptions(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, loadOptions())
}

// saveAutoscanConfig patches only the Autoscan field of the persisted options and
// returns the stored value (so the autoscan endpoints don't clobber the rest).
func saveAutoscanConfig(ac autoscanConfig) autoscanConfig {
	optMu.Lock()
	defer optMu.Unlock()
	if !optLoaded {
		store.ReadJSON(optionsRel, &optCfg)
		optLoaded = true
	}
	optCfg.Autoscan = ac
	store.WriteJSON(optionsRel, optCfg)
	return optCfg.Autoscan
}

// saveTeldriveConfig patches only the Teldrive field, so saving the audit's DSN from the
// tgDrive page can't clobber unrelated options.
func saveTeldriveConfig(tc teldriveConfig) teldriveConfig {
	optMu.Lock()
	defer optMu.Unlock()
	if !optLoaded {
		store.ReadJSON(optionsRel, &optCfg)
		optLoaded = true
	}
	optCfg.Teldrive = tc
	store.WriteJSON(optionsRel, optCfg)
	return optCfg.Teldrive
}

func putOptions(w http.ResponseWriter, req *http.Request) {
	var c optionsConfig
	if json.NewDecoder(req.Body).Decode(&c) != nil {
		http.Error(w, "bad config", http.StatusBadRequest)
		return
	}
	optMu.Lock()
	if !optLoaded {
		store.ReadJSON(optionsRel, &optCfg)
		optLoaded = true
	}
	c.Autoscan = optCfg.Autoscan // autoscan is managed only via /api/autoscan/config
	c.Teldrive = optCfg.Teldrive // teldrive audit config is managed via /api/teldrive/config
	optCfg = c
	store.WriteJSON(optionsRel, optCfg)
	optMu.Unlock()
	resetPlexDirs() // Plex URL/token may have changed → rebuild the path index
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// plexSection is one Plex library section. Sections + all other Plex calls are
// served by the plexgo client (see plexclient.go).
type plexSection struct {
	Key       string   `json:"key"`
	Title     string   `json:"title"`
	Type      string   `json:"type"`
	Locations []string `json:"locations,omitempty"` // library root paths
}

// plexTest reports connectivity: library sections + active streams, via plexgo.
func plexTest(w http.ResponseWriter, req *http.Request) {
	cfg := loadOptions().Plex
	// Allow testing the values currently in the form (before Save).
	var b struct {
		URL   string `json:"url"`
		Token string `json:"token"`
	}
	if json.NewDecoder(req.Body).Decode(&b); strings.TrimSpace(b.URL) != "" {
		cfg.URL = strings.TrimSpace(b.URL)
		if strings.TrimSpace(b.Token) != "" {
			cfg.Token = strings.TrimSpace(b.Token)
		}
	}
	if cfg.URL == "" {
		http.Error(w, "Plex URL not set", http.StatusBadRequest)
		return
	}
	secs := plexSections(cfg)
	if len(secs) == 0 {
		http.Error(w, "cannot reach Plex or no library sections (check URL/token)", http.StatusBadGateway)
		return
	}
	titles := make([]string, 0, len(secs))
	for _, s := range secs {
		titles = append(titles, s.Title)
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "streams": plexActiveStreams(cfg), "sections": titles})
}
