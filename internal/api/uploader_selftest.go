package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"sb-ui/internal/executor"
	"sb-ui/internal/rclone"
)

// Uploader self-test: a battery of non-destructive checks that verify every moving part of
// the auto-uploader actually works right now — the source is readable, each destination is
// reachable, the sizes/durations parse, the upload window is understood, and every enabled
// pause target can be contacted. Nothing is uploaded, paused, or modified; each probe is a
// read-only reachability/parse check. Results are grouped so a section can show just its
// own checks (the per-section "Verify") while the panel shows them all.

type stCheck struct {
	Group  string `json:"group"`  // config | destinations | pause
	Name   string `json:"name"`   // what was checked
	Status string `json:"status"` // ok | warn | fail | skip
	Detail string `json:"detail"` // human explanation
}

type cmdPreview struct {
	Remote string `json:"remote"`
	Cmd    string `json:"cmd"`
}

type selfTestResult struct {
	Checks   []stCheck    `json:"checks"`
	Commands []cmdPreview `json:"commands"` // exact rclone command per selected destination
	RanAt    string       `json:"ran_at"`
	OK       int          `json:"ok"`
	Warn     int          `json:"warn"`
	Fail     int          `json:"fail"`
}

// uploaderCommands renders the exact rclone command the uploader would run for each
// selected destination, using the very same argv builder as a real run — including the
// per-remote --max-transfer computed from each remote's remaining daily allowance (so the
// preview matches the real run and you can confirm the cap flag).
func uploaderCommands(cfg uploaderConfig) []cmdPreview {
	conf := rcloneConfPath()
	op := uploaderOp(cfg)
	now := time.Now()
	upMu.Lock()
	led := cloneLedger(ledger)
	upMu.Unlock()
	var out []cmdPreview
	for _, r := range resolveRemotes(cfg) {
		if strings.TrimSpace(r.Name) == "" {
			continue
		}
		// finite cap → remaining allowance becomes --max-transfer; unlimited (-1) → no flag
		free := remoteFree(r, led, now)
		if free < 0 {
			free = 0
		}
		items, dst, opts := uploaderRemoteJob(cfg, r, free)
		var parts []string
		for _, args := range transferArgv(conf, op, items, dst, false, opts) {
			parts = append(parts, strings.Join(args, " "))
		}
		out = append(out, cmdPreview{Remote: r.Name, Cmd: strings.Join(parts, "\n")})
	}
	return out
}

// rcloneReach probes a remote read-only. `about` is cheapest and returns quota; a backend
// that doesn't implement it (teldrive) falls back to a shallow `lsd`, which only proves the
// connection works without listing a whole tree.
func rcloneReach(ctx context.Context, remote string) (ok bool, detail string) {
	run := func(args ...string) (int, string) {
		full := append([]string{"rclone", "--config", rcloneConfPath(), "--low-level-retries", "1", "--timeout", "12s"}, args...)
		rc, out, _ := executor.Get().Run(ctx, full, "")
		return rc, out
	}
	if rc, out := run("about", "--json", remote+":"); rc == 0 {
		var a struct{ Total, Used, Free int64 }
		if json.Unmarshal([]byte(out), &a) == nil && a.Total > 0 {
			return true, fmt.Sprintf("reachable · %s free of %s", humanBytes(a.Free), humanBytes(a.Total))
		}
		return true, "reachable"
	}
	if rc, _ := run("lsd", remote+":", "--max-depth", "1"); rc == 0 {
		return true, "reachable (no quota API)"
	}
	return false, "cannot reach remote (check rclone.conf / auth / network)"
}

// execPathKind asks the executor what a path is ("dir"/"file"/"none", or "" if it couldn't
// tell). The executor runs where the uploader's du/rclone run — which, when sb-ui is in a
// container that doesn't mount the staging disk, is a different filesystem than this
// process sees. Overridable in tests so they don't depend on a shell. Caller supplies ctx.
var execPathKind = func(ctx context.Context, path string) string {
	rc, out, _ := executor.Get().Run(ctx, []string{"sh", "-c",
		`if [ -d "$1" ]; then echo dir; elif [ -e "$1" ]; then echo file; else echo none; fi`, "sh", path}, "")
	if rc != 0 {
		return ""
	}
	return strings.TrimSpace(out)
}

// sourceKind resolves a path to dir/file/none/"" (unknown). It trusts this process's own
// view first (fast, and correct for a local deployment), then falls back to the executor
// for the common containerised case where the source is only visible on the host.
func sourceKind(ctx context.Context, path string) string {
	if fi, err := os.Stat(path); err == nil {
		if fi.IsDir() {
			return "dir"
		}
		return "file"
	}
	return execPathKind(ctx, path)
}

// validClock reports whether s is a real HH:MM within 00:00–23:59. The shared hm() only
// parses, so the self-test range-checks here rather than changing hm's semantics.
func validClock(s string) bool {
	p := strings.SplitN(strings.TrimSpace(s), ":", 2)
	if len(p) != 2 {
		return false
	}
	h, e1 := strconv.Atoi(p[0])
	m, e2 := strconv.Atoi(p[1])
	return e1 == nil && e2 == nil && h >= 0 && h <= 23 && m >= 0 && m <= 59
}

