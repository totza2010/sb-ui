package jobs

import (
	"strings"
	"time"

	"sb-ui/internal/store"
)

type histEntry struct {
	ID        string `json:"id"`
	Tag       string `json:"tag"`
	Action    string `json:"action"`
	Status    string `json:"status"`
	CreatedAt string `json:"created_at"`
}

func persist(id string) {
	j := get(id)
	if j == nil {
		return
	}
	j.mu.Lock()
	logText := strings.Join(j.lines, "\n")
	entry := histEntry{j.ID, j.Tag, j.Action, j.Status, j.CreatedAt.UTC().Format(time.RFC3339)}
	j.mu.Unlock()

	store.WriteText("logs/"+id+".log", logText)

	var idx []histEntry
	store.ReadJSON("logs/index.json", &idx)
	out := []histEntry{entry}
	for _, e := range idx {
		if e.ID != id {
			out = append(out, e)
		}
	}
	if len(out) > maxHistory {
		out = out[:maxHistory]
	}
	store.WriteJSON("logs/index.json", out)
}

func removeFromIndex(rm map[string]bool) {
	var idx []histEntry
	store.ReadJSON("logs/index.json", &idx)
	out := make([]histEntry, 0, len(idx))
	for _, e := range idx {
		if !rm[e.ID] {
			out = append(out, e)
		}
	}
	store.WriteJSON("logs/index.json", out)
}

// Delete removes a finished job from memory + persisted history (log + index).
// Running/pending jobs are left alone. Returns false if not deletable.
func Delete(id string) bool {
	mu.Lock()
	j := jobs[id]
	if j == nil || j.Status == "running" || j.Status == "pending" {
		mu.Unlock()
		return false
	}
	delete(jobs, id)
	mu.Unlock()
	removeFromIndex(map[string]bool{id: true})
	store.Remove("logs/" + id + ".log")
	return true
}

// ClearFinished removes all terminal jobs; returns the deleted IDs.
func ClearFinished() []string {
	mu.Lock()
	rm := map[string]bool{}
	for id, j := range jobs {
		if j.Status == "completed" || j.Status == "failed" || j.Status == "stopped" {
			rm[id] = true
			delete(jobs, id)
		}
	}
	mu.Unlock()
	if len(rm) == 0 {
		return nil
	}
	removeFromIndex(rm)
	ids := make([]string, 0, len(rm))
	for id := range rm {
		store.Remove("logs/" + id + ".log")
		ids = append(ids, id)
	}
	return ids
}

// LoadHistory restores past jobs from the persisted index on startup.
func LoadHistory() {
	var idx []histEntry
	store.ReadJSON("logs/index.json", &idx)
	mu.Lock()
	defer mu.Unlock()
	for _, e := range idx {
		if e.ID == "" || jobs[e.ID] != nil {
			continue
		}
		created, err := time.Parse(time.RFC3339, e.CreatedAt)
		if err != nil {
			created = time.Now()
		}
		jobs[e.ID] = &Job{
			ID: e.ID, Tag: e.Tag, Action: e.Action, Status: e.Status,
			CreatedAt: created, subs: map[chan Msg]struct{}{}, loaded: false,
		}
	}
}

// EnsureLogLoaded lazy-loads a finished job's log from disk into memory.
func EnsureLogLoaded(id string) {
	j := get(id)
	if j == nil {
		return
	}
	j.mu.Lock()
	if j.loaded || len(j.lines) > 0 {
		j.loaded = true
		j.mu.Unlock()
		return
	}
	terminal := j.Status == "completed" || j.Status == "failed"
	j.mu.Unlock()
	if !terminal {
		return
	}
	if text, ok := store.ReadText("logs/" + id + ".log"); ok && text != "" {
		j.mu.Lock()
		j.lines = strings.Split(text, "\n")
		j.loaded = true
		j.mu.Unlock()
	}
}
