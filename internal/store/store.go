// Package store persists UI state on the Saltbox host (via the executor, like
// patches) under /opt/saltbox-ui — survives restarts.
package store

import (
	"context"
	"encoding/json"
	"path"
	"time"

	"sb-ui/internal/executor"
)

const Base = "/opt/saltbox-ui"

func ctx() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 30*time.Second)
}

// ReadJSON unmarshals Base/rel into v; leaves v untouched if missing/unreadable.
func ReadJSON(rel string, v any) {
	c, cancel := ctx()
	defer cancel()
	e := executor.Get()
	full := Base + "/" + rel
	if ok, _ := e.FileExists(c, full); !ok {
		return
	}
	data, err := e.ReadFile(c, full)
	if err != nil {
		return
	}
	_ = json.Unmarshal([]byte(data), v)
}

func WriteJSON(rel string, v any) {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return
	}
	WriteText(rel, string(b))
}

func ReadText(rel string) (string, bool) {
	c, cancel := ctx()
	defer cancel()
	e := executor.Get()
	full := Base + "/" + rel
	if ok, _ := e.FileExists(c, full); !ok {
		return "", false
	}
	s, err := e.ReadFile(c, full)
	if err != nil {
		return "", false
	}
	return s, true
}

// Remove deletes Base/rel (best-effort).
func Remove(rel string) {
	c, cancel := ctx()
	defer cancel()
	_, _, _ = executor.Get().Run(c, []string{"rm", "-f", Base + "/" + rel}, "")
}

func WriteText(rel, text string) { WriteTextAbs(Base+"/"+rel, text) }

// WriteTextAbs writes text to an absolute path (creating parent dirs).
func WriteTextAbs(full, text string) {
	c, cancel := ctx()
	defer cancel()
	e := executor.Get()
	_ = e.MakeDirs(c, path.Dir(full))
	_ = e.WriteFile(c, full, text)
}
