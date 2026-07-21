package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Teldrive file-integrity audit (phase A — Postgres only).
//
// Background: teldrive stores a file's size as reported by the client, but strips each
// part's size before saving (mapParts keeps only {id, salt}). An upload interrupted near
// the end therefore commits a full-size record backed by too few parts. At read time
// NewReader derives the chunk size from parts[0] and calculatePartByteRanges indexes
// r.parts by the computed part number — with no bounds check — so one short file panics
// the whole container.
//
// Because the DB has no part sizes, a true per-file verdict needs the part sizes fetched
// back from Telegram (phase B). What CAN be decided from Postgres alone is anything that
// holds for every possible chunk size:
//
//	size > parts * maxPartBytes  →  short, always
//
// No Telegram document can exceed maxPartBytes, so even if every part were the largest
// possible the bytes still don't add up. That yields zero false positives without
// assuming a chunk size. Everything softer is reported separately as a suspicion, and
// labelled as such, rather than being mixed in with the proven cases.
//
// The connection is read-only by construction: a user-supplied DSN, opened with
// default_transaction_read_only so a mistake cannot write to teldrive's tables.

const (
	// Ceiling used by the proven test. The default is Telegram's non-premium document
	// limit, which is safe but blunt: teldrive's real chunk size is typically 512 MiB, so
	// at 2 GiB almost no genuinely short file is provable. Setting this to the largest
	// chunk size this teldrive was ever configured with keeps the test assumption-free
	// (no part can exceed the configured chunk) while making it sharp enough to be useful.
	defaultMaxPartBytes = 2 << 30
	auditQueryTimeout   = 60 * time.Second
)

func teldriveMaxPart(db teldriveDB) int64 {
	if db.MaxPartBytes > 0 {
		return db.MaxPartBytes
	}
	return defaultMaxPartBytes
}

// teldrivePool opens a short-lived read-only pool against the teldrive database. The
// pool is deliberately tiny — teldrive itself reserves 25 connections and the audit must
// not compete with the running service.
func teldrivePool(ctx context.Context, dsn string) (*pgxpool.Pool, error) {
	dsn = strings.TrimSpace(dsn)
	if dsn == "" {
		return nil, errors.New("teldrive database URL not set")
	}
	pc, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, err
	}
	pc.MaxConns = 3
	pc.MaxConnLifetime = 5 * time.Minute
	if pc.ConnConfig.RuntimeParams == nil {
		pc.ConnConfig.RuntimeParams = map[string]string{}
	}
	// Belt and braces: every statement on this pool runs in a read-only transaction, and
	// unqualified table names resolve inside teldrive's schema (GORM TablePrefix).
	pc.ConnConfig.RuntimeParams["default_transaction_read_only"] = "on"
	pc.ConnConfig.RuntimeParams["search_path"] = "teldrive,public"
	pc.ConnConfig.RuntimeParams["application_name"] = "sb-ui-audit"
	return pgxpool.NewWithConfig(ctx, pc)
}

type teldriveDBInfo struct {
	OK        bool   `json:"ok"`
	Version   string `json:"version,omitempty"`
	Files     int64  `json:"files"`
	Uploads   int64  `json:"uploads"`
	Sessions  int64  `json:"sessions"`
	SchemaOK  bool   `json:"schema_ok"`
	Error     string `json:"error,omitempty"`
	CheckedAt string `json:"checked_at"`
}

// teldriveDBCheck verifies the DSN reaches a database that actually looks like teldrive's,
// rather than reporting a bare "connected" for any Postgres.
func teldriveDBCheck(ctx context.Context, dsn string) teldriveDBInfo {
	out := teldriveDBInfo{CheckedAt: time.Now().UTC().Format(time.RFC3339)}
	pool, err := teldrivePool(ctx, dsn)
	if err != nil {
		out.Error = err.Error()
		return out
	}
	defer pool.Close()

	if err := pool.QueryRow(ctx, "SELECT version()").Scan(&out.Version); err != nil {
		out.Error = err.Error()
		return out
	}
	out.OK = true

	// Presence of the three tables the audit relies on.
	err = pool.QueryRow(ctx, `
		SELECT (SELECT count(*) FROM teldrive.files),
		       (SELECT count(*) FROM teldrive.uploads),
		       (SELECT count(*) FROM teldrive.sessions)`).Scan(&out.Files, &out.Uploads, &out.Sessions)
	if err != nil {
		out.Error = "connected, but this does not look like a teldrive database: " + err.Error()
		return out
	}
	out.SchemaOK = true
	return out
}