// uploaderSelfTest runs the checks for cfg. group=="" runs them all; otherwise only that
// group, so a section's Verify button is cheap.
func uploaderSelfTest(ctx context.Context, cfg uploaderConfig, group string) []stCheck {
	var (
		mu     sync.Mutex
		checks []stCheck
	)
	add := func(g, n, s, d string) {
		mu.Lock()
		checks = append(checks, stCheck{Group: g, Name: n, Status: s, Detail: d})
		mu.Unlock()
	}
	want := func(g string) bool { return group == "" || group == g }

	var wg sync.WaitGroup

	// ── config ──────────────────────────────────────────────────────────────────
	if want("config") {
		// Source folder: set and a real directory the uploader can reach. Checked through
		// the executor when this process can't see it, so a container mount gap doesn't read
		// as a missing folder (that mismatch is exactly why Check now could work while this
		// reported "not found").
		src := strings.TrimSpace(cfg.Source)
		switch {
		case src == "":
			add("config", "Source folder", "fail", "no source folder set — nothing to upload from")
		default:
			switch sourceKind(ctx, src) {
			case "dir":
				add("config", "Source folder", "ok", "exists and is reachable")
			case "file":
				add("config", "Source folder", "fail", src+" is a file, not a folder")
			case "none":
				add("config", "Source folder", "fail", "not found: "+src)
			default: // unknown — couldn't probe either view
				add("config", "Source folder", "warn", "couldn't verify "+src+" — Check now still measures it directly")
			}
		}

		// Threshold parses to a positive size.
		if thr := int64(parseSize(cfg.Threshold)); thr > 0 {
			add("config", "Upload threshold", "ok", humanBytes(thr))
		} else {
			add("config", "Upload threshold", "warn", "0 or unparseable — would upload on any content")
		}

		// Check interval.
		if cfg.IntervalMinutes > 0 {
			add("config", "Check interval", "ok", fmt.Sprintf("every %d min", cfg.IntervalMinutes))
		} else {
			add("config", "Check interval", "warn", "0 — falls back to the 15 min default")
		}

		// Min file age (optional, lives under Transfer options) must be a valid duration if
		// set. Unset is fine — the source normally only holds finished files.
		if a := strings.TrimSpace(cfg.MinAge); a != "" {
			if parseDur(a) > 0 {
				add("config", "Min file age", "ok", a+" — in-progress files are skipped")
			} else {
				add("config", "Min file age", "fail", "unparseable duration: "+a)
			}
		} else {
			add("config", "Min file age", "ok", "off — all source files treated as ready")
		}

		// ETA speed (optional) must parse if given.
		if s := strings.TrimSpace(cfg.EtaSpeed); s != "" {
			if parseSize(s) > 0 {
				add("config", "Plan ETA speed", "ok", s+"/s (plan estimate only)")
			} else {
				add("config", "Plan ETA speed", "warn", "unparseable: "+s+" — falls back to measured")
			}
		}

		// Upload window: valid HH:MM (or blank), plus whether it's open right now.
		from, until := strings.TrimSpace(cfg.AllowedFrom), strings.TrimSpace(cfg.AllowedUntil)
		if from == "" && until == "" {
			add("config", "Upload window", "ok", "anytime (no off-peak window set)")
		} else if !validClock(from) || !validClock(until) {
			add("config", "Upload window", "fail", "invalid time — use HH:MM (00:00–23:59) for both ends")
		} else if inWindow(from, until, time.Now()) {
			add("config", "Upload window", "ok", fmt.Sprintf("%s–%s · open now", from, until))
		} else {
			open := nextWindowOpen(from, until, time.Now())
			add("config", "Upload window", "ok", fmt.Sprintf("%s–%s · closed now, opens %s", from, until, open.Format("Mon 15:04")))
		}
	}

	// ── destinations ────────────────────────────────────────────────────────────
	if want("destinations") {
		remotes := resolveRemotes(cfg)
		named := make([]uploaderRemote, 0, len(remotes))
		for _, r := range remotes {
			if strings.TrimSpace(r.Name) != "" {
				named = append(named, r)
			}
		}
		if len(named) == 0 {
			add("destinations", "Destinations", "fail", "no remotes selected — the rotation has nowhere to go")
		} else {
			conf, _ := rclone.Remotes(rcloneConfPath())
			var dwg sync.WaitGroup
			for _, r := range named {
				dwg.Add(1)
				go func(r uploaderRemote) {
					defer dwg.Done()
					name, _ := splitRemoteDst(r.Name + ":")
					if _, known := conf[name]; !known {
						add("destinations", r.Name, "fail", "not in rclone.conf")
						return
					}
					// Cap / files / gap sanity (resolved from shared defaults already).
					var notes []string
					if c := strings.TrimSpace(r.CapPerDay); c != "" {
						if parseCapBytes(c) > 0 {
							notes = append(notes, "cap "+humanBytes(parseCapBytes(c)))
						} else {
							notes = append(notes, "cap unparseable ("+c+")")
						}
					}
					rctx, cancel := context.WithTimeout(ctx, 18*time.Second)
					ok, detail := rcloneReach(rctx, name)
					cancel()
					if len(notes) > 0 {
						detail += " · " + strings.Join(notes, " · ")
					}
					if ok {
						add("destinations", r.Name, "ok", detail)
					} else {
						add("destinations", r.Name, "fail", detail)
					}
				}(r)
			}
			dwg.Wait()
		}
	}

	// ── pause targets (only the enabled ones; disabled are reported as skipped) ────
	if want("pause") {
		p := cfg.Pause
		wg.Add(1)
		go func() {
			defer wg.Done()
			if !p.PlexKillTranscode {
				add("pause", "Plex", "skip", "not enabled")
				return
			}
			plex := loadOptions().Plex
			if strings.TrimSpace(plex.URL) == "" {
				add("pause", "Plex", "fail", "no Plex URL configured (Integrations page)")
			} else if secs := plexSections(plex); len(secs) > 0 {
				add("pause", "Plex", "ok", fmt.Sprintf("reachable · %d transcodes now", plexTranscodeCount(plex)))
			} else {
				add("pause", "Plex", "fail", "cannot reach Plex (check URL/token on Integrations)")
			}
		}()

		wg.Add(1)
		go func() {
			defer wg.Done()
			if !p.Qbit.Enabled {
				add("pause", "qBittorrent", "skip", "not enabled")
				return
			}
			if _, _, err := qbitProbe(resolveQbit(p.Qbit)); err != nil {
				add("pause", "qBittorrent", "fail", "cannot reach qBittorrent: "+err.Error())
			} else {
				add("pause", "qBittorrent", "ok", "reachable")
			}
		}()

		if !p.AutoscanHold {
			add("pause", "Autoscan", "skip", "not enabled")
		} else if c := autoscanContainer(); c != "" {
			add("pause", "Autoscan", "ok", "container: "+c)
		} else {
			add("pause", "Autoscan", "warn", "no autoscan container found — only the built-in queue will hold")
		}

		if !p.ArrDisable {
			add("pause", "Sonarr / Radarr", "skip", "not enabled")
		} else {
			insts := arrInstances()
			if len(insts) == 0 {
				add("pause", "Sonarr / Radarr", "fail", "no *arr instances discovered")
			} else {
				var reach int
				var down []string
				for _, inst := range insts {
					if ok, _ := arrGetRaw(inst, "system/status"); ok {
						reach++
					} else {
						down = append(down, inst.Name)
					}
				}
				switch {
				case reach == len(insts):
					add("pause", "Sonarr / Radarr", "ok", fmt.Sprintf("%d instance(s) reachable", reach))
				case reach > 0:
					add("pause", "Sonarr / Radarr", "warn", fmt.Sprintf("%d/%d reachable · down: %s", reach, len(insts), strings.Join(down, ", ")))
				default:
					add("pause", "Sonarr / Radarr", "fail", "no *arr reachable: "+strings.Join(down, ", "))
				}
			}
		}
	}

	wg.Wait()

	// Stable order: group, then failures first within a group so problems surface.
	rank := map[string]int{"config": 0, "destinations": 1, "pause": 2}
	sev := map[string]int{"fail": 0, "warn": 1, "ok": 2, "skip": 3}
	sort.SliceStable(checks, func(i, j int) bool {
		if rank[checks[i].Group] != rank[checks[j].Group] {
			return rank[checks[i].Group] < rank[checks[j].Group]
		}
		return sev[checks[i].Status] < sev[checks[j].Status]
	})
	return checks
}

