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

type transferItem struct {
	Path  string `json:"path"`   // remote:path or /local/path
	IsDir bool   `json:"is_dir"` // dirs get their name appended to dest (rclone merges contents otherwise)
}

// transferOpts mirrors the common rclone transfer flags (whitelisted — we never
// pass raw flag strings, to avoid argument injection).
type transferOpts struct {
	Transfers          int         `json:"transfers"`
	Checkers           int         `json:"checkers"`
	Bwlimit            string      `json:"bwlimit"`
	Tpslimit           int         `json:"tpslimit"`
	Retries            int         `json:"retries"`
	IgnoreExisting     bool        `json:"ignore_existing"`
	Update             bool        `json:"update"`
	CreateEmptySrcDirs bool        `json:"create_empty_src_dirs"`
	NoTraverse         bool        `json:"no_traverse"`
	OneFileSystem      bool        `json:"one_file_system"`
	FastList           bool        `json:"fast_list"`
	Compare            string      `json:"compare"`     // "" | checksum | size-only | ignore-size
	SyncDelete         string      `json:"sync_delete"` // during | after | before (sync only)
	Include            []string    `json:"include"`
	Exclude            []string    `json:"exclude"`
	Extra              []extraFlag `json:"extra"` // free-form rclone flags (from the flag browser)
}

type extraFlag struct {
	Flag  string `json:"flag"`
	Value string `json:"value"`
}

var (
	bwlimitRE = regexp.MustCompile(`^[0-9.]+[bBkKmMgGtTi]*(:[0-9.]+[bBkKmMgGtTi]*)?$`)
	flagRE    = regexp.MustCompile(`^--[a-z0-9][a-z0-9-]*$`)
)

// transferFlags turns whitelisted opts into rclone argv.
func transferFlags(op string, o transferOpts, dryRun bool) []string {
	var f []string
	add := func(name, val string) { f = append(f, name, val) }
	if dryRun {
		f = append(f, "--dry-run")
	}
	if o.Transfers > 0 && o.Transfers <= 64 {
		add("--transfers", strconv.Itoa(o.Transfers))
	}
	if o.Checkers > 0 && o.Checkers <= 64 {
		add("--checkers", strconv.Itoa(o.Checkers))
	}
	if o.Tpslimit > 0 && o.Tpslimit <= 1000 {
		add("--tpslimit", strconv.Itoa(o.Tpslimit))
	}
	if o.Retries > 0 && o.Retries <= 100 {
		add("--retries", strconv.Itoa(o.Retries))
	}
	if o.Bwlimit != "" && bwlimitRE.MatchString(o.Bwlimit) {
		add("--bwlimit", o.Bwlimit)
	}
	if o.IgnoreExisting {
		f = append(f, "--ignore-existing")
	}
	if o.Update {
		f = append(f, "--update")
	}
	if o.CreateEmptySrcDirs {
		f = append(f, "--create-empty-src-dirs")
	}
	if o.NoTraverse {
		f = append(f, "--no-traverse")
	}
	if o.OneFileSystem {
		f = append(f, "--one-file-system")
	}
	if o.FastList {
		f = append(f, "--fast-list")
	}
	switch o.Compare {
	case "checksum":
		f = append(f, "--checksum")
	case "size-only":
		f = append(f, "--size-only")
	case "ignore-size":
		f = append(f, "--ignore-size")
	}
	if op == "sync" {
		switch o.SyncDelete {
		case "after":
			f = append(f, "--delete-after")
		case "before":
			f = append(f, "--delete-before")
		case "during":
			f = append(f, "--delete-during")
		}
	}
	for _, p := range o.Include {
		if p = strings.TrimSpace(p); p != "" && !strings.ContainsAny(p, "\n\r") {
			add("--include", p)
		}
	}
	for _, p := range o.Exclude {
		if p = strings.TrimSpace(p); p != "" && !strings.ContainsAny(p, "\n\r") {
			add("--exclude", p)
		}
	}
	for _, e := range o.Extra {
		if !flagRE.MatchString(e.Flag) || strings.ContainsAny(e.Value, "\n\r") {
			continue
		}
		if e.Value == "" {
			f = append(f, e.Flag)
		} else {
			add(e.Flag, e.Value)
		}
	}
	return f
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
	j := jobs.Create(transferLabel(b.Op, b.Items, b.Dst), b.Op)
	go runTransfer(j.ID, b.Op, b.Items, b.Dst, b.DryRun, b.Opts)
	writeJSON(w, http.StatusOK, map[string]any{"job_id": j.ID})
}

func transferLabel(op string, items []transferItem, dst string) string {
	return op + ": " + summarize(items) + " → " + dst
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
func runTransfer(jobID, op string, items []transferItem, dst string, dryRun bool, opts transferOpts) {
	jobs.SetStatus(jobID, "running")
	startedAt := time.Now().UTC().Format(time.RFC3339)
	setStart(jobID, startedAt)
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
	conf := rcloneConfPath()
	flags := transferFlags(op, opts, dryRun)
	base := []string{"--use-json-log", "--stats", "1s", "--stats-file-name-length", "0", "-v"}

	// Group selected items by their parent, then run ONE rclone command per group
	// with --filter rules (like RcloneBrowser): rclone transfers the group in
	// parallel and preserves each item's name under the destination. Different
	// parents/remotes become separate sequential commands.
	order := []string{}
	groups := map[string][]string{}
	for _, it := range items {
		p := endpointParent(it.Path)
		if _, ok := groups[p]; !ok {
			order = append(order, p)
		}
		groups[p] = append(groups[p], endpointBase(it.Path))
	}

	failed := false
	for _, parent := range order {
		if ctx.Err() != nil {
			break
		}
		args := []string{"rclone", "--config", conf, op, parent, dst}
		args = append(args, base...)
		args = append(args, flags...)
		for _, n := range groups[parent] {
			args = append(args, "--filter", "+ /"+n, "--filter", "+ /"+n+"/**")
		}
		args = append(args, "--filter", "- *")
		jobs.PushLog(jobID, "$ "+strings.Join(args, " "))
		code, err := streamTransfer(ctx, jobID, args)
		if err != nil {
			jobs.PushLog(jobID, "ERROR: "+err.Error())
			failed = true
			break
		}
		if code != 0 {
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
			}
			if rec.Msg != "" {
				jobs.PushLog(jobID, rec.Msg)
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

// endpointParent returns the parent dir of a "remote:path" or "/local/path".
func endpointParent(p string) string {
	if i := strings.Index(p, ":"); i > 0 && !strings.HasPrefix(p, "/") {
		base, rest := p[:i+1], p[i+1:]
		if j := strings.LastIndex(rest, "/"); j >= 0 {
			return base + rest[:j]
		}
		return base
	}
	if j := strings.LastIndex(p, "/"); j > 0 {
		return p[:j]
	}
	return "/"
}

func endpointBase(p string) string {
	if i := strings.Index(p, ":"); i > 0 && !strings.HasPrefix(p, "/") {
		return path.Base(p[i+1:])
	}
	return path.Base(p)
}

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
