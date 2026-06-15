// Package patches stores role-file edits as unified diffs (or full content for
// sandbox) under /opt/saltbox-ui/patches and re-applies them after sb update.
// Port of patches.py.
package patches

import (
	"context"
	"path"
	"strings"
	"time"

	"github.com/pmezard/go-difflib/difflib"

	"sb-ui/internal/config"
	"sb-ui/internal/executor"
	"sb-ui/internal/store"
)

const fullHeader = "# full-content\n"

func roleBase(repo, role string) string {
	if repo == "sandbox" {
		return "/opt/sandbox/roles/" + role
	}
	return config.Get().SaltboxRepo + "/roles/" + role
}

func patchFile(repo, role, rel string) string {
	return store.Base + "/patches/" + repo + "/" + role + "/" + rel + ".patch"
}

func ctx() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 60*time.Second)
}

// gitOriginal returns the git HEAD content of a saltbox role file, or ("",false).
func gitOriginal(repo, role, rel string) (string, bool) {
	if repo == "sandbox" {
		return "", false
	}
	c, cancel := ctx()
	defer cancel()
	gitPath := "roles/" + role + "/" + rel
	rc, out, err := executor.Get().RunStdout(c,
		[]string{"git", "-C", config.Get().SaltboxRepo, "show", "HEAD:" + gitPath}, "")
	if err != nil || rc != 0 {
		return "", false
	}
	return out, true
}

// BuildPatch returns a unified diff (empty string if identical).
func BuildPatch(original, content, rel string) string {
	diff, _ := difflib.GetUnifiedDiffString(difflib.UnifiedDiff{
		A:        difflib.SplitLines(original),
		B:        difflib.SplitLines(content),
		FromFile: "a/" + rel,
		ToFile:   "b/" + rel,
		Context:  3,
	})
	if diff != "" && !strings.HasSuffix(diff, "\n") {
		diff += "\n"
	}
	return diff
}

// Save writes a patch for an edited role file.
func Save(repo, role, rel, content string) {
	original, ok := gitOriginal(repo, role, rel)
	pf := patchFile(repo, role, rel)
	if !ok {
		store.WriteTextAbs(pf, fullHeader+content)
		return
	}
	patch := BuildPatch(original, content, rel)
	if patch == "" {
		c, cancel := ctx()
		_, _, _ = executor.Get().Run(c, []string{"rm", "-f", pf}, "")
		cancel()
		return
	}
	store.WriteTextAbs(pf, patch)
}

// Preview computes what Save would produce, without writing.
func Preview(repo, role, rel, content string) map[string]any {
	original, ok := gitOriginal(repo, role, rel)
	if !ok {
		return map[string]any{"original": nil, "current": content, "patch": nil, "mode": "full-content"}
	}
	return map[string]any{"original": original, "current": content,
		"patch": BuildPatch(original, content, rel), "mode": "diff"}
}

// List returns relative paths of patched files for a role.
func List(repo, role string) []string {
	base := store.Base + "/patches/" + repo + "/" + role
	c, cancel := ctx()
	defer cancel()
	rc, out, err := executor.Get().Run(c, []string{"find", base, "-name", "*.patch", "-type", "f"}, "")
	if err != nil || rc != 0 || strings.TrimSpace(out) == "" {
		return []string{}
	}
	prefix := strings.TrimRight(base, "/") + "/"
	var res []string
	for _, l := range strings.Split(out, "\n") {
		l = strings.TrimSpace(l)
		if strings.HasPrefix(l, prefix) {
			res = append(res, strings.TrimSuffix(strings.TrimPrefix(l, prefix), ".patch"))
		}
	}
	return res
}

// ReadPatch returns the raw .patch content for a file (or "", false).
func ReadPatch(repo, role, rel string) (string, bool) {
	c, cancel := ctx()
	defer cancel()
	content, err := executor.Get().ReadFile(c, patchFile(repo, role, rel))
	if err != nil {
		return "", false
	}
	return content, true
}

type Result struct {
	Role   string `json:"role"`
	File   string `json:"file"`
	Status string `json:"status"`
	Output string `json:"output,omitempty"`
}

// Restore re-applies all stored patches for a repo (after git reset --hard).
func Restore(repo string) []Result {
	e := executor.Get()
	var results []Result
	repos := []string{repo}
	if repo == "" {
		repos = []string{"saltbox", "sandbox"}
	}
	for _, r := range repos {
		base := store.Base + "/patches/" + r
		c, cancel := ctx()
		rc, out, err := e.Run(c, []string{"find", base, "-name", "*.patch", "-type", "f"}, "")
		cancel()
		if err != nil || rc != 0 || strings.TrimSpace(out) == "" {
			continue
		}
		prefix := strings.TrimRight(base, "/") + "/"
		for _, line := range strings.Split(out, "\n") {
			line = strings.TrimSpace(line)
			if !strings.HasPrefix(line, prefix) {
				continue
			}
			relToBase := strings.TrimPrefix(line, prefix)
			parts := strings.SplitN(relToBase, "/", 2)
			if len(parts) != 2 {
				continue
			}
			role := parts[0]
			rel := strings.TrimSuffix(parts[1], ".patch")
			results = append(results, applyOne(e, r, role, rel, line))
		}
	}
	return results
}

func applyOne(e executor.Executor, repo, role, rel, patchPath string) Result {
	c, cancel := ctx()
	defer cancel()
	patchContent, err := e.ReadFile(c, patchPath)
	if err != nil {
		return Result{repo + "/" + role, rel, "error", err.Error()}
	}
	rb := roleBase(repo, role)
	if strings.HasPrefix(patchContent, fullHeader) {
		dest := rb + "/" + rel
		_ = e.MakeDirs(c, path.Dir(dest))
		if werr := e.WriteFile(c, dest, strings.TrimPrefix(patchContent, fullHeader)); werr != nil {
			return Result{repo + "/" + role, rel, "error", werr.Error()}
		}
		return Result{repo + "/" + role, rel, "full-content", ""}
	}

	use := patchPath
	dryRC, dryOut, _ := e.Run(c, []string{"patch", "--dry-run", "-p1", "-d", rb, "--input", patchPath}, "")
	if dryRC != 0 && !strings.HasSuffix(patchContent, "\n") {
		fixed := patchPath + ".fixed"
		if e.WriteFile(c, fixed, patchContent+"\n") == nil {
			if rc2, _, _ := e.Run(c, []string{"patch", "--dry-run", "-p1", "-d", rb, "--input", fixed}, ""); rc2 == 0 {
				_ = e.WriteFile(c, patchPath, patchContent+"\n")
				_, _, _ = e.Run(c, []string{"rm", "-f", fixed}, "")
				use, dryRC = patchPath, 0
			} else {
				_, _, _ = e.Run(c, []string{"rm", "-f", fixed}, "")
			}
		}
	}
	if dryRC != 0 {
		return Result{repo + "/" + role, rel, "conflict", strings.TrimSpace(dryOut)}
	}
	rc, out, _ := e.Run(c, []string{"patch", "-p1", "-d", rb, "--input", use}, "")
	if rc != 0 {
		return Result{repo + "/" + role, rel, "error", strings.TrimSpace(out)}
	}
	return Result{repo + "/" + role, rel, "ok", ""}
}