// uploaderSelfTestHandler runs the self-test. The body may carry the on-screen config so a
// user can verify unsaved edits; with no body it tests the saved config. ?group= narrows
// it to one section.
func uploaderSelfTestHandler(w http.ResponseWriter, req *http.Request) {
	upMu.Lock()
	ensureUploader()
	cfg := ucfg
	upMu.Unlock()

	// Optional on-screen config override.
	var posted uploaderConfig
	if json.NewDecoder(req.Body).Decode(&posted) == nil && (posted.Source != "" || len(posted.Remotes) > 0) {
		cfg = posted
	}

	group := req.URL.Query().Get("group")
	ctx, cancel := context.WithTimeout(req.Context(), 40*time.Second)
	defer cancel()

	checks := uploaderSelfTest(ctx, cfg, group)
	res := selfTestResult{Checks: checks, RanAt: time.Now().UTC().Format(time.RFC3339)}
	// Commands belong with the destinations view (and the whole-page run).
	if group == "" || group == "destinations" {
		res.Commands = uploaderCommands(cfg)
	}
	for _, c := range checks {
		switch c.Status {
		case "ok":
			res.OK++
		case "warn":
			res.Warn++
		case "fail":
			res.Fail++
		}
	}
	writeJSON(w, http.StatusOK, res)
}
