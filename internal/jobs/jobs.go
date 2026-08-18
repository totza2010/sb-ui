// Package jobs tracks background jobs (install/update/…), streams their logs to
// WebSocket subscribers, and persists log + history to /opt/saltbox-ui so the
// History view survives restarts.
package jobs

import (
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

const maxHistory = 200

// Msg is what a subscriber receives over the WebSocket.
type Msg struct {
	Type   string `json:"type"`             // "log" | "status"
	Line   string `json:"line,omitempty"`   // for type=log
	Status string `json:"status,omitempty"` // for type=status
}

type Job struct {
	ID        string    `json:"id"`
	Tag       string    `json:"tag"`
	Action    string    `json:"action"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"created_at"`

	mu     sync.Mutex
	lines  []string
	subs   map[chan Msg]struct{}
	loaded bool // log loaded into memory (for history)
}

// TimeFormat keeps sub-second precision at a FIXED width, so timestamps compare correctly as
// strings. RFC3339 has only second precision, which made every job created in the same second
// indistinguishable; RFC3339Nano would fix that but trims trailing zeros, so ".5Z" and
// ".50001Z" no longer sort against each other correctly.
const TimeFormat = "2006-01-02T15:04:05.000000000Z07:00"

func (j *Job) toDict() map[string]any {
	j.mu.Lock()
	defer j.mu.Unlock()
	return map[string]any{
		"id": j.ID, "tag": j.Tag, "action": j.Action, "status": j.Status,
		"created_at": j.CreatedAt.UTC().Format(TimeFormat), "log_lines": len(j.lines),
	}
}

var (
	mu   sync.Mutex
	jobs = map[string]*Job{}
)

func Create(tag, action string) *Job {
	j := &Job{
		ID: uuid.NewString(), Tag: tag, Action: action, Status: "pending",
		CreatedAt: time.Now(), subs: map[chan Msg]struct{}{}, loaded: true,
	}
	mu.Lock()
	jobs[j.ID] = j
	mu.Unlock()
	return j
}

func get(id string) *Job {
	mu.Lock()
	defer mu.Unlock()
	return jobs[id]
}

// Status returns a job's final/current status ("completed" | "failed" | "stopped" | …),
// or "" if the job is unknown. Used by the uploader to tell a clean finish from an
// interruption so it can resume the same remote.
func Status(id string) string {
	j := get(id)
	if j == nil {
		return ""
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.Status
}

// ListDicts returns all jobs (newest first) as JSON-ready maps.
func ListDicts() []map[string]any {
	mu.Lock()
	all := make([]*Job, 0, len(jobs))
	for _, j := range jobs {
		all = append(all, j)
	}
	mu.Unlock()
	// Newest first, with the id breaking ties. Both halves matter: map iteration order is
	// randomised by Go, and sort.Slice is not stable, so jobs created in the same instant came
	// back in a different arrangement on every poll and the list visibly shuffled itself.
	sort.SliceStable(all, func(i, k int) bool {
		if all[i].CreatedAt.Equal(all[k].CreatedAt) {
			return all[i].ID < all[k].ID
		}
		return all[i].CreatedAt.After(all[k].CreatedAt)
	})
	out := make([]map[string]any, len(all))
	for i, j := range all {
		out[i] = j.toDict()
	}
	return out
}

// JobDict returns one job + its log lines (loading from disk if needed).
func JobDict(id string) (map[string]any, bool) {
	j := get(id)
	if j == nil {
		return nil, false
	}
	EnsureLogLoaded(id)
	d := j.toDict()
	j.mu.Lock()
	d["lines"] = append([]string(nil), j.lines...)
	j.mu.Unlock()
	return d, true
}

func PushLog(id, line string) {
	line = strings.TrimRight(line, "\n")
	if line == "" {
		return
	}
	j := get(id)
	if j == nil {
		return
	}
	j.mu.Lock()
	j.lines = append(j.lines, line)
	for ch := range j.subs {
		select {
		case ch <- Msg{Type: "log", Line: line}:
		default:
		}
	}
	j.mu.Unlock()
}

func SetStatus(id, status string) {
	j := get(id)
	if j == nil {
		return
	}
	j.mu.Lock()
	j.Status = status
	for ch := range j.subs {
		select {
		case ch <- Msg{Type: "status", Status: status}:
		default:
		}
	}
	terminal := status == "completed" || status == "failed" || status == "stopped"
	if terminal {
		for ch := range j.subs {
			close(ch)
			delete(j.subs, ch)
		}
	}
	j.mu.Unlock()
	if terminal {
		go persist(id) // log + history → /opt/saltbox-ui
	} else if status == "running" {
		go persistMeta(id) // record it now so a restart mid-run doesn't lose the job
	}
}

// Subscribe registers a subscriber: returns a snapshot of existing lines, a
// channel of new messages (closed when the job finishes), and an unsubscribe fn.
func Subscribe(id string) (snapshot []string, ch chan Msg, cancel func(), ok bool) {
	EnsureLogLoaded(id)
	j := get(id)
	if j == nil {
		return nil, nil, nil, false
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	snapshot = append([]string(nil), j.lines...)
	ch = make(chan Msg, 256)
	if j.Status == "completed" || j.Status == "failed" || j.Status == "stopped" {
		ch <- Msg{Type: "status", Status: j.Status}
		close(ch)
		return snapshot, ch, func() {}, true
	}
	j.subs[ch] = struct{}{}
	cancel = func() {
		j.mu.Lock()
		if _, live := j.subs[ch]; live {
			delete(j.subs, ch)
		}
		j.mu.Unlock()
	}
	return snapshot, ch, cancel, true
}
