package api

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"sb-ui/internal/jobs"
	"sb-ui/internal/store"
)

// A Task is a saved transfer config that can be run on demand, queued, or run on
// a cron schedule.
type Task struct {
	ID        string         `json:"id"`
	Name      string         `json:"name"`
	Op        string         `json:"op"`
	Items     []transferItem `json:"items"`
	Dst       string         `json:"dst"`
	DryRun    bool           `json:"dry_run"`
	Opts      transferOpts   `json:"opts"`
	Schedule  string         `json:"schedule"`           // 5-field cron, "" = none
	Disabled  bool           `json:"disabled,omitempty"` // pause the schedule without deleting
	RunMode   string         `json:"run_mode,omitempty"` // "" | queue (default) | now
	CreatedAt string         `json:"created_at"`
}

const tasksRel = "cache/transfer_tasks.json"

var (
	taskMu      sync.Mutex
	tasks       []Task
	tasksLoaded bool
)

func ensureTasks() {
	taskMu.Lock()
	defer taskMu.Unlock()
	if tasksLoaded {
		return
	}
	store.ReadJSON(tasksRel, &tasks)
	tasksLoaded = true
}

func saveTasks() { store.WriteJSON(tasksRel, tasks) } // call under taskMu

type taskResp struct {
	Task
	NextRun string `json:"next_run,omitempty"`
}

func listTasks(w http.ResponseWriter, _ *http.Request) {
	ensureTasks()
	taskMu.Lock()
	cp := append([]Task(nil), tasks...)
	taskMu.Unlock()
	out := make([]taskResp, 0, len(cp))
	live := make(map[string]bool, len(cp))
	for _, t := range cp {
		out = append(out, taskResp{Task: t, NextRun: taskNextRun(t.ID, t.Schedule, t.Disabled)})
		live[nrKey(t.ID, t.Schedule)] = true
	}
	// Entries for edited or deleted schedules are dead once nothing refers to them; drop
	// them here so the cache tracks the current task set instead of growing forever.
	pruneNextRunCache(live)
	writeJSON(w, http.StatusOK, out)
}

func toggleTask(w http.ResponseWriter, req *http.Request) {
	id := chi.URLParam(req, "id")
	ensureTasks()
	taskMu.Lock()
	defer taskMu.Unlock()
	for i := range tasks {
		if tasks[i].ID == id {
			tasks[i].Disabled = !tasks[i].Disabled
			saveTasks()
			// No cache work needed: a disabled task reports no next run at all, and the entry
			// is keyed by the schedule, so re-enabling either reuses a still-valid time or
			// recomputes because it has passed.
			writeJSON(w, http.StatusOK, tasks[i])
			return
		}
	}
	http.Error(w, "Not found", http.StatusNotFound)
}

func taskFromBody(req *http.Request) (Task, bool) {
	var t Task
	if json.NewDecoder(req.Body).Decode(&t) != nil {
		return t, false
	}
	return t, validTask(t)
}

// validTask checks a task is runnable, whether it was just created or just edited.
func validTask(t Task) bool {
	if t.Op != "copy" && t.Op != "move" && t.Op != "sync" {
		return false
	}
	if len(t.Items) == 0 || !validEndpoint(t.Dst) {
		return false
	}
	for _, it := range t.Items {
		if !validEndpoint(it.Path) {
			return false
		}
	}
	if t.Schedule != "" && !validCron(t.Schedule) {
		return false
	}
	return true
}

// applyTaskPatch decodes an edit onto the STORED task, so a field the request doesn't
// mention keeps the value it already had.
//
// Editing used to decode into an empty Task and replace the stored one wholesale, which made
// the browser's in-memory form the system of record: any field the client failed to send was
// silently reset to its zero value. That is not hypothetical — it turned a dry-run task into
// a real one when a hot reload reset the form's dry_run to false, and the next scheduled run
// copied files for real. Other fields had already been lost the same way and were rescued
// one at a time (the options handler still hand-copies Autoscan and Teldrive back, and the
// autoscan handler regenerates a webhook token that "went missing"). Patching instead of
// replacing removes the whole class rather than the next symptom.
//
// ID and CreatedAt are never taken from the request. `opts` is replaced as a unit when the
// request mentions it at all, rather than merged field by field — a half-sent options object
// should not silently inherit the other half of the old one.
func applyTaskPatch(stored Task, body []byte) (Task, bool) {
	sent := sentKeys(body)
	if sent == nil {
		return stored, false
	}
	base := stored
	if sent["opts"] {
		base.Opts = transferOpts{}
	}
	t, err := patchOnto(base, body)
	if err != nil {
		return stored, false
	}
	t.ID, t.CreatedAt = stored.ID, stored.CreatedAt
	if !validTask(t) {
		// Hand back the untouched record, never the half-applied one: a caller that only
		// looked at the value would otherwise be holding a task with the bad edit in it.
		return stored, false
	}
	return t, true
}