// auditFile is one file flagged by the audit.
type auditFile struct {
	Remote    string `json:"remote"` // which teldrive instance this file belongs to
	ID        string `json:"id"`
	Name      string `json:"name"`
	Path      string `json:"path"` // resolved through the folder tree; "?/…" when the parent is unreachable
	ParentID  string `json:"parent_id,omitempty"`
	Size      int64  `json:"size"`
	Parts     int    `json:"parts"`
	ChannelID int64  `json:"channel_id"`
	Encrypted bool   `json:"encrypted"`
	MinNeeded int64  `json:"min_needed"` // parts required even at the maximum part size
	ShortBy   int64  `json:"short_by"`   // bytes unaccounted for at the maximum part size
	GuessNeed int64  `json:"guess_need"` // parts implied by the chunk-size hypothesis
	UpdatedAt string `json:"updated_at"`
	Verdict   string `json:"verdict"` // PROVEN_SHORT | SUSPECT | NO_PARTS | BAD_PARTS_TYPE
}

// auditStalled is an upload that stopped part-way and has not been cleaned up yet.
type auditStalled struct {
	Remote    string `json:"remote"`
	UploadID  string `json:"upload_id"`
	Name      string `json:"name"`
	Parts     int    `json:"parts"`
	Bytes     int64  `json:"bytes"`
	StartedAt string `json:"started_at"`
	LastPart  string `json:"last_part_at"`
}

// auditInstance is the per-instance outcome, so one unreachable database reports itself
// instead of failing the whole scan.
type auditInstance struct {
	Remote       string `json:"remote"`
	Scanned      int64  `json:"scanned"`
	MaxPartBytes int64  `json:"max_part_bytes"`
	TookMS       int64  `json:"took_ms"` // wall time for this instance, so a slow one is identifiable
	Error        string `json:"error,omitempty"`
}

type auditResult struct {
	Instances  []auditInstance `json:"instances"`
	Scanned    int64           `json:"scanned"`
	ChunkGuess int64           `json:"chunk_guess"`
	ActiveOnly bool            `json:"active_only"` // deleted (non-active) files were skipped
	Proven     []auditFile     `json:"proven"`
	Suspect    []auditFile     `json:"suspect"`
	Broken     []auditFile     `json:"broken"`   // parts missing or not a JSON array
	Orphans    []auditFile     `json:"orphans"`  // parent chain doesn't reach a root
	PathsOK    bool            `json:"paths_ok"` // folder tree resolved, so paths and orphans are meaningful
	Stalled    []auditStalled  `json:"stalled"`
	PartsTypes map[string]int  `json:"parts_types"`
	RanAt      string          `json:"ran_at"`
}

// teldrive soft-deletes: a removed file keeps its row and only its status changes, so
// without this filter an audit reports files the user already deleted. The column is
// checked rather than assumed — older teldrive schemas don't have it, and a missing
// column would otherwise fail the instance's whole scan with a confusing error.
func hasStatusColumn(ctx context.Context, pool *pgxpool.Pool) bool {
	var n int
	err := pool.QueryRow(ctx, `
		SELECT count(*) FROM information_schema.columns
		WHERE table_schema = 'teldrive' AND table_name = 'files' AND column_name = 'status'`).Scan(&n)
	return err == nil && n > 0
}

// filePredicate is the WHERE clause shared by every file query. It is assembled from
// constants only — never from user input — so it is safe to interpolate.
func filePredicate(activeOnly, hasStatus bool) string {
	if activeOnly && hasStatus {
		return "type = 'file' AND status = 'active'"
	}
	return "type = 'file'"
}

