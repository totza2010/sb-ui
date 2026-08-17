package api

import (
	"context"
	"encoding/json"
	"net/http"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"

	"sb-ui/internal/executor"
	"sb-ui/internal/jobs"
	"sb-ui/internal/rcloneexec"
	"sb-ui/internal/store"
)

// Transfers drive rclone directly (browse + copy/move/sync) for remote-to-remote
// and remote↔local work that the disk file manager can't reach. We shell out via
// the executor (local/SSH) and stream transfers into the job/WS log.

var remoteNameRE = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)

type lsEntry struct {
	Name  string `json:"name"`
	IsDir bool   `json:"is_dir"`
	Size  int64  `json:"size"`
}

// rcloneLs lists a remote path via `rclone lsjson remote:path`.
func rcloneLs(w http.ResponseWriter, req *http.Request) {
	remote := req.URL.Query().Get("remote")
	if !remoteNameRE.MatchString(remote) {
		http.Error(w, "Invalid remote", http.StatusBadRequest)
		return
	}
	rel := strings.TrimPrefix(path.Clean("/"+req.URL.Query().Get("path")), "/")
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	rc, out, _ := executor.Get().Run(ctx, []string{
		"rclone", "--config", rcloneConfPath(), "lsjson", remote + ":" + rel,
	}, "")
	entries := []lsEntry{}
	if rc == 0 {
		var raw []struct {
			Name  string
			Size  int64
			IsDir bool
		}
		if json.Unmarshal([]byte(out), &raw) == nil {
			for _, e := range raw {
				sz := e.Size
				if e.IsDir || sz < 0 {
					sz = 0
				}
				entries = append(entries, lsEntry{Name: e.Name, IsDir: e.IsDir, Size: sz})
			}
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"remote": remote, "path": rel, "entries": entries, "ok": rc == 0})
}