func createTask(w http.ResponseWriter, req *http.Request) {
	t, ok := taskFromBody(req)
	if !ok {
		http.Error(w, "Invalid task", http.StatusBadRequest)
		return
	}
	ensureTasks()
	t.ID = uuid.NewString()
	t.CreatedAt = time.Now().UTC().Format(time.RFC3339)
	if strings.TrimSpace(t.Name) == "" {
		t.Name = transferLabel(t.Op, t.Items, t.Dst, false) // the flag can change; the stored name must not
	}
	taskMu.Lock()
	tasks = append(tasks, t)
	saveTasks()
	taskMu.Unlock()
	writeJSON(w, http.StatusOK, t)
}

func updateTask(w http.ResponseWriter, req *http.Request) {
	id := chi.URLParam(req, "id")
	body, err := io.ReadAll(io.LimitReader(req.Body, 1<<20))
	if err != nil {
		http.Error(w, "Invalid task", http.StatusBadRequest)
		return
	}
	ensureTasks()
	taskMu.Lock()
	defer taskMu.Unlock()
	for i := range tasks {
		if tasks[i].ID == id {
			// Patch the stored task rather than replacing it: an omitted field keeps its value.
			t, ok := applyTaskPatch(tasks[i], body)
			if !ok {
				http.Error(w, "Invalid task", http.StatusBadRequest)
				return
			}
			if strings.TrimSpace(t.Name) == "" {
				t.Name = transferLabel(t.Op, t.Items, t.Dst, false) // the flag can change; the stored name must not
			}
			tasks[i] = t
			saveTasks()
			writeJSON(w, http.StatusOK, t)
			return
		}
	}
	http.Error(w, "Not found", http.StatusNotFound)
}

