package api

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"

	"sb-ui/internal/executor"
	"sb-ui/internal/rclone"
	"sb-ui/internal/store"
)

// Telemetry (P1): record detailed per-job upload state while a transfer runs —
// a downsampled speed/throughput time-series, per-file progress, and classified
// events (rate-limits, errors, retries). Persisted per job so we can analyse
// afterwards WHAT went wrong, WHEN and WHY, and recommend task settings.
//
// The schema + the analysis rules below are intentionally data-driven so later
// phases (P2 charts, P3 verdict + "apply to task" + simulator calibration) can
// extend them without touching the recorder.

// ── schema ────────────────────────────────────────────────────────────────────

type telSample struct {
	T      int   `json:"t"`      // seconds since start
	Speed  int64 `json:"speed"`  // aggregate bytes/sec
	Bytes  int64 `json:"bytes"`  // cumulative bytes transferred
	Active int   `json:"active"` // files transferring at this instant
	Errors int   `json:"errors"` // cumulative errors
}

type telFile struct {
	Name     string `json:"name"`
	Size     int64  `json:"size"`
	Bytes    int64  `json:"bytes"`
	SpeedAvg int64  `json:"speed_avg"` // best avg speed seen for this file
}

type telEvent struct {
	T    int    `json:"t"`    // seconds since start
	Kind string `json:"kind"` // flood | auth | quota | checksum | network | retry | error
	Msg  string `json:"msg"`
}

type telSummary struct {
	DurationSec int64 `json:"duration_sec"`
	Bytes       int64 `json:"bytes"`
	Files       int   `json:"files"`
	AvgSpeed    int64 `json:"avg_speed"`
	PeakSpeed   int64 `json:"peak_speed"`
	PeakActive  int   `json:"peak_active"`
	Errors      int   `json:"errors"`
	FloodHits   int   `json:"flood_hits"`
	Throttled   bool  `json:"throttled"`
	PerConnEst  int64 `json:"per_conn_est"` // estimated speed per connection (calibrates the simulator)
	Concurrency int   `json:"concurrency"`  // upload_concurrency used for the estimate
}

type telemetry struct {
	JobID      string              `json:"job_id"`
	StartedAt  string              `json:"started_at"`
	Dst        string              `json:"dst"`
	Samples    []telSample         `json:"samples"`
	Files      map[string]*telFile `json:"files"`
	Events     []telEvent          `json:"events"`
	Summary    *telSummary         `json:"summary,omitempty"`
	peakSpeed  int64               // working state (not serialised)
	peakActive int
	winPeak    int64 // max speed seen since the last stored sample
	lastSample int
	effEvery   int // adaptive sampling interval (sec); doubles when the buffer fills
	startNS    time.Time
}

const (
	telDir        = "cache/telemetry"
	telMaxSamples = 6000 // when full, halve resolution (keeps the whole timeline, bounded)
	telMaxEvents  = 1000
)

var (
	telMu    sync.Mutex
	telStore = map[string]*telemetry{}
)

// ── recorder ──────────────────────────────────────────────────────────────────

func telStart(jobID, dst string) {
	telMu.Lock()
	telStore[jobID] = &telemetry{
		JobID: jobID, Dst: dst, StartedAt: time.Now().UTC().Format(time.RFC3339),
		Files: map[string]*telFile{}, lastSample: -1, effEvery: 1, startNS: time.Now(),
	}
	telMu.Unlock()
}