// rcloneMkdir creates a folder on a remote (rclone mkdir remote:path).
func rcloneMkdir(w http.ResponseWriter, req *http.Request) {
	var b struct {
		Remote string `json:"remote"`
		Path   string `json:"path"`
	}
	_ = json.NewDecoder(req.Body).Decode(&b)
	if !remoteNameRE.MatchString(b.Remote) {
		http.Error(w, "Invalid remote", http.StatusBadRequest)
		return
	}
	rel := strings.TrimPrefix(path.Clean("/"+b.Path), "/")
	if rel == "" {
		http.Error(w, "Path required", http.StatusBadRequest)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	rc, out, err := executor.Get().Run(ctx, []string{"rclone", "--config", rcloneConfPath(), "mkdir", b.Remote + ":" + rel}, "")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if rc != 0 {
		http.Error(w, strings.TrimSpace(out), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// The transfer request model and the argv/flag rendering live in internal/rcloneexec, the
// only place allowed to build an rclone command line. These aliases keep the api-local
// names (and their JSON shapes, which persisted tasks depend on) pointing at it.
type transferItem = rcloneexec.Item
type transferOpts = rcloneexec.Opts
type extraFlag = rcloneexec.ExtraFlag

// transferFlags renders whitelisted opts as rclone argv.
func transferFlags(op string, o transferOpts, dryRun bool) []string {
	return rcloneexec.Flags(op, o, dryRun)
}

// ── rclone flag catalog (per-backend options, e.g. teldrive-specific) ─────────

type flagInfo struct {
	Flag string `json:"flag"`
	Help string `json:"help"`
	Type string `json:"type"`
}

var (
	provMu      sync.Mutex
	provCache   map[string][]flagInfo
	globalCache []flagInfo
	flagsLoaded bool
)

// rcloneProviders returns global rclone flags + each backend's options as flags
// (--<prefix>-<opt>) with help text, so the UI can offer a described, selectable
// flag list (incl. backend-specific ones like teldrive). Cached.
func rcloneProviders(w http.ResponseWriter, _ *http.Request) {
	provMu.Lock()
	defer provMu.Unlock()
	if !flagsLoaded {
		provCache = loadProviders()
		globalCache = loadGlobalFlags()
		flagsLoaded = true
	}
	writeJSON(w, http.StatusOK, map[string]any{"global": globalCache, "backends": provCache})
}

// loadGlobalFlags reads the main rclone options (transfers, fast-list, …) via the
// in-process rc call (no daemon needed).
func loadGlobalFlags() []flagInfo {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	rc, o, _ := executor.Get().Run(ctx, []string{"rclone", "rc", "--loopback", "options/info"}, "")
	if rc != 0 {
		return nil
	}
	var groups map[string][]struct {
		Name      string
		FieldName string
		Help      string
		Type      string
	}
	if json.Unmarshal([]byte(o), &groups) != nil {
		return nil
	}
	seen := map[string]bool{}
	var out []flagInfo
	for _, opts := range groups {
		for _, op := range opts {
			name := op.Name
			if name == "" {
				name = op.FieldName
			}
			flag := "--" + strings.ReplaceAll(strings.ToLower(name), "_", "-")
			if name == "" || seen[flag] {
				continue
			}
			seen[flag] = true
			help := op.Help
			if i := strings.IndexByte(help, '\n'); i > 0 {
				help = help[:i]
			}
			out = append(out, flagInfo{Flag: flag, Help: help, Type: op.Type})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Flag < out[j].Flag })
	return out
}

func loadProviders() map[string][]flagInfo {
	out := map[string][]flagInfo{}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	rc, o, _ := executor.Get().Run(ctx, []string{"rclone", "config", "providers"}, "")
	if rc != 0 {
		return out
	}
	var provs []struct {
		Name    string
		Prefix  string
		Options []struct {
			Name string
			Help string
			Type string
		}
	}
	if json.Unmarshal([]byte(o), &provs) != nil {
		return out
	}
	for _, p := range provs {
		prefix := p.Prefix
		if prefix == "" {
			prefix = p.Name
		}
		fs := make([]flagInfo, 0, len(p.Options))
		for _, op := range p.Options {
			help := op.Help
			if i := strings.IndexByte(help, '\n'); i > 0 {
				help = help[:i] // first line only
			}
			fs = append(fs, flagInfo{
				Flag: "--" + prefix + "-" + strings.ReplaceAll(op.Name, "_", "-"),
				Help: help, Type: op.Type,
			})
		}
		out[prefix] = fs
	}
	return out
}

// rcloneTransfer launches a copy/move/sync of one or more items into a dest
// folder, as a single streamed job (each item run sequentially).
func rcloneTransfer(w http.ResponseWriter, req *http.Request) {
	var b struct {
		Op     string         `json:"op"` // copy | move | sync
		Items  []transferItem `json:"items"`
		Dst    string         `json:"dst"` // destination folder
		DryRun bool           `json:"dry_run"`
		Opts   transferOpts   `json:"opts"`
		Queue  bool           `json:"queue"` // run via the sequential queue instead of now
	}
	_ = json.NewDecoder(req.Body).Decode(&b)
	if b.Op != "copy" && b.Op != "move" && b.Op != "sync" {
		http.Error(w, "op must be copy/move/sync", http.StatusBadRequest)
		return
	}
	if len(b.Items) == 0 || !validEndpoint(b.Dst) {
		http.Error(w, "Invalid items/dst", http.StatusBadRequest)
		return
	}
	for _, it := range b.Items {
		if !validEndpoint(it.Path) {
			http.Error(w, "Invalid source: "+it.Path, http.StatusBadRequest)
			return
		}
	}
	if b.Queue {
		id := enqueueTask(Task{Op: b.Op, Items: b.Items, Dst: b.Dst, DryRun: b.DryRun, Opts: b.Opts})
		writeJSON(w, http.StatusOK, map[string]any{"job_id": id})
		return
	}
	j := jobs.Create(transferLabel(b.Op, b.Items, b.Dst, b.DryRun), b.Op)
	go runTransfer(j.ID, "", b.Op, b.Items, b.Dst, b.DryRun, b.Opts)
	writeJSON(w, http.StatusOK, map[string]any{"job_id": j.ID})
}

// transferLabel names a job for the Activity list and the persisted history.
//
// A dry run says so in the name. Without it a preview and a real transfer were labelled
// identically, so a scheduled run that had quietly lost its dry-run flag looked exactly like
// the harmless one from the day before — the only way to tell them apart was to open the log
// and look for --dry-run. It cuts both ways: a real move must be recognisable as one at a
// glance, in history, forever.
func transferLabel(op string, items []transferItem, dst string, dryRun bool) string {
	label := op + ": " + summarize(items) + " → " + dst
	if dryRun {
		return "DRY-RUN " + label
	}
	return label
}

// cancel registry: lets the Stop endpoint kill a running transfer.
var (
	cancelMu  sync.Mutex
	cancelFns = map[string]context.CancelFunc{}
)

func stopTransfer(w http.ResponseWriter, req *http.Request) {
	id := chi.URLParam(req, "id")
	cancelMu.Lock()
	fn := cancelFns[id]
	cancelMu.Unlock()
	if fn == nil {
		http.Error(w, "Not running", http.StatusNotFound)
		return
	}
	fn()
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// runTransfer executes a transfer (one job, items sequentially), streaming output
// into the job log and live stats. Shared by immediate runs, tasks, and the queue.
const (
	rcExitMaxTransfer = rcloneexec.ExitMaxTransfer
	rcExitNoTransfer  = rcloneexec.ExitNoTransfer
)

func classifyExit(code int) (failed, capped bool) { return rcloneexec.ClassifyExit(code) }

func runTransfer(jobID, taskID, op string, items []transferItem, dst string, dryRun bool, opts transferOpts) {
	jobs.SetStatus(jobID, "running")
	startedAt := time.Now().UTC().Format(time.RFC3339)
	setStart(jobID, startedAt)
	telStart(jobID, taskID, dst)
	ctx, cancel := context.WithCancel(context.Background())
	cancelMu.Lock()
	cancelFns[jobID] = cancel
	cancelMu.Unlock()
	defer func() {
		cancel()
		cancelMu.Lock()
		delete(cancelFns, jobID)
		cancelMu.Unlock()
	}()
	// One argv per group of same-parent items; rcloneexec owns how they are built, so the
	// command that runs here is the same one a preview would show.
	failed := false
	for _, args := range rcloneexec.Argv(rcloneConfPath(), op, items, dst, dryRun, opts) {
		if ctx.Err() != nil {
			break
		}
		jobs.PushLog(jobID, "$ "+strings.Join(args, " "))
		code, err := streamTransfer(ctx, jobID, args)
		if err != nil {
			jobs.PushLog(jobID, "ERROR: "+err.Error())
			failed = true
			break
		}
		bad, capped := classifyExit(code)
		if capped {
			// The uploader passes each remote's remaining daily allowance as --max-transfer, so
			// this is the designed end of a capped run, not an error: rclone stopped at a whole-file
			// boundary (--cutoff-mode cautious) with the allowance spent. Reporting it as "failed"
			// made every correct capped upload look broken. The allowance is gone, so stop here and
			// let the caller rotate to the next remote.
			jobs.PushLog(jobID, "\nReached the --max-transfer limit (this remote's daily cap) — stopped cleanly at a file boundary. This is the expected end of a capped run, not a failure; the rotation continues on the next remote.")
			break
		}
		if bad {
			failed = true
			break
		}
	}
	switch {
	case ctx.Err() != nil:
		jobs.PushLog(jobID, "\nStopped by user.")
		jobs.SetStatus(jobID, "stopped")
	case failed:
		jobs.SetStatus(jobID, "failed")
	default:
		jobs.SetStatus(jobID, "completed")
	}
	saveSummary(jobID, startedAt, time.Now().UTC().Format(time.RFC3339))
	telFinish(jobID)
}

// ── live transfer stats (per job) ─────────────────────────────────────────────

type fileStat struct {
	Name       string  `json:"name"`
	Size       int64   `json:"size"`
	Bytes      int64   `json:"bytes"`
	Percentage int     `json:"percentage"`
	Speed      float64 `json:"speed"`
	SpeedAvg   float64 `json:"speedAvg"`
	Eta        float64 `json:"eta"`
}

type transferStats struct {
	Bytes          int64      `json:"bytes"`
	TotalBytes     int64      `json:"totalBytes"`
	Speed          float64    `json:"speed"`
	Eta            float64    `json:"eta"`
	Transfers      int        `json:"transfers"`
	TotalTransfers int        `json:"totalTransfers"`
	Checks         int        `json:"checks"`
	TotalChecks    int        `json:"totalChecks"`
	ElapsedTime    float64    `json:"elapsedTime"`
	Errors         int        `json:"errors"`
	Transferring   []fileStat `json:"transferring"`
}

var (
	statsMu    sync.Mutex
	statsStore = map[string]*transferStats{}
	startStore = map[string]string{} // jobID -> started RFC3339 (for live jobs)
	floodStore = map[string]bool{}   // jobID -> hit a rate-limit/flood error (kept across stats updates)
)

func setStats(id string, s *transferStats) { statsMu.Lock(); statsStore[id] = s; statsMu.Unlock() }

// markFlood / floodHit track whether a job tripped a provider rate-limit so the
// uploader can pause that remote (Telegram FLOOD_WAIT, Drive 429/rateLimitExceeded).
func markFlood(id string)     { statsMu.Lock(); floodStore[id] = true; statsMu.Unlock() }
func floodHit(id string) bool { statsMu.Lock(); defer statsMu.Unlock(); return floodStore[id] }

func isFloodMsg(m string) bool {
	m = strings.ToLower(m)
	return strings.Contains(m, "flood_wait") ||
		strings.Contains(m, "too many requests") ||
		strings.Contains(m, "toomanyrequests") ||
		strings.Contains(m, "ratelimitexceeded") ||
		strings.Contains(m, "userratelimitexceeded") ||
		strings.Contains(m, " 429 ") || strings.Contains(m, "(429)") || strings.Contains(m, "error 429")
}
func setStart(id, t string) { statsMu.Lock(); startStore[id] = t; statsMu.Unlock() }

// transferSummary is the final snapshot persisted when a transfer job ends, so
// completed jobs still show their stats + timing after a restart.
type transferSummary struct {
	Stats      *transferStats `json:"stats"`
	StartedAt  string         `json:"started_at"`
	FinishedAt string         `json:"finished_at"`
}

const summariesRel = "cache/transfer_summaries.json"

var (
	sumMu     sync.Mutex
	summaries map[string]*transferSummary
)

func ensureSummaries() { // call under sumMu
	if summaries != nil {
		return
	}
	summaries = map[string]*transferSummary{}
	store.ReadJSON(summariesRel, &summaries)
	if summaries == nil {
		summaries = map[string]*transferSummary{}
	}
}

func saveSummary(jobID, started, finished string) {
	statsMu.Lock()
	s := statsStore[jobID]
	statsMu.Unlock()
	sumMu.Lock()
	defer sumMu.Unlock()
	ensureSummaries()
	summaries[jobID] = &transferSummary{Stats: s, StartedAt: started, FinishedAt: finished}
	// Cap the persisted history (keep the most recent ~300 by finish time).
	if len(summaries) > 300 {
		oldest, oldestAt := "", ""
		for id, sm := range summaries {
			if oldestAt == "" || sm.FinishedAt < oldestAt {
				oldest, oldestAt = id, sm.FinishedAt
			}
		}
		delete(summaries, oldest)
	}
	store.WriteJSON(summariesRel, summaries)
}

// statsResp embeds the live/finished stats plus timing.
type statsResp struct {
	*transferStats
	StartedAt  string `json:"started_at,omitempty"`
	FinishedAt string `json:"finished_at,omitempty"`
}

func transferStatsHandler(w http.ResponseWriter, req *http.Request) {
	id := chi.URLParam(req, "id")
	statsMu.Lock()
	live := statsStore[id]
	started := startStore[id]
	statsMu.Unlock()
	if live != nil {
		writeJSON(w, http.StatusOK, statsResp{transferStats: live, StartedAt: started})
		return
	}
	sumMu.Lock()
	ensureSummaries()
	sum := summaries[id]
	sumMu.Unlock()
	if sum != nil {
		writeJSON(w, http.StatusOK, statsResp{transferStats: sum.Stats, StartedAt: sum.StartedAt, FinishedAt: sum.FinishedAt})
		return
	}
	// Fallback: reconstruct from the job log's final rclone stats block (covers
	// jobs from before summaries were persisted, or after a restart).
	if snap, _, cancel, ok := jobs.Subscribe(id); ok {
		cancel()
		if st := parseFinalStats(snap); st != nil {
			started := ""
			if d, ok := jobs.JobDict(id); ok {
				started, _ = d["created_at"].(string)
			}
			writeJSON(w, http.StatusOK, statsResp{transferStats: st, StartedAt: started})
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{})
}

// ── parse rclone's final text stats block from a job log (fallback) ───────────

var sizeRE = regexp.MustCompile(`([0-9.]+)\s*([KMGTP]i?)?B?`)

func parseSize(s string) float64 {
	m := sizeRE.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return 0
	}
	v, _ := strconv.ParseFloat(m[1], 64)
	switch strings.TrimSuffix(m[2], "i") {
	case "K":
		v *= 1 << 10
	case "M":
		v *= 1 << 20
	case "G":
		v *= 1 << 30
	case "T":
		v *= 1 << 40
	case "P":
		v *= 1 << 50
	}
	return v
}

func parseDur(s string) float64 {
	d, err := time.ParseDuration(strings.TrimSpace(s))
	if err != nil {
		return 0
	}
	return d.Seconds()
}

func firstInt(s string) int {
	f := strings.Fields(s)
	if len(f) == 0 {
		return 0
	}
	n, _ := strconv.Atoi(strings.TrimSpace(f[0]))
	return n
}

// parseFinalStats scans a job log for rclone's stats lines, keeping the last
// (final) values. Returns nil if no stats block is present.
func parseFinalStats(lines []string) *transferStats {
	st := &transferStats{}
	found := false
	for _, raw := range strings.Split(strings.Join(lines, "\n"), "\n") {
		ln := strings.TrimSpace(raw)
		switch {
		case strings.HasPrefix(ln, "Transferred:"):
			rest := strings.TrimSpace(strings.TrimPrefix(ln, "Transferred:"))
			parts := strings.Split(rest, ",")
			xy := strings.SplitN(parts[0], "/", 2)
			if strings.Contains(rest, "ETA") { // bytes line
				if len(xy) == 2 {
					st.Bytes, st.TotalBytes = int64(parseSize(xy[0])), int64(parseSize(xy[1]))
					found = true
				}
				if len(parts) >= 3 {
					st.Speed = parseSize(strings.TrimSuffix(strings.TrimSpace(parts[2]), "/s"))
				}
				if len(parts) >= 4 {
					st.Eta = parseDur(strings.TrimPrefix(strings.TrimSpace(parts[3]), "ETA "))
				}
			} else if len(xy) == 2 { // count line: "n / m"
				st.Transfers, st.TotalTransfers = firstInt(xy[0]), firstInt(xy[1])
				found = true
			}
		case strings.HasPrefix(ln, "Checks:"):
			xy := strings.SplitN(strings.Split(strings.TrimPrefix(ln, "Checks:"), ",")[0], "/", 2)
			if len(xy) == 2 {
				st.Checks, st.TotalChecks = firstInt(xy[0]), firstInt(xy[1])
			}
		case strings.HasPrefix(ln, "Errors:"):
			st.Errors = firstInt(strings.TrimPrefix(ln, "Errors:"))
		case strings.HasPrefix(ln, "Elapsed time:"):
			st.ElapsedTime = parseDur(strings.TrimPrefix(ln, "Elapsed time:"))
		}
	}
	if !found {
		return nil
	}
	return st
}

// streamTransfer runs one rclone command, parsing --use-json-log lines into the
// job log + live stats. Returns the exit code.
func streamTransfer(ctx context.Context, jobID string, args []string) (int, error) {
	s, err := executor.Get().RunStream(ctx, args, "", false)
	if err != nil {
		return -1, err
	}
	for line := range s.Lines {
		var rec struct {
			Msg   string         `json:"msg"`
			Stats *transferStats `json:"stats"`
		}
		if json.Unmarshal([]byte(line), &rec) == nil && (rec.Msg != "" || rec.Stats != nil) {
			if rec.Stats != nil {
				setStats(jobID, rec.Stats)
				telOnStats(jobID, rec.Stats)
			}
			if rec.Msg != "" {
				jobs.PushLog(jobID, rec.Msg)
				telOnLog(jobID, rec.Msg)
				if isFloodMsg(rec.Msg) {
					markFlood(jobID)
				}
			}
		} else {
			jobs.PushLog(jobID, line)
		}
	}
	return s.Exit(), nil
}

func endpointParent(p string) string { return rcloneexec.Parent(p) }
func endpointBase(p string) string   { return rcloneexec.Base(p) }

func summarize(items []transferItem) string {
	if len(items) == 1 {
		return endpointBase(items[0].Path)
	}
	return endpointBase(items[0].Path) + " +" + strconv.Itoa(len(items)-1)
}

// validEndpoint accepts "remote:path" or an absolute local path; rejects flags.
func validEndpoint(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" || strings.HasPrefix(s, "-") {
		return false
	}
	if strings.HasPrefix(s, "/") {
		return true // local path
	}
	i := strings.Index(s, ":")
	return i > 0 && remoteNameRE.MatchString(s[:i]) // remote:path
}

// ── shared helpers ────────────────────────────────────────────────────────────
// These are transfer-domain utilities that happened to live in the uploader. They moved
// here when the auto-upload rotation was lifted out, since fs and telemetry need them.

// duBytes returns the apparent size of a local path in bytes (0 on failure).
func duBytes(path string) int64 {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	rc, out, _ := executor.Get().Run(ctx, []string{"du", "-sb", "--", path}, "")
	if rc != 0 {
		return 0
	}
	if f := strings.Fields(out); len(f) > 0 {
		n, _ := strconv.ParseInt(f[0], 10, 64)
		return n
	}
	return 0
}

// remoteOfDst returns the remote name from an rclone "remote:path" destination. A local
// absolute path is returned unchanged.
func remoteOfDst(dst string) string {
	if i := strings.Index(dst, ":"); i > 0 && !strings.HasPrefix(dst, "/") {
		return dst[:i]
	}
	return dst
}

// confInt reads an integer from a parsed rclone remote's config section (0 when absent).
func confInt(p map[string]string, key string) int {
	if p == nil {
		return 0
	}
	n, _ := strconv.Atoi(strings.TrimSpace(p[key]))
	return n
}