func deleteTask(w http.ResponseWriter, req *http.Request) {
	id := chi.URLParam(req, "id")
	ensureTasks()
	taskMu.Lock()
	defer taskMu.Unlock()
	for i := range tasks {
		if tasks[i].ID == id {
			tasks = append(tasks[:i], tasks[i+1:]...)
			saveTasks()
			break
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func findTask(id string) (Task, bool) {
	ensureTasks()
	taskMu.Lock()
	defer taskMu.Unlock()
	for _, t := range tasks {
		if t.ID == id {
			return t, true
		}
	}
	return Task{}, false
}

// findTaskByName looks a task up by display name (used to recover uploader destinations
// whose remote name was mistakenly stored as the old task label).
func findTaskByName(name string) (Task, bool) {
	ensureTasks()
	taskMu.Lock()
	defer taskMu.Unlock()
	for _, t := range tasks {
		if t.Name == name {
			return t, true
		}
	}
	return Task{}, false
}

// runTaskNow runs a task immediately (concurrent).
func runTaskNow(w http.ResponseWriter, req *http.Request) {
	t, ok := findTask(chi.URLParam(req, "id"))
	if !ok {
		http.Error(w, "Not found", http.StatusNotFound)
		return
	}
	j := jobs.Create(transferLabel(t.Op, t.Items, t.Dst, t.DryRun), t.Op)
	go runTransfer(j.ID, t.ID, t.Op, t.Items, t.Dst, t.DryRun, t.Opts)
	writeJSON(w, http.StatusOK, map[string]any{"job_id": j.ID})
}

// queueTaskNow appends a task run to the sequential queue.
func queueTaskNow(w http.ResponseWriter, req *http.Request) {
	t, ok := findTask(chi.URLParam(req, "id"))
	if !ok {
		http.Error(w, "Not found", http.StatusNotFound)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"job_id": enqueueTask(t)})
}

// ── managed queue: one transfer at a time (gentler on remotes / rate limits) ───
// Unlike a fire-and-forget channel, this is an ordered, inspectable list with
// start/stop (pause), reorder, and remove — like RcloneBrowser's queue.

type queueItem struct {
	JobID string `json:"job_id"`
	Label string `json:"label"`
	task  Task
}

var (
	qMu               sync.Mutex
	queueList         []queueItem
	queueCurrent      string
	queueCurrentLabel string
	queueRunning      = true
	queueKick         = make(chan struct{}, 1)
	queueOnce         sync.Once
)

func kickQueue() {
	select {
	case queueKick <- struct{}{}:
	default:
	}
}

func startQueueWorker() {
	queueOnce.Do(func() {
		go func() {
			for range queueKick {
				for {
					qMu.Lock()
					if !queueRunning || queueCurrent != "" || len(queueList) == 0 {
						qMu.Unlock()
						break
					}
					it := queueList[0]
					queueList = queueList[1:]
					queueCurrent, queueCurrentLabel = it.JobID, it.Label
					qMu.Unlock()

					runTransfer(it.JobID, it.task.ID, it.task.Op, it.task.Items, it.task.Dst, it.task.DryRun, it.task.Opts)

					qMu.Lock()
					queueCurrent, queueCurrentLabel = "", ""
					qMu.Unlock()
				}
			}
		}()
	})
}

// enqueueTask creates a pending job and appends it to the queue; the worker runs
// it when the queue is running and idle.
func enqueueTask(t Task) string {
	startQueueWorker()
	j := jobs.Create(transferLabel(t.Op, t.Items, t.Dst, t.DryRun), t.Op)
	qMu.Lock()
	queueList = append(queueList, queueItem{JobID: j.ID, Label: j.Tag, task: t})
	qMu.Unlock()
	kickQueue()
	return j.ID
}

func queueState(w http.ResponseWriter, _ *http.Request) {
	qMu.Lock()
	items := make([]map[string]string, 0, len(queueList))
	for _, it := range queueList {
		items = append(items, map[string]string{"job_id": it.JobID, "label": it.Label})
	}
	var current map[string]string
	if queueCurrent != "" {
		current = map[string]string{"job_id": queueCurrent, "label": queueCurrentLabel}
	}
	resp := map[string]any{"running": queueRunning, "current": current, "items": items}
	qMu.Unlock()
	writeJSON(w, http.StatusOK, resp)
}

func queueStart(w http.ResponseWriter, _ *http.Request) {
	qMu.Lock()
	queueRunning = true
	qMu.Unlock()
	kickQueue()
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func queueStop(w http.ResponseWriter, _ *http.Request) {
	qMu.Lock()
	queueRunning = false
	qMu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func queuePurge(w http.ResponseWriter, _ *http.Request) {
	qMu.Lock()
	for _, it := range queueList {
		jobs.SetStatus(it.JobID, "stopped")
	}
	queueList = nil
	qMu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func queueRemove(w http.ResponseWriter, req *http.Request) {
	id := chi.URLParam(req, "id")
	qMu.Lock()
	for i := range queueList {
		if queueList[i].JobID == id {
			queueList = append(queueList[:i], queueList[i+1:]...)
			jobs.SetStatus(id, "stopped")
			break
		}
	}
	qMu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func queueMove(dir int) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		id := chi.URLParam(req, "id")
		qMu.Lock()
		for i := range queueList {
			if queueList[i].JobID == id {
				j := i + dir
				if j >= 0 && j < len(queueList) {
					queueList[i], queueList[j] = queueList[j], queueList[i]
				}
				break
			}
		}
		qMu.Unlock()
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	}
}

// ── scheduler: minute ticker matching each task's cron ────────────────────────

var schedOnce sync.Once

func startScheduler() {
	schedOnce.Do(func() {
		go func() {
			lastFired := map[string]string{}
			for {
				now := time.Now()
				minute := now.Format("2006-01-02T15:04")
				ensureTasks()
				taskMu.Lock()
				due := []Task(nil)
				for _, t := range tasks {
					if t.Schedule != "" && !t.Disabled && lastFired[t.ID] != minute && cronMatch(t.Schedule, now) {
						lastFired[t.ID] = minute
						due = append(due, t)
					}
				}
				taskMu.Unlock()
				for _, t := range due {
					if t.RunMode == "now" {
						j := jobs.Create(transferLabel(t.Op, t.Items, t.Dst, t.DryRun), t.Op)
						go runTransfer(j.ID, t.ID, t.Op, t.Items, t.Dst, t.DryRun, t.Opts)
					} else {
						enqueueTask(t)
					}
				}
				time.Sleep(time.Until(now.Truncate(time.Minute).Add(time.Minute)) + time.Second)
			}
		}()
	})
}

// ── next-run computation (cached; recomputed when it passes) ───────────────────

var (
	nrMu         sync.Mutex
	nextRunCache = map[string]time.Time{}
)

// nrKey identifies a cached next-run by the task AND the schedule it was computed from.
//
// Keying on the id alone meant an edited schedule kept reporting the next run of the OLD
// one: the entry is only recomputed once its time has passed, so changing 20:15 to 20:21
// left the future 20:15 in place and the UI showed it until that moment arrived. Invalidating
// on update was the alternative, but only the enable/disable toggle ever remembered to —
// putting the schedule in the key means nobody has to.
func nrKey(id, cron string) string { return id + "\x00" + cron }

func taskNextRun(id, cron string, disabled bool) string {
	if cron == "" || disabled {
		return ""
	}
	now := time.Now()
	key := nrKey(id, cron)
	nrMu.Lock()
	nr := nextRunCache[key]
	nrMu.Unlock()
	if nr.IsZero() || !nr.After(now) {
		// Walks forward a minute at a time for up to a year, which is why the result is cached.
		nr = computeNextRun(cron, now)
		nrMu.Lock()
		nextRunCache[key] = nr
		nrMu.Unlock()
	}
	if nr.IsZero() {
		return ""
	}
	return nr.UTC().Format(time.RFC3339)
}

// pruneNextRunCache keeps only the entries still referred to by a live task+schedule.
func pruneNextRunCache(keep map[string]bool) {
	nrMu.Lock()
	defer nrMu.Unlock()
	for k := range nextRunCache {
		if !keep[k] {
			delete(nextRunCache, k)
		}
	}
}

func computeNextRun(cron string, from time.Time) time.Time {
	t := from.Truncate(time.Minute).Add(time.Minute)
	for i := 0; i < 366*1440; i++ {
		if cronMatch(cron, t) {
			return t
		}
		t = t.Add(time.Minute)
	}
	return time.Time{}
}

// ── minimal 5-field cron matcher (min hour dom month dow) ─────────────────────

func validCron(spec string) bool { return len(strings.Fields(spec)) == 5 }

func cronMatch(spec string, t time.Time) bool {
	f := strings.Fields(spec)
	if len(f) != 5 {
		return false
	}
	return cronField(f[0], t.Minute(), 0, 59) &&
		cronField(f[1], t.Hour(), 0, 23) &&
		cronField(f[2], t.Day(), 1, 31) &&
		cronField(f[3], int(t.Month()), 1, 12) &&
		cronField(f[4], int(t.Weekday()), 0, 6)
}

// cronField matches a single field supporting *, a, a-b, lists (,) and steps (/n).
func cronField(spec string, val, lo, hi int) bool {
	for _, part := range strings.Split(spec, ",") {
		step := 1
		rng := part
		if i := strings.Index(part, "/"); i >= 0 {
			if s, err := strconv.Atoi(part[i+1:]); err == nil && s > 0 {
				step = s
			}
			rng = part[:i]
		}
		a, b := lo, hi
		if rng != "*" {
			if i := strings.Index(rng, "-"); i >= 0 {
				a, _ = strconv.Atoi(rng[:i])
				b, _ = strconv.Atoi(rng[i+1:])
			} else {
				n, err := strconv.Atoi(rng)
				if err != nil {
					continue
				}
				a, b = n, n
			}
		}
		for v := a; v <= b; v += step {
			if v == val {
				return true
			}
		}
	}
	return false
}
