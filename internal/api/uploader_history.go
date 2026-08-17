package api

import (
	"time"

	"sb-ui/internal/store"
)

// Upload history is a persistent, append-only log of real uploads — separate from the
// 24h cap ledger (which prunes) — so the UI can show the actual PAST sequence (which
// remote, how much, the gap since the previous upload, how long it took) right next to
// the dry-run plan's FUTURE sequence. Together they read as one timeline that recomputes
// whenever a new upload lands. Bounded to the most recent histMax entries.

const (
	uploaderHistoryRel = "cache/uploader_history.json"
	histMax            = 300
)

type histEntry struct {
	At     time.Time `json:"at"`
	Remote string    `json:"remote"`
	Bytes  int64     `json:"bytes"`
	Files  int       `json:"files"`
	GapSec int64     `json:"gap_sec"` // seconds since the previous upload (any remote); 0 for the first
	DurSec int64     `json:"dur_sec"` // how long this upload actually took
}

var uploadHistory []histEntry

// loadHistory reads the persisted log (best-effort). Caller holds upMu.
func loadHistory() {
	var h []histEntry
	store.ReadJSON(uploaderHistoryRel, &h)
	if h != nil {
		uploadHistory = h
	}
}

// recordHistory appends a real upload to the log (gap measured from the previous entry)
// and persists it. Caller holds upMu.
func recordHistory(remote string, bytes int64, files int, dur time.Duration, now time.Time) {
	var gap int64
	if n := len(uploadHistory); n > 0 {
		if g := now.Sub(uploadHistory[n-1].At); g > 0 {
			gap = int64(g.Seconds())
		}
	}
	uploadHistory = append(uploadHistory, histEntry{
		At: now, Remote: remote, Bytes: bytes, Files: files,
		GapSec: gap, DurSec: int64(dur.Seconds()),
	})
	if len(uploadHistory) > histMax {
		uploadHistory = uploadHistory[len(uploadHistory)-histMax:]
	}
	store.WriteJSON(uploaderHistoryRel, uploadHistory)
}

// recentHistory returns up to n most-recent entries, newest first (for the status API).
// Caller holds upMu.
func recentHistory(n int) []histEntry {
	if n > len(uploadHistory) {
		n = len(uploadHistory)
	}
	out := make([]histEntry, 0, n)
	for i := len(uploadHistory) - 1; i >= 0 && len(out) < n; i-- {
		out = append(out, uploadHistory[i])
	}
	return out
}
