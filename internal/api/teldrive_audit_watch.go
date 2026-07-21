package api

import (
	"context"
	"strings"
	"sync"
	"time"

	"sb-ui/internal/store"
)

// A full audit is a minutes-long pass over millions of rows, so the result is persisted:
// reloading the page must show the last findings rather than an empty table and a demand
// to scan again.
//
// It is also kept current without anyone pressing anything. Every tick the watcher asks
// each instance only for a row count — cheap next to the audit itself — and rescans when
// files have appeared. The count is deliberately not the trigger on its own: files show up
// in the table while their parts are still being written, and auditing then would report
// half-written uploads as short files. So a rescan waits until the uploads table has been
// quiet for uploadSettleFor, which is exactly the "is the upload finished" question.

const (
	teldriveAuditRel = "cache/teldrive_audit.json"
	auditWatchEvery  = 5 * time.Minute
	uploadSettleFor  = 3 * time.Minute
	auditCountTimout = 60 * time.Second
)

type auditSnapshot struct {
	Result   auditResult      `json:"result"`
	Counts   map[string]int64 `json:"counts"`             // per-remote file count when this was taken
	SavedAt  string           `json:"saved_at"`           // when the scan that produced it finished
	Auto     bool             `json:"auto"`               // taken by the watcher rather than by hand
	WatchMsg string           `json:"watch_msg"`          // what the watcher is currently doing/waiting on
	Pending  bool             `json:"pending"`            // new files seen, rescan deferred until uploads settle
	Error    string           `json:"error,omitempty"`    // last watcher failure, if any
	Scanning bool             `json:"scanning,omitempty"` // a scan is running right now
}

var (
	auditMu   sync.Mutex
	auditSnap *auditSnapshot
	auditRun  bool // a scan (manual or automatic) is in flight
	auditOnce sync.Once
)

// loadAuditSnapshot reads the persisted result once, so a restart doesn't lose it either.
func loadAuditSnapshot() *auditSnapshot {
	auditMu.Lock()
	defer auditMu.Unlock()
	if auditSnap == nil {
		var s auditSnapshot
		store.ReadJSON(teldriveAuditRel, &s)
		if s.SavedAt != "" {
			auditSnap = &s
		}
	}
	if auditSnap == nil {
		return nil
	}
	snap := *auditSnap
	snap.Scanning = auditRun
	return &snap
}

// saveAuditSnapshot records a completed scan along with the counts it was taken at, which
// is what the watcher later compares against to notice new files.
func saveAuditSnapshot(res auditResult, auto bool, msg string) {
	counts := map[string]int64{}
	for _, inst := range res.Instances {
		if inst.Error == "" {
			counts[inst.Remote] = inst.Scanned
		}
	}
	s := &auditSnapshot{
		Result: res, Counts: counts, Auto: auto, WatchMsg: msg,
		SavedAt: time.Now().UTC().Format(time.RFC3339),
	}
	auditMu.Lock()
	auditSnap = s
	auditMu.Unlock()
	store.WriteJSON(teldriveAuditRel, s)
}

// markWatch updates only the watcher's commentary, leaving the findings intact — the UI
// can say "waiting for uploads to finish" without the results flickering away.
func markWatch(msg string, pending bool, errMsg string) {
	auditMu.Lock()
	defer auditMu.Unlock()
	if auditSnap == nil {
		return
	}
	auditSnap.WatchMsg, auditSnap.Pending, auditSnap.Error = msg, pending, errMsg
	store.WriteJSON(teldriveAuditRel, auditSnap)
}

