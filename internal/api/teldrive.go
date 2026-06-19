package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"path"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"sb-ui/internal/executor"
	"sb-ui/internal/rclone"
)

// teldrive (tgdrive) enhancements: a dedicated panel that only applies when the
// rclone config has teldrive-type remotes. Federated search across every teldrive
// remote at once (their own UI is one-instance and doesn't show the folder path),
// resolving each hit's containing folder so you can jump straight to it.

type tdRemote struct {
	Name, Host, APIKey, AccessToken string
}

// teldriveRemotes returns every teldrive remote from rclone.conf (with API auth).
func teldriveRemotes() []tdRemote {
	conf, _ := rclone.Remotes(rcloneConfPath())
	var out []tdRemote
	for name, p := range conf {
		host := strings.TrimRight(p["api_host"], "/")
		if p["type"] == "teldrive" && host != "" {
			out = append(out, tdRemote{Name: name, Host: host, APIKey: p["api_key"], AccessToken: p["access_token"]})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// get calls the teldrive server API over curl on the host (shares rclone's
// network). The token is sent every way teldrive forks accept auth (cookie
// access_token, X-Api-Key header, Bearer) since we can't know which the server
// uses. On a non-2xx, curl -f exits non-zero and the message is returned for
// diagnostics.
func (r tdRemote) get(ctx context.Context, rel string) (int, string) {
	tok := r.AccessToken
	if tok == "" {
		tok = r.APIKey
	}
	args := []string{"curl", "-fsS", "--max-time", "20", r.Host + rel}
	if tok != "" {
		args = append(args,
			"-H", "Cookie: access_token="+tok,
			"-H", "X-Api-Key: "+tok,
			"-H", "Authorization: Bearer "+tok,
		)
	}
	rc, out, _ := executor.Get().Run(ctx, args, "")
	return rc, out
}

// teldriveRemotesHandler lists teldrive remote names (so the UI can show/hide the
// panel — empty = no teldrive configured).
func teldriveRemotesHandler(w http.ResponseWriter, _ *http.Request) {
	rs := teldriveRemotes()
	names := make([]string, 0, len(rs))
	for _, r := range rs {
		names = append(names, r.Name)
	}
	writeJSON(w, http.StatusOK, map[string]any{"remotes": names})
}

type tdFileJSON struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Type      string `json:"type"`
	Size      int64  `json:"size"`
	Category  string `json:"category"`
	UpdatedAt string `json:"updatedAt"`
	ParentID  string `json:"parentId"`
	Path      string `json:"path"`
}

type tdResult struct {
	Remote   string `json:"remote"`
	Name     string `json:"name"`
	IsDir    bool   `json:"is_dir"`
	Size     int64  `json:"size"`
	Human    string `json:"human"`
	Category string `json:"category"`
	Modified string `json:"modified"`
	Dir      string `json:"dir"` // containing folder (for jump-to-folder)
}

// teldriveStorage aggregates per-category storage across ALL teldrive remotes —
// the enhancement teldrive's own web UI can't do (it shows one account at a time).
func teldriveStorage(w http.ResponseWriter, _ *http.Request) {
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
	defer cancel()

	type catJSON struct {
		Category   string `json:"category"`
		TotalFiles int64  `json:"totalFiles"`
		TotalSize  int64  `json:"totalSize"`
	}
	var mu sync.Mutex
	remotesOut := []map[string]any{}
	aggBytes := map[string]int64{}
	aggFiles := map[string]int64{}
	var grandBytes, grandFiles int64
	var wg sync.WaitGroup

	for _, r := range teldriveRemotes() {
		wg.Add(1)
		go func(r tdRemote) {
			defer wg.Done()
			rc, out := r.get(ctx, "/api/files/categories")
			if rc != 0 {
				return
			}
			var cats []catJSON
			if json.Unmarshal([]byte(out), &cats) != nil {
				return
			}
			var rb, rf int64
			rcats := make([]map[string]any, 0, len(cats))
			for _, c := range cats {
				rb += c.TotalSize
				rf += c.TotalFiles
				rcats = append(rcats, map[string]any{"category": c.Category, "bytes": c.TotalSize, "human": humanBytes(c.TotalSize), "files": c.TotalFiles})
			}
			sort.SliceStable(rcats, func(i, j int) bool { return rcats[i]["bytes"].(int64) > rcats[j]["bytes"].(int64) })
			mu.Lock()
			remotesOut = append(remotesOut, map[string]any{"remote": r.Name, "bytes": rb, "human": humanBytes(rb), "files": rf, "categories": rcats})
			for _, c := range cats {
				aggBytes[c.Category] += c.TotalSize
				aggFiles[c.Category] += c.TotalFiles
			}
			grandBytes += rb
			grandFiles += rf
			mu.Unlock()
		}(r)
	}
	wg.Wait()
	sort.SliceStable(remotesOut, func(i, j int) bool { return remotesOut[i]["bytes"].(int64) > remotesOut[j]["bytes"].(int64) })

	agg := make([]map[string]any, 0, len(aggBytes))
	for cat, b := range aggBytes {
		agg = append(agg, map[string]any{"category": cat, "bytes": b, "human": humanBytes(b), "files": aggFiles[cat]})
	}
	sort.SliceStable(agg, func(i, j int) bool { return agg[i]["bytes"].(int64) > agg[j]["bytes"].(int64) })

	writeJSON(w, http.StatusOK, map[string]any{
		"remotes": remotesOut, "categories": agg,
		"total_bytes": grandBytes, "total_human": humanBytes(grandBytes), "total_files": grandFiles,
	})
}

// tdFindRel builds the find request exactly like the teldrive web UI (spaces as
// %20, with page/order/sort) — matching it avoids subtle search-param mismatches.
func tdFindRel(q string, limit int) string {
	enc := strings.ReplaceAll(url.QueryEscape(q), "+", "%20")
	return "/api/files?page=1&order=asc&sort=name&operation=find&query=" + enc + "&limit=" + strconv.Itoa(limit)
}

// teldriveSearch fans a find query out to every teldrive remote in parallel, merges
// the hits, and resolves each one's containing folder (from the result's own path,
// or by resolving its parent once per unique parent).
func teldriveSearch(w http.ResponseWriter, req *http.Request) {
	q := strings.TrimSpace(req.URL.Query().Get("q"))
	if q == "" {
		writeJSON(w, http.StatusOK, map[string]any{"results": []tdResult{}})
		return
	}
	limit := 50
	if n, err := strconv.Atoi(req.URL.Query().Get("limit")); err == nil && n > 0 && n <= 200 {
		limit = n
	}
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
	defer cancel()
	rel := tdFindRel(q, limit)

	// debug: return the raw request + response per remote (open the endpoint directly
	// with &debug=1) to see exactly what the teldrive server replies.
	if req.URL.Query().Get("debug") != "" {
		dbg := []map[string]any{}
		for _, r := range teldriveRemotes() {
			rc, out := r.get(ctx, rel)
			if len(out) > 800 {
				out = out[:800]
			}
			dbg = append(dbg, map[string]any{"remote": r.Name, "url": r.Host + rel, "rc": rc, "raw": out})
		}
		writeJSON(w, http.StatusOK, map[string]any{"debug": dbg})
		return
	}

	var mu sync.Mutex
	var all []tdResult
	var errs []string
	var wg sync.WaitGroup
	for _, r := range teldriveRemotes() {
		wg.Add(1)
		go func(r tdRemote) {
			defer wg.Done()
			rc, out := r.get(ctx, rel)
			if rc != 0 {
				msg := strings.SplitN(strings.TrimSpace(out), "\n", 2)[0]
				if msg == "" {
					msg = "request failed"
				}
				mu.Lock()
				errs = append(errs, r.Name+": "+msg)
				mu.Unlock()
				return
			}
			var fl struct {
				Items []tdFileJSON `json:"items"`
			}
			if json.Unmarshal([]byte(out), &fl) != nil {
				mu.Lock()
				errs = append(errs, r.Name+": bad response")
				mu.Unlock()
				return
			}
			parentDir := map[string]string{} // parentId -> folder path (resolved once)
			local := make([]tdResult, 0, len(fl.Items))
			for _, f := range fl.Items {
				dir := ""
				switch {
				case f.Path != "":
					dir = path.Dir("/" + strings.TrimPrefix(f.Path, "/"))
				case f.ParentID != "":
					if d, ok := parentDir[f.ParentID]; ok {
						dir = d
					} else if rc2, out2 := r.get(ctx, "/api/files/"+url.PathEscape(f.ParentID)); rc2 == 0 {
						var one struct {
							Path string `json:"path"`
						}
						_ = json.Unmarshal([]byte(out2), &one)
						dir = one.Path
						parentDir[f.ParentID] = dir
					}
				}
				local = append(local, tdResult{
					Remote: r.Name, Name: f.Name, IsDir: f.Type == "folder",
					Size: f.Size, Human: humanBytes(f.Size), Category: f.Category,
					Modified: f.UpdatedAt, Dir: dir,
				})
			}
			mu.Lock()
			all = append(all, local...)
			mu.Unlock()
		}(r)
	}
	wg.Wait()
	sort.SliceStable(all, func(i, j int) bool { return all[i].Modified > all[j].Modified })
	writeJSON(w, http.StatusOK, map[string]any{"results": all, "count": len(all), "errors": errs})
}
