package api

// Upload plan (dry-run preview): given the current source, simulate distributing it
// across the eligible remotes exactly the way the real cycle would — one remote per
// check, capped by each remote's remaining daily allowance (rclone --max-transfer, which
// stops at a file boundary) — so the UI can show which remote gets what, where it stops,
// the size per remote, and an ETA. Runs even while auto-upload is disabled.

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"sb-ui/internal/executor"
)

type planFile struct {
	Path string
	Size int64
}

// listSourceFiles enumerates every file under the source with its size (best-effort via
// `find`), sorted by path for a stable, reproducible plan.
func listSourceFiles(path string) []planFile {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	rc, out, _ := executor.Get().Run(ctx, []string{"find", path, "-type", "f", "-printf", "%s\\t%p\\n"}, "")
	if rc != 0 {
		return nil
	}
	var files []planFile
	for _, line := range strings.Split(out, "\n") {
		tab := strings.IndexByte(line, '\t')
		if tab < 0 {
			continue
		}
		n, err := strconv.ParseInt(strings.TrimSpace(line[:tab]), 10, 64)
		if err != nil {
			continue
		}
		files = append(files, planFile{Path: strings.TrimSpace(line[tab+1:]), Size: n})
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	return files
}

// cloneLedger deep-copies the ledger so a plan can simulate consumption without touching
// the real one.
func cloneLedger(led map[string]*ledgerRemote) map[string]*ledgerRemote {
	out := make(map[string]*ledgerRemote, len(led))
	for k, v := range led {
		if v == nil {
			continue
		}
		cp := *v
		cp.Events = append([]ledgerEvent(nil), v.Events...)
		out[k] = &cp
	}
	return out
}

type planRemote struct {
	Remote    string `json:"remote"`
	Dest      string `json:"dest"`
	Bytes     int64  `json:"bytes"`
	Human     string `json:"human"`
	Files     int    `json:"files"`
	StopFile  string `json:"stop_file"`
	ETASec    int64  `json:"eta_sec"`    // transfer time for this chunk
	AtSec     int64  `json:"at_sec"`     // seconds from now this upload starts (incl. cap-reset waits)
	Round     int    `json:"round"`      // 1-based rotation pass
	Capped    bool   `json:"capped"`     // stopped because it hit its daily cap (vs took everything)
	FillHuman string `json:"fill_human"` // account fill the balancer saw when it picked this remote
	Cmd       string `json:"cmd"`        // exact rclone command this step runs (incl. this round's --max-transfer)
}

type uploadPlan struct {
	At             time.Time    `json:"at"`
	SourceBytes    int64        `json:"source_bytes"`
	SourceHuman    string       `json:"source_human"`
	FilesTotal     int          `json:"files_total"`
	ThresholdHuman string       `json:"threshold_human"`
	Meets          bool         `json:"meets_threshold"`
	Remotes        []planRemote `json:"remotes"`
	LeftoverBytes  int64        `json:"leftover_bytes"`
	LeftoverHuman  string       `json:"leftover_human"`
	LeftoverWhy    string       `json:"leftover_why,omitempty"`
	TransferSec    int64        `json:"transfer_sec"`  // sum of transfer times (bandwidth)
	TotalETASec    int64        `json:"total_eta_sec"` // wall-clock to done, incl. cap-reset waits
}

const (
	planFallbackSpeed = 15 << 20            // 15 MiB/s when a remote has no calibration yet
	planMaxSteps      = 300                 // safety cap on simulated upload cycles
	planMaxHorizon    = 60 * 24 * time.Hour // don't simulate beyond ~60 days of waiting
	planMaxSaneSpeed  = 2 << 30             // ignore calibration above 2 GiB/s (bogus tiny-run spikes)
)

// planSpeed is the assumed throughput for a remote's ETA: the user's manual EtaSpeed if
// set, else that remote's calibrated average (ignoring absurd spikes), else the fallback.
func planSpeed(cfg uploaderConfig, remote string) int64 {
	if s := int64(parseSize(cfg.EtaSpeed)); s > 0 {
		return s
	}
	if sp := calibratedSpeed(remote); sp > 0 && sp < planMaxSaneSpeed {
		return sp
	}
	return planFallbackSpeed
}

// buildUploadPlan simulates the WHOLE upload the way the real rotation runs: uploads go
// one remote at a time up to each remote's daily cap; when every remote is capped it
// waits for the earliest 24h window to free up (like the live loop) and continues. The
// balancer's account-fill grows as chunks land, so picks evolve. led/used are snapshots;
// it mutates only its own clones.
func buildUploadPlan(cfg uploaderConfig, size int64, files []planFile, led map[string]*ledgerRemote, used map[string]int64, cursor int, resume string, now time.Time) *uploadPlan {
	thr := int64(parseSize(cfg.Threshold))
	pl := &uploadPlan{
		At: now, SourceBytes: size, SourceHuman: humanBytes(size), FilesTotal: len(files),
		ThresholdHuman: humanBytes(thr), Meets: (thr <= 0 || size >= thr) && len(files) > 0,
		Remotes: []planRemote{}, // never nil, so the UI can read .length safely
	}
	simLed := cloneLedger(led)
	simUsed := map[string]int64{} // account fill for the balancer, grows as we upload
	for k, v := range used {
		simUsed[k] = v
	}
	remotes := resolveRemotes(cfg)
	planConf, planOp := rcloneConfPath(), uploaderOp(cfg) // for the per-step command preview

	// The largest single-remote daily cap. A file bigger than this can never upload (with
	// --cutoff-mode cautious rclone won't start a file that would exceed --max-transfer), so
	// the plan flags it clearly instead of spinning to the horizon. -1 = an unlimited remote.
	var maxCap int64 = 0
	for _, r := range remotes {
		if strings.TrimSpace(r.Name) == "" {
			continue
		}
		c := parseCapBytes(r.CapPerDay)
		if c <= 0 {
			maxCap = -1
			break
		}
		if c > maxCap {
			maxCap = c
		}
	}

	clock := now // simulated wall clock (advances by transfer time + cap-reset waits)
	round := 1
	i := 0
	// The cursor is seeded from the LIVE rotation position so the plan's future continues
	// where the real uploads left off rather than restarting the sequence each time.
	simCursor := cursor
	// Honor the resume override so the plan's order + per-remote sizes match the real run:
	// an interrupted remote is finished first (at its full remaining cap), not slotted behind
	// the sequence-first remote as leftover. selectRemote drops the resume remote from the
	// override once it's capped, so it applies to just the first fill — same as execution.
	pc := pickCtx{seq: cfg.Sequence, cursor: &simCursor, resume: resume}
	for i < len(files) && len(pl.Remotes) < planMaxSteps && clock.Sub(now) < planMaxHorizon {
		if maxCap > 0 && files[i].Size > maxCap {
			pl.LeftoverWhy = fmt.Sprintf("%q (%s) is larger than the biggest daily cap (%s) — it can't upload; raise a remote's cap above your largest file",
				baseName(files[i].Path), humanBytes(files[i].Size), humanBytes(maxCap))
			break
		}
		r, free, reason := selectRemote(remotes, simLed, pc, clock)
		if r == nil {
			next := nextEligible(remotes, simLed, clock) // all capped → jump to the next reset
			if !next.After(clock) {
				pl.LeftoverWhy = reason
				break
			}
			clock, round = next, round+1
			continue
		}
		start, bytes, capped := i, int64(0), false
		stop := ""
		for i < len(files) {
			// Match --cutoff-mode cautious: don't start a file that would push this remote
			// over its remaining daily cap — stop before it. Whole files only, so the plan
			// never shows more than the cap (which is exactly what the run does).
			if free != -1 && bytes+files[i].Size > free {
				capped = true
				break
			}
			bytes += files[i].Size
			stop = files[i].Path
			i++
		}
		// The remaining cap couldn't fit even the next whole file (free > 0 but < its size).
		// Cautious uploads nothing here and leaves the slack unused, so record the remainder
		// against this remote — dropping it out of eligibility — and let the file fall to
		// another remote, instead of fabricating a tiny over-cap chunk the real run refuses.
		if bytes == 0 {
			if free > 0 {
				ledgerAdd(simLed, remoteKey(*r), free, 0, clock)
			}
			continue
		}
		// Exact command for this step — same builder as a real run. When the round is
		// capped, pass this remote's remaining allowance so the preview shows the real
		// --max-transfer it will run with (free == -1 means uncapped → 0).
		freeCap := int64(0)
		if free > 0 {
			freeCap = free
		}
		items, dst, opts := uploaderRemoteJob(cfg, *r, freeCap)
		var cmdLines []string
		for _, args := range transferArgv(planConf, planOp, items, dst, false, opts) {
			cmdLines = append(cmdLines, strings.Join(args, " "))
		}
		eta := bytes / planSpeed(cfg, r.Name)
		pl.Remotes = append(pl.Remotes, planRemote{
			Remote: r.Name, Dest: dst,
			Bytes: bytes, Human: humanBytes(bytes), Files: i - start,
			StopFile: baseName(stop), ETASec: eta, AtSec: int64(clock.Sub(now).Seconds()),
			Round: round, Capped: capped, FillHuman: humanBytes(simUsed[r.Name]),
			Cmd: strings.Join(cmdLines, "\n"),
		})
		pl.TransferSec += eta
		clock = clock.Add(time.Duration(eta) * time.Second) // uploads are serial
		ledgerAdd(simLed, remoteKey(*r), bytes, i-start, clock) // event lands at end of transfer
		simUsed[r.Name] += bytes                            // account fills (balancer)
	}
	pl.TotalETASec = int64(clock.Sub(now).Seconds())
	if i < len(files) {
		var left int64
		for _, f := range files[i:] {
			left += f.Size
		}
		pl.LeftoverBytes = left
		pl.LeftoverHuman = humanBytes(left)
		if pl.LeftoverWhy == "" {
			pl.LeftoverWhy = "beyond the planning horizon"
		}
	}
	return pl
}

// projectRotation replays the rotation the way buildUploadPlan does — same eligibility,
// caps, gaps and timing (so the order matches the Upload plan) — but over a synthetic,
// unbounded source: each pick takes the remote's full daily allowance. That yields the
// upcoming remote ORDER without measuring the source, so the UI can show it without a
// manual "Check now". Starts from the live cursor so it continues the real rotation.
func projectRotation(cfg uploaderConfig, led map[string]*ledgerRemote, cursor, n int, resume string) []string {
	remotes := resolveRemotes(cfg)
	if len(remotes) == 0 {
		return nil
	}
	simLed := cloneLedger(led)
	simCursor := cursor
	pc := pickCtx{seq: cfg.Sequence, cursor: &simCursor, resume: resume}
	now := time.Now()
	clock := now
	out := make([]string, 0, n)
	for len(out) < n && clock.Sub(now) < planMaxHorizon {
		r, free, _ := selectRemote(remotes, simLed, pc, clock)
		if r == nil {
			next := nextEligible(remotes, simLed, clock)
			if !next.After(clock) {
				break
			}
			clock = next
			continue
		}
		out = append(out, r.Name)
		bytes := free
		if bytes <= 0 { // unlimited daily cap → a nominal chunk so the clock still advances
			bytes = 10 << 30
		}
		clock = clock.Add(time.Duration(bytes/planSpeed(cfg, r.Name)) * time.Second)
		ledgerAdd(simLed, remoteKey(*r), bytes, 1, clock)
	}
	return out
}

// storeManualPlan measures + lists the source, builds the plan, and stashes it for the
// UI. Returns the plan.
func storeManualPlan(cfg uploaderConfig, ledSnap map[string]*ledgerRemote, now time.Time) *uploadPlan {
	// Account fill (rclone about) is only for the plan's informational Fill column now, not
	// for picking; fetch it best-effort and keep the cache warm.
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	used := balanceFill(ctx)
	cancel()
	upMu.Lock()
	balFillM, balFillAt = used, time.Now()
	upMu.Unlock()
	size := measureSource(cfg.Source)
	files := listSourceFiles(cfg.Source)
	upMu.Lock()
	cur := seqCursor // continue the real rotation
	res := resumeRemote
	upMu.Unlock()
	pl := buildUploadPlan(cfg, size, files, ledSnap, used, cur, res, now)
	upMu.Lock()
	upLastPlan = pl
	upMu.Unlock()
	return pl
}

// planMsg is the one-line status summary for a manual check.
func planMsg(pl *uploadPlan, enabled bool) string {
	if !pl.Meets {
		return "below threshold — " + pl.SourceHuman + " / " + pl.ThresholdHuman
	}
	if len(pl.Remotes) == 0 {
		if pl.LeftoverWhy != "" {
			return pl.LeftoverWhy
		}
		return "nothing to upload"
	}
	base := "would upload " + pl.SourceHuman + " across " + strconv.Itoa(len(pl.Remotes)) + " remote(s)"
	if !enabled {
		return "preview — " + base + " (auto-upload off)"
	}
	return base
}

func baseName(p string) string {
	if i := strings.LastIndexByte(p, '/'); i >= 0 {
		return p[i+1:]
	}
	return p
}