func telOnStats(jobID string, s *transferStats) {
	if s == nil {
		return
	}
	telMu.Lock()
	defer telMu.Unlock()
	t := telStore[jobID]
	if t == nil {
		return
	}
	if int64(s.Speed) > t.peakSpeed {
		t.peakSpeed = int64(s.Speed)
	}
	if int64(s.Speed) > t.winPeak {
		t.winPeak = int64(s.Speed) // peak within the current sampling window
	}
	if n := len(s.Transferring); n > t.peakActive {
		t.peakActive = n
	}
	for _, f := range s.Transferring {
		fr := t.Files[f.Name]
		if fr == nil {
			fr = &telFile{Name: f.Name, Size: f.Size}
			t.Files[f.Name] = fr
		}
		fr.Bytes = f.Bytes
		if int64(f.SpeedAvg) > fr.SpeedAvg {
			fr.SpeedAvg = int64(f.SpeedAvg)
		}
	}
	sec := int(time.Since(t.startNS).Seconds())
	if sec-t.lastSample >= t.effEvery {
		t.lastSample = sec
		t.Samples = append(t.Samples, telSample{
			T: sec, Speed: t.winPeak, Bytes: s.Bytes, Active: len(s.Transferring), Errors: s.Errors,
		})
		t.winPeak = 0                        // reset for the next window
		if len(t.Samples) >= telMaxSamples { // buffer full → halve resolution, keep full timeline
			kept := t.Samples[:0:0]
			for i := 0; i < len(t.Samples); i += 2 {
				kept = append(kept, t.Samples[i])
			}
			t.Samples = kept
			t.effEvery *= 2
		}
	}
}

func telOnLog(jobID, msg string) {
	kind := classifyEvent(msg)
	if kind == "" {
		return
	}
	telMu.Lock()
	defer telMu.Unlock()
	t := telStore[jobID]
	if t == nil || len(t.Events) >= telMaxEvents {
		return
	}
	m := strings.TrimSpace(msg)
	if len(m) > 240 {
		m = m[:240]
	}
	t.Events = append(t.Events, telEvent{T: int(time.Since(t.startNS).Seconds()), Kind: kind, Msg: m})
}

// classifyEvent buckets a log line by failure cause ("" = not noteworthy).
func classifyEvent(msg string) string {
	m := strings.ToLower(msg)
	switch {
	case isFloodMsg(msg):
		return "flood"
	case strings.Contains(m, "401") || strings.Contains(m, "403") || strings.Contains(m, "unauthor") || strings.Contains(m, "auth") || strings.Contains(m, "token"):
		return "auth"
	case strings.Contains(m, "quota") || strings.Contains(m, "storage quota") || strings.Contains(m, "limit exceeded"):
		return "quota"
	case strings.Contains(m, "corrupt") || strings.Contains(m, "hash differ") || strings.Contains(m, "checksum") || strings.Contains(m, "md5"):
		return "checksum"
	case strings.Contains(m, "timeout") || strings.Contains(m, "connection") || strings.Contains(m, "network") || strings.Contains(m, "i/o") || strings.Contains(m, "eof") || strings.Contains(m, "reset by peer"):
		return "network"
	case strings.Contains(m, "low level retry") || strings.Contains(m, "retrying"):
		return "retry"
	case strings.Contains(m, "error") || strings.Contains(m, "failed to"):
		return "error"
	}
	return ""
}

// telFinish computes the summary, persists the record, and keeps it in memory for
// quick reads. concurrency comes from the destination remote's rclone.conf.
func telFinish(jobID string) {
	telMu.Lock()
	t := telStore[jobID]
	telMu.Unlock()
	if t == nil {
		return
	}

	conc := 4 // rclone default
	if rn := remoteOfDst(t.Dst); rn != "" && !strings.HasPrefix(t.Dst, "/") {
		if conf, _ := rclone.Remotes(rcloneConfPath()); conf != nil {
			if c := confInt(conf[rn], "upload_concurrency"); c > 0 {
				conc = c
			}
		}
	}

	telMu.Lock()
	defer telMu.Unlock()
	dur := int64(time.Since(t.startNS).Seconds())
	if dur < 1 {
		dur = 1
	}
	var total int64
	if n := len(t.Samples); n > 0 {
		total = t.Samples[n-1].Bytes
	}
	floods := 0
	for _, e := range t.Events {
		if e.Kind == "flood" {
			floods++
		}
	}
	// per-connection estimate: peak aggregate throughput shared over the streams
	// that were actually running (active files × upload_concurrency).
	streams := t.peakActive * conc
	if streams < 1 {
		streams = conc
	}
	t.Summary = &telSummary{
		DurationSec: dur, Bytes: total, Files: len(t.Files),
		AvgSpeed: total / dur, PeakSpeed: t.peakSpeed, PeakActive: t.peakActive,
		Errors: countErrEvents(t.Events), FloodHits: floods, Throttled: floods > 0,
		PerConnEst: t.peakSpeed / int64(streams), Concurrency: conc,
	}
	persistTelemetry(t)
	delete(telStore, jobID) // drop from RAM; reads come from disk afterwards
}

