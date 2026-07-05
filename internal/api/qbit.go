package api

import (
	"fmt"
	"strings"

	"sb-ui/internal/executor"
	"sb-ui/internal/inventory"

	qbittorrent "github.com/autobrr/go-qbittorrent"
)

// qbitConn is the qBittorrent WebUI connection — configured on the Integrations page
// (like Plex/Seerr), shared by the uploader's block module.
type qbitConn struct {
	URL  string `json:"url"`  // e.g. http://<ip>:8080 ("" = auto-discover)
	User string `json:"user"` // WebUI username
	Pass string `json:"pass"` // WebUI password
}

// qbitConfig — what to do to qBittorrent while an upload runs (the uploader's block
// behaviour). Connection details are merged in from options (see resolveQbit).
type qbitConfig struct {
	Enabled bool   `json:"enabled"`
	Action  string `json:"action"`  // "pause" (stop all torrents) | "throttle" (cap speeds)
	DlKBps  int    `json:"dl_kbps"` // throttle: global download cap KB/s (0 = unlimited)
	UpKBps  int    `json:"up_kbps"` // throttle: global upload cap KB/s (0 = unlimited)

	// filled at runtime from the shared connection (not persisted in the uploader):
	URL  string `json:"-"`
	User string `json:"-"`
	Pass string `json:"-"`
}

// resolveQbit fills the connection (URL/user/pass) from the shared options config,
// auto-discovering the container URL when none is set — like the *arr apps.
func resolveQbit(cfg qbitConfig) qbitConfig {
	c := loadOptions().Qbit
	cfg.URL, cfg.User, cfg.Pass = c.URL, c.User, c.Pass
	if strings.TrimSpace(cfg.URL) == "" {
		cfg.URL = discoverQbitURL()
	}
	return cfg
}

// qbitProbe logs in and reads back the app version + torrent counts (for Integrations).
func qbitProbe(cfg qbitConfig) (version string, stats []connStat, err error) {
	c, err := qbitClient(cfg)
	if err != nil {
		return "", nil, err
	}
	if v, e := c.GetAppVersion(); e == nil {
		version = strings.TrimPrefix(strings.TrimSpace(v), "v")
	}
	ts, e := c.GetTorrents(qbittorrent.TorrentFilterOptions{})
	if e != nil {
		return version, nil, e
	}
	dl := 0
	for _, t := range ts {
		switch t.State {
		case qbittorrent.TorrentStateDownloading, qbittorrent.TorrentStateStalledDl,
			qbittorrent.TorrentStateMetaDl, qbittorrent.TorrentStateQueuedDl,
			qbittorrent.TorrentStateCheckingDl, qbittorrent.TorrentStateForcedDl:
			dl++
		}
	}
	return version, []connStat{{Label: "torrents", Value: len(ts)}, {Label: "downloading", Value: dl}}, nil
}

// discoverQbitURL finds the qBittorrent container's WebUI URL the same mode-aware way
// as the *arr apps: in remote mode prefer its public Traefik host (then the tsdproxy
// tailnet URL) since the docker IP isn't routable from here; locally use the docker
// IP :8080.
func discoverQbitURL() string {
	for _, ap := range inventory.ResolveAppdata("qbittorrent") {
		name := ap.Instance
		if _, local := executor.Get().(executor.LocalExecutor); !local {
			if h := containerWebHost(name); h != "" {
				return "https://" + h
			}
			if u := containerTsdURL(name); u != "" {
				return u
			}
		}
		if ip := containerIP(name); ip != "" {
			return "http://" + ip + ":8080"
		}
	}
	return ""
}

func qbitClient(cfg qbitConfig) (*qbittorrent.Client, error) {
	c := qbittorrent.NewClient(qbittorrent.Config{
		Host: strings.TrimRight(cfg.URL, "/"), Username: cfg.User, Password: cfg.Pass, Timeout: 20,
	})
	if err := c.Login(); err != nil {
		return nil, err
	}
	return c, nil
}

// isDownloadingState reports whether a torrent is actively downloading (not seeding).
func isDownloadingState(s qbittorrent.TorrentState) bool {
	switch s {
	case qbittorrent.TorrentStateDownloading, qbittorrent.TorrentStateStalledDl,
		qbittorrent.TorrentStateMetaDl, qbittorrent.TorrentStateQueuedDl,
		qbittorrent.TorrentStateCheckingDl, qbittorrent.TorrentStateForcedDl:
		return true
	}
	return false
}

// qbitPausedHashes remembers exactly which torrents qbitPause paused, so qbitResume
// restarts only those (never touching torrents the user paused themselves).
var qbitPausedHashes []string

// qbitPause applies the configured slowdown while an upload runs:
//   - "throttle" caps the global up/down speeds (torrents keep running, just slower);
//   - "pause" pauses only the actively-downloading torrents (seeders keep seeding, so
//     ratio is untouched) — so nothing new completes and the *arr apps get nothing to
//     import while the media root is being moved.
func qbitPause(cfg qbitConfig) error {
	c, err := qbitClient(cfg)
	if err != nil {
		return err
	}
	if cfg.Action == "throttle" {
		if e := c.SetGlobalDownloadLimit(int64(cfg.DlKBps) * 1024); e != nil {
			return e
		}
		return c.SetGlobalUploadLimit(int64(cfg.UpKBps) * 1024)
	}
	ts, err := c.GetTorrents(qbittorrent.TorrentFilterOptions{})
	if err != nil {
		return err
	}
	var hashes []string
	for _, t := range ts {
		if isDownloadingState(t.State) {
			hashes = append(hashes, t.Hash)
		}
	}
	qbitPausedHashes = hashes
	if len(hashes) == 0 {
		return nil
	}
	return c.Pause(hashes)
}

// qbitResume undoes qbitPause after the upload finishes.
func qbitResume(cfg qbitConfig) error {
	c, err := qbitClient(cfg)
	if err != nil {
		return err
	}
	if cfg.Action == "throttle" {
		if e := c.SetGlobalDownloadLimit(0); e != nil { // 0 = unlimited
			return e
		}
		return c.SetGlobalUploadLimit(0)
	}
	h := qbitPausedHashes
	qbitPausedHashes = nil
	if len(h) == 0 {
		return nil
	}
	return c.Resume(h)
}

// qbitStatus reads qBittorrent back so the user can verify the block actually took
// effect: how many torrents are paused/stopped and the current global speed limits.
func qbitStatus(cfg qbitConfig) string {
	c, err := qbitClient(cfg)
	if err != nil {
		return "unreachable: " + err.Error()
	}
	ts, err := c.GetTorrents(qbittorrent.TorrentFilterOptions{})
	if err != nil {
		return "error: " + err.Error()
	}
	paused := 0
	for _, t := range ts {
		switch t.State {
		case qbittorrent.TorrentStatePausedUp, qbittorrent.TorrentStatePausedDl,
			qbittorrent.TorrentStateStoppedUp, qbittorrent.TorrentStateStoppedDl:
			paused++
		}
	}
	limit := "off"
	if ti, e := c.GetTransferInfo(); e == nil && (ti.DlRateLimit > 0 || ti.UpRateLimit > 0) {
		limit = fmt.Sprintf("↓%s ↑%s", kbpsLabel(ti.DlRateLimit), kbpsLabel(ti.UpRateLimit))
	}
	return fmt.Sprintf("%d/%d torrents paused · global limit %s", paused, len(ts), limit)
}

func kbpsLabel(b int64) string {
	if b <= 0 {
		return "∞"
	}
	return fmt.Sprintf("%dK", b/1024)
}
