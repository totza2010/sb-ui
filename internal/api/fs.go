package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"path"
	"strconv"
	"strings"
	"time"

	"sb-ui/internal/executor"
)

var fsRoots = []string{"/mnt", "/opt", "/srv", "/home"}

// Writes (mkdir/rename/delete/move/copy) are allowed under /mnt too so the file
// manager can clean up media storage — but never on a root or mount point itself
// (see fsProtected).
var fsWriteRoots = []string{"/mnt", "/opt", "/srv", "/home"}

// fsProtected guards the roots + Saltbox mount points from destructive ops.
func fsProtected(p string) bool {
	for _, r := range []string{"/mnt", "/mnt/unionfs", "/mnt/local", "/mnt/remote", "/opt", "/srv", "/home"} {
		if p == r {
			return true
		}
	}
	return false
}

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

// fsCmd runs a filesystem command and returns ok/error as JSON.
func fsCmd(w http.ResponseWriter, args []string) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	rc, out, err := executor.Get().Run(ctx, args, "")
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

// validTarget cleans + checks a path is writable and not a protected root/mount.
func validTarget(raw string) (string, bool) {
	p := path.Clean(raw)
	return p, underRoot(p, fsWriteRoots) && !fsProtected(p)
}

func fsMkdir(w http.ResponseWriter, req *http.Request) {
	var b struct {
		Path string `json:"path"`
	}
	_ = json.NewDecoder(req.Body).Decode(&b)
	p := path.Clean(b.Path)
	if !underRoot(p, fsWriteRoots) {
		http.Error(w, "Path not writable", http.StatusBadRequest)
		return
	}
	fsCmd(w, []string{"mkdir", "-p", "--", p})
}

func fsRename(w http.ResponseWriter, req *http.Request) {
	var b struct {
		Path string `json:"path"`
		Name string `json:"name"`
	}
	_ = json.NewDecoder(req.Body).Decode(&b)
	src, ok := validTarget(b.Path)
	if !ok {
		http.Error(w, "Path not writable", http.StatusBadRequest)
		return
	}
	if b.Name == "" || strings.ContainsAny(b.Name, `/\`) {
		http.Error(w, "Invalid name", http.StatusBadRequest)
		return
	}
	fsCmd(w, []string{"mv", "--", src, path.Dir(src) + "/" + b.Name})
}

func fsDelete(w http.ResponseWriter, req *http.Request) {
	var b struct {
		Paths []string `json:"paths"`
	}
	_ = json.NewDecoder(req.Body).Decode(&b)
	if len(b.Paths) == 0 {
		http.Error(w, "No paths", http.StatusBadRequest)
		return
	}
	args := []string{"rm", "-rf", "--"}
	for _, raw := range b.Paths {
		p, ok := validTarget(raw)
		if !ok {
			http.Error(w, "Refused: "+raw, http.StatusBadRequest)
			return
		}
		args = append(args, p)
	}
	fsCmd(w, args)
}

// fsTransfer handles move (paste-cut) and copy (paste-copy) into a destination dir.
func fsTransfer(move bool) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		var b struct {
			Paths []string `json:"paths"`
			Dest  string   `json:"dest"`
		}
		_ = json.NewDecoder(req.Body).Decode(&b)
		dest := path.Clean(b.Dest)
		if !underRoot(dest, fsWriteRoots) {
			http.Error(w, "Destination not writable", http.StatusBadRequest)
			return
		}
		if len(b.Paths) == 0 {
			http.Error(w, "No paths", http.StatusBadRequest)
			return
		}
		args := []string{"cp", "-a"}
		if move {
			args = []string{"mv"}
		}
		args = append(args, "--")
		for _, raw := range b.Paths {
			p, ok := validTarget(raw)
			if !ok {
				http.Error(w, "Refused: "+raw, http.StatusBadRequest)
				return
			}
			args = append(args, p)
		}
		fsCmd(w, append(args, dest))
	}
}

// fsUpload streams a multipart upload to disk. cubone sends a `path` field (the
// destination dir, via onFileUploading) followed by the `file` part.
func fsUpload(w http.ResponseWriter, req *http.Request) {
	mr, err := req.MultipartReader()
	if err != nil {
		http.Error(w, "expected a multipart upload", http.StatusBadRequest)
		return
	}
	destDir := ""
	for {
		part, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		switch part.FormName() {
		case "path":
			b, _ := io.ReadAll(io.LimitReader(part, 8192))
			destDir = path.Clean(string(b))
		case "file":
			if !underRoot(destDir, fsWriteRoots) {
				http.Error(w, "Destination not writable", http.StatusBadRequest)
				return
			}
			name := path.Base(strings.ReplaceAll(part.FileName(), `\`, "/"))
			if name == "" || name == "." || name == "/" {
				http.Error(w, "Invalid filename", http.StatusBadRequest)
				return
			}
			if err := executor.Get().Upload(req.Context(), destDir+"/"+name, part); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
		}
		_ = part.Close()
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// fsDownload streams a file to the client as an attachment.
func fsDownload(w http.ResponseWriter, req *http.Request) {
	p := path.Clean(req.URL.Query().Get("path"))
	if !underRoot(p, fsRoots) {
		http.Error(w, "Path not allowed", http.StatusBadRequest)
		return
	}
	rc, err := executor.Get().Download(req.Context(), p)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	defer rc.Close()
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", "attachment; filename="+strconv.Quote(path.Base(p)))
	_, _ = io.Copy(w, rc)
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