// ── persistence (compact, split, gzipped) ─────────────────────────────────────
//
// Two files per job: a small <id>.sum.json (meta + summary + events + files, read
// for overviews) and <id>.series.json.gz — the time-series stored columnar
// (parallel arrays, no repeated keys) then gzipped. Columnar+gzip on a monotonic
// series is ~15-20× smaller than the pretty array-of-objects, at full resolution.

func sumRel(id string) string    { return telDir + "/" + id + ".sum.json" }
func seriesRel(id string) string { return telDir + "/" + id + ".series.json.gz" }

// telSeries is the columnar on-disk form of []telSample.
type telSeries struct {
	T      []int   `json:"t"`
	Speed  []int64 `json:"speed"`
	Bytes  []int64 `json:"bytes"`
	Active []int   `json:"active"`
	Errors []int   `json:"errors"`
}

type telSum struct {
	JobID     string              `json:"job_id"`
	StartedAt string              `json:"started_at"`
	Dst       string              `json:"dst"`
	Files     map[string]*telFile `json:"files"`
	Events    []telEvent          `json:"events"`
	Summary   *telSummary         `json:"summary"`
}

func persistTelemetry(t *telemetry) {
	sum := telSum{JobID: t.JobID, StartedAt: t.StartedAt, Dst: t.Dst, Files: t.Files, Events: t.Events, Summary: t.Summary}
	if b, err := json.Marshal(sum); err == nil { // compact (no indent)
		store.WriteText(sumRel(t.JobID), string(b))
	}
	cs := telSeries{}
	for _, s := range t.Samples {
		cs.T = append(cs.T, s.T)
		cs.Speed = append(cs.Speed, s.Speed)
		cs.Bytes = append(cs.Bytes, s.Bytes)
		cs.Active = append(cs.Active, s.Active)
		cs.Errors = append(cs.Errors, s.Errors)
	}
	if b, err := json.Marshal(cs); err == nil {
		writeGz(seriesRel(t.JobID), b)
	}
}

func writeGz(rel string, data []byte) {
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	_, _ = zw.Write(data)
	if zw.Close() != nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	full := store.Base + "/" + rel
	e := executor.Get()
	_ = e.MakeDirs(ctx, store.Base+"/"+telDir)
	_ = e.Upload(ctx, full, &buf)
}

func readGz(rel string) ([]byte, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	rc, err := executor.Get().Download(ctx, store.Base+"/"+rel)
	if err != nil || rc == nil {
		return nil, false
	}
	defer rc.Close()
	zr, err := gzip.NewReader(rc)
	if err != nil {
		return nil, false
	}
	defer zr.Close()
	b, err := io.ReadAll(zr)
	if err != nil {
		return nil, false
	}
	return b, true
}

func countErrEvents(ev []telEvent) int {
	n := 0
	for _, e := range ev {
		if e.Kind != "retry" {
			n++
		}
	}
	return n
}

func getTelemetry(jobID string) (*telemetry, bool) {
	telMu.Lock()
	t := telStore[jobID]
	telMu.Unlock()
	if t != nil {
		return t, true // live (running) — full record in memory
	}
	// finished: reassemble from the compact summary + gzipped columnar series
	txt, ok := store.ReadText(sumRel(jobID))
	if !ok {
		return nil, false
	}
	var sum telSum
	if json.Unmarshal([]byte(txt), &sum) != nil || sum.JobID == "" {
		return nil, false
	}
	out := &telemetry{
		JobID: sum.JobID, StartedAt: sum.StartedAt, Dst: sum.Dst,
		Files: sum.Files, Events: sum.Events, Summary: sum.Summary,
	}
	if b, ok := readGz(seriesRel(jobID)); ok {
		var cs telSeries
		if json.Unmarshal(b, &cs) == nil {
			for i := range cs.T {
				out.Samples = append(out.Samples, telSample{T: cs.T[i], Speed: cs.Speed[i], Bytes: cs.Bytes[i], Active: cs.Active[i], Errors: cs.Errors[i]})
			}
		}
	}
	return out, true
}

// ── analysis rules (data-driven; consumed by P2/P3) ───────────────────────────