// instanceCounts asks each configured instance for its file count only. Errors are
// reported per instance rather than aborting: one database being unreachable must not stop
// the others from being watched.
func instanceCounts(ctx context.Context, cfg teldriveConfig, activeOnly bool) (map[string]int64, error) {
	out := map[string]int64{}
	var firstErr error
	for _, db := range cfg.teldriveDBs() {
		if db.Disabled || strings.TrimSpace(db.DSN) == "" {
			continue
		}
		pool, err := teldrivePool(ctx, db.DSN)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		var n int64
		where := filePredicate(activeOnly, hasStatusColumn(ctx, pool))
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM teldrive.files WHERE `+where).Scan(&n); err != nil {
			if firstErr == nil {
				firstErr = err
			}
		} else {
			out[db.Remote] = n
		}
		pool.Close()
	}
	return out, firstErr
}

// uploadsSettled reports whether every instance has gone quiet — no upload part written
// within uploadSettleFor. A file row exists from the moment its first part lands, so
// scanning before this is true would flag uploads that are merely still in progress.
func uploadsSettled(ctx context.Context, cfg teldriveConfig) (bool, error) {
	for _, db := range cfg.teldriveDBs() {
		if db.Disabled || strings.TrimSpace(db.DSN) == "" {
			continue
		}
		pool, err := teldrivePool(ctx, db.DSN)
		if err != nil {
			return false, err
		}
		var busy bool
		err = pool.QueryRow(ctx, `
			SELECT EXISTS (SELECT 1 FROM teldrive.uploads WHERE created_at > now() - $1::interval)`,
			uploadSettleFor.String()).Scan(&busy)
		pool.Close()
		if err != nil {
			return false, err
		}
		if busy {
			return false, nil
		}
	}
	return true, nil
}

// newFilesSince counts how many files appeared since the last scan. Only growth counts: a
// shrinking count means files were deleted, and the findings can't have grown from that.
// An instance that wasn't in the baseline (newly configured, or unreachable at scan time)
// is skipped rather than having its whole count treated as new, which would otherwise
// trigger a rescan on every single tick.
func newFilesSince(before, now map[string]int64) int64 {
	var added int64
	for remote, n := range now {
		if prev, ok := before[remote]; ok && n > prev {
			added += n - prev
		}
	}
	return added
}

// auditWatchTick is one pass of the watcher. It never starts the first scan by itself: with
// no previous result there is nothing to compare against, and a multi-minute scan should
// begin because someone asked for it.
func auditWatchTick() {
	snap := loadAuditSnapshot()
	if snap == nil {
		return
	}
	auditMu.Lock()
	busy := auditRun
	auditMu.Unlock()
	if busy {
		return
	}

	cfg := loadOptions().Teldrive
	ctx, cancel := context.WithTimeout(context.Background(), auditCountTimout)
	counts, err := instanceCounts(ctx, cfg, snap.Result.ActiveOnly)
	cancel()
	if err != nil && len(counts) == 0 {
		markWatch("couldn't reach the database to check for new files", snap.Pending, err.Error())
		return
	}

	if added := newFilesSince(snap.Counts, counts); added == 0 {
		markWatch("no new files since the last scan", false, "")
		return
	}

	ctx, cancel = context.WithTimeout(context.Background(), auditCountTimout)
	settled, serr := uploadsSettled(ctx, cfg)
	cancel()
	if serr != nil {
		markWatch("couldn't check whether uploads are still running", true, serr.Error())
		return
	}
	if !settled {
		markWatch("new files found, waiting for uploads in progress to finish", true, "")
		return
	}

	markWatch("new files found and uploads have settled — rescanning", true, "")
	auditMu.Lock()
	auditRun = true
	auditMu.Unlock()
	defer func() {
		auditMu.Lock()
		auditRun = false
		auditMu.Unlock()
	}()

	ctx, cancel = context.WithTimeout(context.Background(), auditQueryTimeout)
	defer cancel()
	res, err := runTeldriveAudit(ctx, cfg, snap.Result.ChunkGuess, snap.Result.ActiveOnly)
	if err != nil {
		markWatch("automatic rescan failed", true, err.Error())
		return
	}
	saveAuditSnapshot(res, true, "rescanned automatically after new files finished uploading")
}

func startAuditWatcher() {
	auditOnce.Do(func() {
		go func() {
			for {
				time.Sleep(auditWatchEvery)
				auditWatchTick()
			}
		}()
	})
}
