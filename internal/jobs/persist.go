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
