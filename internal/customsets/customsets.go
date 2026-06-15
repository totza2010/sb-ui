// Package customsets stores user-defined install sets (name + tags) under
// /opt/saltbox-ui/cache. Port of custom_sets.py.
package customsets

import (
	"errors"
	"strings"
	"sync"

	"github.com/google/uuid"

	"sb-ui/internal/store"
)

const rel = "cache/custom_sets.json"

type Set struct {
	ID   string   `json:"id"`
	Name string   `json:"name"`
	Tags []string `json:"tags"`
}

var starter = []Set{
	{ID: "starter-arr", Name: "Arr apps", Tags: []string{"sonarr", "radarr", "bazarr", "prowlarr"}},
	{ID: "starter-downloaders", Name: "Downloaders", Tags: []string{"sabnzbd", "qbittorrent"}},
	{ID: "starter-monitoring", Name: "Monitoring", Tags: []string{"grafana", "netdata", "dozzle"}},
}

var (
	mu     sync.Mutex
	sets   []Set
	loaded bool
)

func Load() {
	mu.Lock()
	defer mu.Unlock()
	var data []Set
	var present bool
	if _, ok := store.ReadText(rel); ok {
		present = true
	}
	store.ReadJSON(rel, &data)
	if !present {
		sets = append([]Set(nil), starter...)
		store.WriteJSON(rel, sets)
	} else {
		sets = data
	}
	loaded = true
}

func ensure() {
	mu.Lock()
	l := loaded
	mu.Unlock()
	if !l {
		Load()
	}
}

func GetAll() []Set {
	ensure()
	mu.Lock()
	defer mu.Unlock()
	return append([]Set(nil), sets...)
}

func Upsert(name string, tags []string, id string) (Set, error) {
	name = strings.TrimSpace(name)
	clean := []string{}
	for _, t := range tags {
		if t = strings.TrimSpace(t); t != "" {
			clean = append(clean, t)
		}
	}
	if name == "" {
		return Set{}, errors.New("Name is required")
	}
	if len(clean) == 0 {
		return Set{}, errors.New("At least one tag is required")
	}
	ensure()
	mu.Lock()
	defer mu.Unlock()
	rec := Set{ID: id, Name: name, Tags: clean}
	if rec.ID == "" {
		rec.ID = uuid.NewString()[:12]
	}
	found := false
	for i, s := range sets {
		if s.ID == rec.ID {
			sets[i] = rec
			found = true
			break
		}
	}
	if !found {
		sets = append(sets, rec)
	}
	store.WriteJSON(rel, sets)
	return rec, nil
}

func Delete(id string) {
	ensure()
	mu.Lock()
	defer mu.Unlock()
	out := sets[:0]
	for _, s := range sets {
		if s.ID != id {
			out = append(out, s)
		}
	}
	sets = out
	store.WriteJSON(rel, sets)
}
