package api

import (
	"context"
	"encoding/json"
	"net/http"
	"path"
	"strings"
	"time"

	"sb-ui/internal/executor"
	"sb-ui/internal/rclone"
)

// rclone-side file operations for the Files page (the disk side lives in fs.go).
// These are single-shot ops run via the executor — browse+mkdir already exist in
// transfers.go; here we add delete/purge, rename/move, copy, size(du), about(quota)
// and fsinfo (per-backend capabilities, so the UI can hide unsupported actions).

// relClean normalises a path within a remote ("" = root, no leading slash, no "..").
func relClean(p string) (string, bool) {
	c := strings.TrimPrefix(path.Clean("/"+p), "/")
	if c == ".." || strings.HasPrefix(c, "../") {
		return "", false
	}
	return c, true
}

func rcloneRun(args ...string) (int, string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	full := append([]string{"rclone", "--config", rcloneConfPath()}, args...)
	rc, out, _ := executor.Get().Run(ctx, full, "")
	return rc, out
}

// rcloneDelete removes a file (deletefile) or a directory tree (purge).
func rcloneDelete(w http.ResponseWriter, req *http.Request) {
	var b struct {
		Remote string `json:"remote"`
		Path   string `json:"path"`
		IsDir  bool   `json:"is_dir"`
	}
	_ = json.NewDecoder(req.Body).Decode(&b)
	rel, ok := relClean(b.Path)
	if !remoteNameRE.MatchString(b.Remote) || !ok || rel == "" {
		http.Error(w, "Invalid remote/path", http.StatusBadRequest)
		return
	}
	cmd := "deletefile"
	if b.IsDir {
		cmd = "purge"
	}
	if rc, out := rcloneRun(cmd, b.Remote+":"+rel); rc != 0 {
		http.Error(w, strings.TrimSpace(out), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// rcloneMoveto renames / moves a file or directory within a remote (moveto).
func rcloneMoveto(w http.ResponseWriter, req *http.Request) { rcloneMoveCopy(w, req, "moveto") }

// rcloneCopyto copies a file or directory within a remote (copyto).
func rcloneCopyto(w http.ResponseWriter, req *http.Request) { rcloneMoveCopy(w, req, "copyto") }

func rcloneMoveCopy(w http.ResponseWriter, req *http.Request, cmd string) {
	var b struct {
		Remote string `json:"remote"`
		Src    string `json:"src"`
		Dst    string `json:"dst"`
	}
	_ = json.NewDecoder(req.Body).Decode(&b)
	src, ok1 := relClean(b.Src)
	dst, ok2 := relClean(b.Dst)
	if !remoteNameRE.MatchString(b.Remote) || !ok1 || !ok2 || src == "" || dst == "" {
		http.Error(w, "Invalid remote/path", http.StatusBadRequest)
		return
	}
	if rc, out := rcloneRun(cmd, b.Remote+":"+src, b.Remote+":"+dst); rc != 0 {
		http.Error(w, strings.TrimSpace(out), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// rcloneSize returns du (count + bytes) for a remote path via `rclone size --json`.
func rcloneSize(w http.ResponseWriter, req *http.Request) {
	remote := req.URL.Query().Get("remote")
	rel, ok := relClean(req.URL.Query().Get("path"))
	if !remoteNameRE.MatchString(remote) || !ok {
		http.Error(w, "Invalid remote/path", http.StatusBadRequest)
		return
	}
	rc, out := rcloneRun("size", "--json", remote+":"+rel)
	if rc != 0 {
		http.Error(w, strings.TrimSpace(out), http.StatusInternalServerError)
		return
	}
	var s struct {
		Count int64 `json:"count"`
		Bytes int64 `json:"bytes"`
	}
	_ = json.Unmarshal([]byte(out), &s)
	writeJSON(w, http.StatusOK, map[string]any{"count": s.Count, "bytes": s.Bytes, "human": humanBytes(s.Bytes)})
}

// rcloneAbout returns the remote's quota (total/used/free) via `rclone about --json`.
func rcloneAbout(w http.ResponseWriter, req *http.Request) {
	remote := req.URL.Query().Get("remote")
	if !remoteNameRE.MatchString(remote) {
		http.Error(w, "Invalid remote", http.StatusBadRequest)
		return
	}
	rc, out := rcloneRun("about", "--json", remote+":")
	if rc != 0 {
		http.Error(w, strings.TrimSpace(out), http.StatusInternalServerError)
		return
	}
	var a map[string]int64
	_ = json.Unmarshal([]byte(out), &a)
	resp := map[string]any{}
	for k, v := range a {
		resp[k] = v
		resp[k+"_human"] = humanBytes(v)
	}
	writeJSON(w, http.StatusOK, resp)
}

// rcloneFsinfo reports a remote's supported features (About, CleanUp, Purge, Move,
// Copy, DirMove, PublicLink, MergeDirs, …) so the UI greys out unsupported actions.
func rcloneFsinfo(w http.ResponseWriter, req *http.Request) {
	remote := req.URL.Query().Get("remote")
	if !remoteNameRE.MatchString(remote) {
		http.Error(w, "Invalid remote", http.StatusBadRequest)
		return
	}
	rc, out := rcloneRun("rc", "--loopback", "operations/fsinfo", "fs="+remote+":")
	if rc != 0 {
		http.Error(w, strings.TrimSpace(out), http.StatusInternalServerError)
		return
	}
	var info struct {
		Features map[string]bool `json:"Features"`
		Hashes   []string        `json:"Hashes"`
	}
	_ = json.Unmarshal([]byte(out), &info)
	writeJSON(w, http.StatusOK, map[string]any{"features": info.Features, "hashes": info.Hashes})
}

// rcloneCleanup empties trash / removes old versions (rclone cleanup) — only on
// backends whose fsinfo reports CleanUp.
func rcloneCleanup(w http.ResponseWriter, req *http.Request) {
	var b struct {
		Remote string `json:"remote"`
	}
	_ = json.NewDecoder(req.Body).Decode(&b)
	if !remoteNameRE.MatchString(b.Remote) {
		http.Error(w, "Invalid remote", http.StatusBadRequest)
		return
	}
	if rc, out := rcloneRun("cleanup", b.Remote+":"); rc != 0 {
		http.Error(w, strings.TrimSpace(out), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// rcloneDedupe merges duplicate files (rclone dedupe, non-interactive newest).
func rcloneDedupe(w http.ResponseWriter, req *http.Request) {
	var b struct {
		Remote string `json:"remote"`
		Path   string `json:"path"`
	}
	_ = json.NewDecoder(req.Body).Decode(&b)
	rel, ok := relClean(b.Path)
	if !remoteNameRE.MatchString(b.Remote) || !ok {
		http.Error(w, "Invalid remote/path", http.StatusBadRequest)
		return
	}
	if rc, out := rcloneRun("dedupe", "--dedupe-mode", "newest", b.Remote+":"+rel); rc != 0 {
		http.Error(w, strings.TrimSpace(out), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// rcloneLink creates a public link (rclone link) for a file/dir.
func rcloneLink(w http.ResponseWriter, req *http.Request) {
	var b struct {
		Remote string `json:"remote"`
		Path   string `json:"path"`
	}
	_ = json.NewDecoder(req.Body).Decode(&b)
	rel, ok := relClean(b.Path)
	if !remoteNameRE.MatchString(b.Remote) || !ok || rel == "" {
		http.Error(w, "Invalid remote/path", http.StatusBadRequest)
		return
	}
	rc, out := rcloneRun("link", b.Remote+":"+rel)
	if rc != 0 {
		http.Error(w, strings.TrimSpace(out), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"url": strings.TrimSpace(out)})
}

// rcloneCategories returns per-category storage (Movies/TV/…) for a teldrive
// remote, read straight from the teldrive server API (instant — from its DB),
// via curl on the host so it shares rclone's network. Auth from the conf
// (X-Api-Key header and/or access_token cookie).
func rcloneCategories(w http.ResponseWriter, req *http.Request) {
	remote := req.URL.Query().Get("remote")
	if !remoteNameRE.MatchString(remote) {
		http.Error(w, "Invalid remote", http.StatusBadRequest)
		return
	}
	conf, _ := rclone.Remotes(rcloneConfPath())
	p := conf[remote]
	if p == nil || p["type"] != "teldrive" {
		http.Error(w, "Not a teldrive remote", http.StatusBadRequest)
		return
	}
	host := strings.TrimRight(p["api_host"], "/")
	if host == "" {
		http.Error(w, "Remote has no api_host", http.StatusBadRequest)
		return
	}
	args := []string{"curl", "-fsS", "--max-time", "20", host + "/api/files/categories"}
	if k := p["api_key"]; k != "" {
		args = append(args, "-H", "X-Api-Key: "+k)
	}
	if t := p["access_token"]; t != "" {
		args = append(args, "-H", "Cookie: access_token="+t)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	rc, out, _ := executor.Get().Run(ctx, args, "")
	if rc != 0 {
		http.Error(w, "teldrive API call failed", http.StatusBadGateway)
		return
	}
	var raw []struct {
		Category   string `json:"category"`
		TotalFiles int64  `json:"totalFiles"`
		TotalSize  int64  `json:"totalSize"`
	}
	if json.Unmarshal([]byte(out), &raw) != nil {
		http.Error(w, "bad teldrive response", http.StatusBadGateway)
		return
	}
	cats := make([]map[string]any, 0, len(raw))
	var total int64
	for _, c := range raw {
		total += c.TotalSize
		cats = append(cats, map[string]any{"category": c.Category, "files": c.TotalFiles, "bytes": c.TotalSize, "human": humanBytes(c.TotalSize)})
	}
	writeJSON(w, http.StatusOK, map[string]any{"categories": cats, "total": total, "total_human": humanBytes(total)})
}
