package api

import (
	"context"
	"encoding/json"
	"net/http"
	"path"
	"strconv"
	"strings"
	"time"

	"sb-ui/internal/executor"
)

var fsRoots = []string{"/mnt", "/opt", "/srv", "/home"}
var fsWriteRoots = []string{"/opt", "/srv", "/home"}

func underRoot(p string, roots []string) bool {
	for _, r := range roots {
		if p == r || strings.HasPrefix(p, r+"/") {
			return true
		}
	}
	return false
}

type fsEntry struct {
	Type string `json:"type"`
	Size int64  `json:"size"`
	Name string `json:"name"`
}

func fsList(w http.ResponseWriter, req *http.Request) {
	resolved := path.Clean(req.URL.Query().Get("path"))
	if !underRoot(resolved, fsRoots) {
		http.Error(w, "Path not allowed", http.StatusBadRequest)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	rc, out, _ := executor.Get().Run(ctx, []string{
		"find", resolved, "-maxdepth", "1", "-mindepth", "1", "-printf", `%y\t%s\t%f\n`,
	}, "")
	entries := []fsEntry{}
	if rc == 0 {
		for _, line := range strings.Split(out, "\n") {
			parts := strings.SplitN(line, "\t", 3)
			if len(parts) != 3 {
				continue
			}
			typ := "file"
			if parts[0] == "d" {
				typ = "dir"
			}
			sz, _ := strconv.ParseInt(parts[1], 10, 64)
			entries = append(entries, fsEntry{Type: typ, Size: sz, Name: parts[2]})
		}
	}
	// dirs first, then by name
	sortEntries(entries)
	writeJSON(w, http.StatusOK, map[string]any{"path": resolved, "entries": entries, "exists": rc == 0})
}

func fsReadFile(w http.ResponseWriter, req *http.Request) {
	resolved := path.Clean(req.URL.Query().Get("path"))
	if !underRoot(resolved, fsRoots) {
		http.Error(w, "Path not allowed", http.StatusBadRequest)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	e := executor.Get()
	if rc, sz, _ := e.Run(ctx, []string{"stat", "-c", "%s", resolved}, ""); rc == 0 {
		if n, err := strconv.ParseInt(strings.TrimSpace(sz), 10, 64); err == nil && n > 2_000_000 {
			http.Error(w, "File too large to edit (>2 MB)", http.StatusRequestEntityTooLarge)
			return
		}
	}
	content, err := e.ReadFile(ctx, resolved)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"path": resolved, "content": content, "writable": underRoot(resolved, fsWriteRoots),
	})
}

func fsWriteFile(w http.ResponseWriter, req *http.Request) {
	resolved := path.Clean(req.URL.Query().Get("path"))
	if !underRoot(resolved, fsWriteRoots) {
		http.Error(w, "Path not writable", http.StatusBadRequest)
		return
	}
	var body struct {
		Content string `json:"content"`
	}
	_ = json.NewDecoder(req.Body).Decode(&body)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := executor.Get().WriteFile(ctx, resolved, body.Content); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "path": resolved})
}

func sortEntries(e []fsEntry) {
	for i := 1; i < len(e); i++ {
		for k := i; k > 0; k-- {
			a, b := e[k-1], e[k]
			aDir, bDir := a.Type == "dir", b.Type == "dir"
			swap := false
			if aDir != bDir {
				swap = !aDir // dirs first
			} else {
				swap = strings.ToLower(a.Name) > strings.ToLower(b.Name)
			}
			if swap {
				e[k-1], e[k] = e[k], e[k-1]
			} else {
				break
			}
		}
	}
}