type telFinding struct {
	Severity string         `json:"severity"` // good | warn | bad
	Title    string         `json:"title"`
	Detail   string         `json:"detail"`
	Suggest  map[string]any `json:"suggest,omitempty"` // task opts to apply (P3)
}

// analyzeTelemetry turns a finished record into human findings + suggested task
// tweaks. Rules are deliberately small and additive so they're easy to grow.
func analyzeTelemetry(t *telemetry) []telFinding {
	var out []telFinding
	s := t.Summary
	if s == nil {
		return out
	}
	kinds := map[string]int{}
	var firstFlood = -1
	for _, e := range t.Events {
		kinds[e.Kind]++
		if e.Kind == "flood" && firstFlood < 0 {
			firstFlood = e.T
		}
	}

	if s.FloodHits > 0 {
		out = append(out, telFinding{
			Severity: "bad",
			Title:    "Rate-limited (FLOOD_WAIT / 429)",
			Detail:   "Hit the provider's request-rate limit " + plural(s.FloodHits, "time", "times") + " (first at " + secs(firstFlood) + "). Pace requests down to avoid bans.",
			Suggest:  map[string]any{"tpslimit": 4, "transfers": 2, "checkers": 2},
		})
	}
	if kinds["auth"] > 0 {
		out = append(out, telFinding{Severity: "bad", Title: "Auth / token errors", Detail: "Saw 401/403/token errors — the remote token may be expired or lacks permission."})
	}
	if kinds["quota"] > 0 {
		out = append(out, telFinding{Severity: "bad", Title: "Quota exceeded", Detail: "The remote reported a storage/quota limit — rotate to another remote or raise the uploader cap."})
	}
	if kinds["network"] >= 3 {
		out = append(out, telFinding{
			Severity: "warn", Title: "Frequent network errors",
			Detail:  "Many connection/timeout errors — too many parallel streams can overwhelm the link.",
			Suggest: map[string]any{"transfers": 2},
		})
	}
	if v := speedVariance(t.Samples); v > 0.6 && s.Errors > 0 {
		out = append(out, telFinding{
			Severity: "warn", Title: "Unstable throughput",
			Detail:  "Speed swung widely (" + pct(v) + " variance) alongside errors — try fewer parallel transfers.",
			Suggest: map[string]any{"transfers": 2},
		})
	}
	if s.FloodHits == 0 && s.Errors == 0 && s.Bytes > 0 {
		out = append(out, telFinding{
			Severity: "good", Title: "Healthy run",
			Detail: "No rate-limits or errors. Avg " + humanBytes(s.AvgSpeed) + "/s, peak " + humanBytes(s.PeakSpeed) + "/s; est. " + humanBytes(s.PerConnEst) + "/s per connection.",
		})
	}
	return out
}

// speedVariance returns the coefficient of variation (stddev/mean) of sample speeds.
func speedVariance(ss []telSample) float64 {
	if len(ss) < 3 {
		return 0
	}
	var sum, n float64
	for _, s := range ss {
		if s.Speed > 0 {
			sum += float64(s.Speed)
			n++
		}
	}
	if n < 3 {
		return 0
	}
	mean := sum / n
	var sq float64
	for _, s := range ss {
		if s.Speed > 0 {
			d := float64(s.Speed) - mean
			sq += d * d
		}
	}
	if mean == 0 {
		return 0
	}
	return math.Sqrt(sq/n) / mean
}

func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return strconv.Itoa(n) + " " + many
}
func secs(t int) string {
	if t < 0 {
		return "—"
	}
	return (time.Duration(t) * time.Second).String()
}
func pct(v float64) string { return strconv.Itoa(int(v*100)) + "%" }

// ── endpoint ──────────────────────────────────────────────────────────────────

func transferTelemetry(w http.ResponseWriter, req *http.Request) {
	t, ok := getTelemetry(chi.URLParam(req, "id"))
	if !ok {
		http.Error(w, "no telemetry", http.StatusNotFound)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"job_id": t.JobID, "started_at": t.StartedAt, "dst": t.Dst,
		"samples": t.Samples, "files": t.Files, "events": t.Events,
		"summary": t.Summary, "findings": analyzeTelemetry(t),
	})
}