// folderTree resolves every folder's full path. All folders are pulled in one flat query
// and the chains are walked in Go — there are orders of magnitude fewer folders than
// files, and doing it here rather than with a recursive CTE means a detached folder can
// still be given the partial path it does have, which a walk down from the roots can never
// produce. Returns the id→path map and how many roots were found; zero roots means this
// schema doesn't mark them with a NULL parent, in which case reachability can't be judged
// and the orphan check is skipped rather than flagging everything.
//
// Paths of folders that do reach a root start with "/". Those that don't start with "?/",
// which is what marks a folder — and so every file under it — as unreachable.
func folderTree(ctx context.Context, pool *pgxpool.Pool) (map[string]string, int, error) {
	type node struct{ parent, name string }
	nodes := map[string]node{}
	rows, err := pool.Query(ctx, `
		SELECT id::text, COALESCE(parent_id::text, ''), COALESCE(name, '')
		FROM teldrive.files WHERE type = 'folder'`)
	if err != nil {
		return nil, 0, err
	}
	var roots int
	for rows.Next() {
		var id string
		var n node
		if rows.Scan(&id, &n.parent, &n.name) != nil {
			continue
		}
		nodes[id] = n
		if n.parent == "" {
			roots++
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, roots, err
	}

	// Walk up from each folder, memoising as we go. A chain that runs into a missing
	// parent, a cycle, or the depth cap keeps the names it collected and is marked "?/".
	paths := make(map[string]string, len(nodes))
	var resolve func(id string, depth int, seen map[string]bool) string
	resolve = func(id string, depth int, seen map[string]bool) string {
		if p, ok := paths[id]; ok {
			return p
		}
		n, ok := nodes[id]
		if !ok {
			return "?"
		}
		var prefix string
		switch {
		case n.parent == "":
			prefix = ""
		case depth >= 64 || seen[id]:
			prefix = "?" // depth cap or a parent cycle: stop here rather than spin
		default:
			seen[id] = true
			prefix = resolve(n.parent, depth+1, seen)
			delete(seen, id)
		}
		p := prefix + "/" + n.name
		paths[id] = p
		return p
	}
	for id := range nodes {
		resolve(id, 0, map[string]bool{})
	}
	return paths, roots, nil
}

// fullPath places a file inside the folder tree. A parent that isn't a known folder at all
// is reported as "?/" rather than silently rendering the bare name, since that is exactly
// the condition the orphan check exists to surface.
func fullPath(paths map[string]string, parentID, name string) string {
	if parentID == "" {
		return "/" + name
	}
	if dir, ok := paths[parentID]; ok {
		return dir + "/" + name
	}
	return "?/" + name
}

// auditOneDB runs the phase-A checks against a single teldrive instance. chunkGuess only
// drives the clearly labelled "suspect" list; the proven list never depends on it.
func auditOneDB(ctx context.Context, db teldriveDB, chunkGuess int64, activeOnly bool) (auditResult, error) {
	res := auditResult{
		ChunkGuess: chunkGuess,
		Proven:     []auditFile{}, Suspect: []auditFile{}, Broken: []auditFile{}, Orphans: []auditFile{},
		Stalled: []auditStalled{}, PartsTypes: map[string]int{},
	}
	maxPartBytes := teldriveMaxPart(db)
	pool, err := teldrivePool(ctx, db.DSN)
	if err != nil {
		return res, err
	}
	defer pool.Close()

	where := filePredicate(activeOnly, hasStatusColumn(ctx, pool))

	// Folder paths first: findings are far more useful with a path than a bare filename,
	// and the same tree decides which files are orphaned.
	paths, roots, perr := folderTree(ctx, pool)
	res.PathsOK = perr == nil && roots > 0

	// jsonb_array_length errors out on any row where parts isn't an array, which would
	// take down the whole scan — so the shape is surveyed first and those rows are
	// handled separately rather than being allowed to abort the query.
	rows, err := pool.Query(ctx, `
		SELECT COALESCE(jsonb_typeof(parts), 'null') AS t, count(*)
		FROM teldrive.files WHERE `+where+` GROUP BY 1`)
	if err != nil {
		return res, err
	}
	for rows.Next() {
		var t string
		var n int
		if rows.Scan(&t, &n) == nil {
			res.PartsTypes[t] = n
			// Every scanned row lands in exactly one bucket, so the total falls out of this
			// survey — no need for a separate count(*) pass over the same table.
			res.Scanned += int64(n)
		}
	}
	rows.Close()

	// Files whose parts column can't be counted at all.
	rows, err = pool.Query(ctx, `
		SELECT id::text, name, COALESCE(parent_id::text, ''), COALESCE(size, 0), COALESCE(channel_id, 0),
		       COALESCE(encrypted, false), COALESCE(updated_at, created_at),
		       COALESCE(jsonb_typeof(parts), 'null')
		FROM teldrive.files
		WHERE `+where+` AND COALESCE(size, 0) > 0
		  AND (parts IS NULL OR jsonb_typeof(parts) <> 'array' OR jsonb_array_length(parts) = 0)
		ORDER BY size DESC LIMIT 500`)
	if err != nil {
		return res, err
	}
	for rows.Next() {
		var f auditFile
		var ts time.Time
		var typ string
		if rows.Scan(&f.ID, &f.Name, &f.ParentID, &f.Size, &f.ChannelID, &f.Encrypted, &ts, &typ) != nil {
			continue
		}
		f.Remote, f.Path = db.Remote, fullPath(paths, f.ParentID, f.Name)
		f.UpdatedAt = ts.UTC().Format(time.RFC3339)
		if typ == "array" {
			f.Verdict = "NO_PARTS"
		} else {
			f.Verdict = "BAD_PARTS_TYPE"
		}
		res.Broken = append(res.Broken, f)
	}
	rows.Close()

	// The main pass. Guarded by jsonb_typeof so the malformed rows above can't abort it.
	if chunkGuess <= 0 {
		chunkGuess = 512 << 20
		res.ChunkGuess = chunkGuess
	}
	// The screening threshold must be <= the proven bound; a coarser hypothesis would
	// filter out files the proven test would otherwise catch.
	screen := chunkGuess
	if screen > maxPartBytes {
		screen = maxPartBytes
	}
	rows, err = pool.Query(ctx, `
		SELECT id::text, name, COALESCE(parent_id::text, ''), size, COALESCE(channel_id, 0),
		       COALESCE(encrypted, false), jsonb_array_length(parts) AS parts,
		       COALESCE(updated_at, created_at)
		FROM teldrive.files
		WHERE `+where+` AND size > 0
		  AND jsonb_typeof(parts) = 'array' AND jsonb_array_length(parts) > 0
		  AND size > jsonb_array_length(parts)::bigint * $1
		ORDER BY size DESC LIMIT 2000`, screen)
	if err != nil {
		return res, err
	}
	maxPart := maxPartBytes
	for rows.Next() {
		var f auditFile
		var ts time.Time
		if rows.Scan(&f.ID, &f.Name, &f.ParentID, &f.Size, &f.ChannelID, &f.Encrypted, &f.Parts, &ts) != nil {
			continue
		}
		f.Remote, f.Path = db.Remote, fullPath(paths, f.ParentID, f.Name)
		f.UpdatedAt = ts.UTC().Format(time.RFC3339)
		f.GuessNeed = ceilDiv(f.Size, chunkGuess)
		f.MinNeeded = ceilDiv(f.Size, maxPart)
		if covered := int64(f.Parts) * maxPart; f.Size > covered {
			// True for every possible chunk size — not a hypothesis.
			f.ShortBy = f.Size - covered
			f.Verdict = "PROVEN_SHORT"
			res.Proven = append(res.Proven, f)
			continue
		}
		f.Verdict = "SUSPECT"
		res.Suspect = append(res.Suspect, f)
	}
	rows.Close()

	// Files whose parent chain never reaches a root: either the parent row is gone, or an
	// ancestor higher up is itself detached. Such a file still occupies space and still
	// answers by id, but it cannot be reached by browsing, so it is invisible in normal use.
	//
	// The reachable set is exactly the folders folderTree already resolved, so it is handed
	// back to Postgres as an array rather than being recomputed with a second recursive
	// walk. That is one anti-join over the file scan instead of re-deriving the tree.
	if res.PathsOK {
		// Only folders that actually reach a root count as reachable; a detached folder now
		// has a path too, but it is prefixed "?/" precisely because it is not browsable.
		reachable := make([]string, 0, len(paths))
		for id, p := range paths {
			if !strings.HasPrefix(p, "?") {
				reachable = append(reachable, id)
			}
		}
		rows, err = pool.Query(ctx, `
			SELECT f.id::text, f.name, COALESCE(f.parent_id::text, ''), COALESCE(f.size, 0),
			       COALESCE(f.channel_id, 0), COALESCE(f.updated_at, f.created_at)
			FROM teldrive.files f
			LEFT JOIN unnest($1::text[]) AS ok(id) ON ok.id = f.parent_id::text
			WHERE `+where+` AND f.parent_id IS NOT NULL AND ok.id IS NULL
			ORDER BY f.size DESC NULLS LAST LIMIT 500`, reachable)
		if err == nil {
			for rows.Next() {
				var f auditFile
				var ts time.Time
				if rows.Scan(&f.ID, &f.Name, &f.ParentID, &f.Size, &f.ChannelID, &ts) != nil {
					continue
				}
				f.Remote, f.Verdict = db.Remote, "ORPHANED"
				f.Path = fullPath(paths, f.ParentID, f.Name)
				f.UpdatedAt = ts.UTC().Format(time.RFC3339)
				res.Orphans = append(res.Orphans, f)
			}
			rows.Close()
		}
	}

	// Uploads that stopped part-way and haven't been swept yet — the same failure caught
	// before it becomes a permanent broken file.
	rows, err = pool.Query(ctx, `
		SELECT upload_id, COALESCE(max(name), ''), count(*), COALESCE(sum(size), 0),
		       min(created_at), max(created_at)
		FROM teldrive.uploads
		GROUP BY upload_id
		HAVING max(created_at) < now() - interval '1 hour'
		ORDER BY min(created_at) LIMIT 200`)
	if err == nil {
		for rows.Next() {
			var s auditStalled
			var st, lp time.Time
			if rows.Scan(&s.UploadID, &s.Name, &s.Parts, &s.Bytes, &st, &lp) != nil {
				continue
			}
			s.Remote = db.Remote
			s.StartedAt, s.LastPart = st.UTC().Format(time.RFC3339), lp.UTC().Format(time.RFC3339)
			res.Stalled = append(res.Stalled, s)
		}
		rows.Close()
	}
	return res, nil
}

// runTeldriveAudit scans every configured instance in parallel and merges the findings.
// An instance that can't be reached is reported in Instances rather than aborting the
// run — with several databases, one being down must not hide the others' results.
func runTeldriveAudit(ctx context.Context, cfg teldriveConfig, chunkGuess int64, activeOnly bool) (auditResult, error) {
	if chunkGuess <= 0 {
		chunkGuess = 512 << 20
	}
	dbs := cfg.teldriveDBs()
	out := auditResult{
		ChunkGuess: chunkGuess, ActiveOnly: activeOnly, Instances: []auditInstance{},
		Proven: []auditFile{}, Suspect: []auditFile{}, Broken: []auditFile{}, Orphans: []auditFile{},
		Stalled: []auditStalled{}, PartsTypes: map[string]int{},
		RanAt: time.Now().UTC().Format(time.RFC3339),
	}
	active := make([]teldriveDB, 0, len(dbs))
	for _, db := range dbs {
		if !db.Disabled && strings.TrimSpace(db.DSN) != "" {
			active = append(active, db)
		}
	}
	if len(active) == 0 {
		return out, errors.New("no teldrive database configured")
	}

	type outcome struct {
		db   teldriveDB
		res  auditResult
		err  error
		took time.Duration
	}
	results := make([]outcome, len(active))
	var wg sync.WaitGroup
	for i, db := range active {
		wg.Add(1)
		go func(i int, db teldriveDB) {
			defer wg.Done()
			t0 := time.Now()
			r, err := auditOneDB(ctx, db, chunkGuess, activeOnly)
			results[i] = outcome{db, r, err, time.Since(t0)}
		}(i, db)
	}
	wg.Wait()

	for _, o := range results {
		inst := auditInstance{Remote: o.db.Remote, MaxPartBytes: teldriveMaxPart(o.db), TookMS: o.took.Milliseconds()}
		if o.err != nil {
			inst.Error = o.err.Error()
			out.Instances = append(out.Instances, inst)
			continue
		}
		inst.Scanned = o.res.Scanned
		out.Instances = append(out.Instances, inst)
		out.Scanned += o.res.Scanned
		out.Proven = append(out.Proven, o.res.Proven...)
		out.Suspect = append(out.Suspect, o.res.Suspect...)
		out.Broken = append(out.Broken, o.res.Broken...)
		out.Orphans = append(out.Orphans, o.res.Orphans...)
		out.PathsOK = out.PathsOK || o.res.PathsOK
		out.Stalled = append(out.Stalled, o.res.Stalled...)
		for k, v := range o.res.PartsTypes {
			out.PartsTypes[k] += v
		}
	}
	// Biggest offenders first, regardless of which instance they came from.
	sort.Slice(out.Proven, func(i, j int) bool { return out.Proven[i].ShortBy > out.Proven[j].ShortBy })
	sort.Slice(out.Suspect, func(i, j int) bool { return out.Suspect[i].Size > out.Suspect[j].Size })
	return out, nil
}

func ceilDiv(a, b int64) int64 {
	if b <= 0 {
		return 0
	}
	return (a + b - 1) / b
}

// teldriveFileParts lists the stored part ids for one file, so a flagged file can be
// inspected without leaving the page.
func teldriveFileParts(ctx context.Context, dsn, fileID string) ([]int64, error) {
	pool, err := teldrivePool(ctx, dsn)
	if err != nil {
		return nil, err
	}
	defer pool.Close()
	rows, err := pool.Query(ctx, `
		SELECT (p.value->>'id')::bigint
		FROM teldrive.files f
		CROSS JOIN LATERAL jsonb_array_elements(f.parts) WITH ORDINALITY AS p(value, ord)
		WHERE f.id = $1::uuid AND jsonb_typeof(f.parts) = 'array'
		ORDER BY p.ord`, fileID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []int64{}
	for rows.Next() {
		var id int64
		if rows.Scan(&id) == nil {
			out = append(out, id)
		}
	}
	return out, rows.Err()
}

// ── HTTP ────────────────────────────────────────────────────────────────────────

func teldriveDBTest(w http.ResponseWriter, req *http.Request) {
	// Accepts the structured fields straight from the form so a connection can be tested
	// before it is saved; falls back to whatever is stored for that remote.
	var b struct {
		Remote   string         `json:"remote"`
		DSN      string         `json:"dsn"`
		Database string         `json:"database"`
		Server   teldriveServer `json:"server"`
	}
	_ = json.NewDecoder(req.Body).Decode(&b)
	dsn := strings.TrimSpace(b.DSN)
	if dsn == "" && b.Server.filled() {
		dsn = b.Server.dsn(b.Database)
	}
	if dsn == "" {
		dsn = dsnForRemote(b.Remote)
	}
	ctx, cancel := context.WithTimeout(req.Context(), 20*time.Second)
	defer cancel()
	writeJSON(w, http.StatusOK, teldriveDBCheck(ctx, dsn))
}

func teldriveAuditHandler(w http.ResponseWriter, req *http.Request) {
	cfg := loadOptions().Teldrive
	var chunk int64
	if v := req.URL.Query().Get("chunk"); v != "" {
		chunk, _ = strconv.ParseInt(v, 10, 64)
	}
	// Default on: a deleted file that is short is not a problem anyone needs to see.
	activeOnly := req.URL.Query().Get("all") != "1"
	auditMu.Lock()
	auditRun = true
	auditMu.Unlock()
	defer func() {
		auditMu.Lock()
		auditRun = false
		auditMu.Unlock()
	}()

	ctx, cancel := context.WithTimeout(req.Context(), auditQueryTimeout)
	defer cancel()
	res, err := runTeldriveAudit(ctx, cfg, chunk, activeOnly)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	// Persisted so a page reload shows these findings instead of an empty table, and so the
	// watcher has a baseline to notice new files against.
	saveAuditSnapshot(res, false, "scanned manually")
	writeJSON(w, http.StatusOK, res)
}

// teldriveAuditLastHandler returns the stored findings without scanning anything.
func teldriveAuditLastHandler(w http.ResponseWriter, _ *http.Request) {
	snap := loadAuditSnapshot()
	if snap == nil {
		writeJSON(w, http.StatusOK, map[string]any{"saved_at": ""})
		return
	}
	writeJSON(w, http.StatusOK, snap)
}

// dsnForRemote resolves a configured instance by its rclone remote name; with a single
// instance configured the name is optional.
func dsnForRemote(remote string) string {
	dbs := loadOptions().Teldrive.teldriveDBs()
	remote = strings.TrimSpace(remote)
	for _, db := range dbs {
		if db.Remote == remote {
			return db.DSN
		}
	}
	if remote == "" && len(dbs) == 1 {
		return dbs[0].DSN
	}
	return ""
}

func teldrivePartsHandler(w http.ResponseWriter, req *http.Request) {
	id := strings.TrimSpace(req.URL.Query().Get("id"))
	if id == "" {
		http.Error(w, "id required", http.StatusBadRequest)
		return
	}
	dsn := dsnForRemote(req.URL.Query().Get("remote"))
	if dsn == "" {
		http.Error(w, "unknown teldrive instance", http.StatusBadRequest)
		return
	}
	ctx, cancel := context.WithTimeout(req.Context(), 30*time.Second)
	defer cancel()
	ids, err := teldriveFileParts(ctx, dsn, id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"file_id": id, "message_ids": ids})
}

func teldriveGetConfig(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, loadOptions().Teldrive)
}

func teldrivePutConfig(w http.ResponseWriter, req *http.Request) {
	var c teldriveConfig
	if json.NewDecoder(req.Body).Decode(&c) != nil {
		http.Error(w, "bad config", http.StatusBadRequest)
		return
	}
	writeJSON(w, http.StatusOK, saveTeldriveConfig(c))
}
