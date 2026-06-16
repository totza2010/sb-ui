// Package ansible runs ansible-playbook for install/update/remove actions,
// routing tags to the right playbook (saltbox / sandbox / mod) and streaming
// output into a job.
package ansible

import (
	"context"
	"strconv"
	"strings"

	"sb-ui/internal/config"
	"sb-ui/internal/executor"
	"sb-ui/internal/jobs"
)

// routeTag returns (repoPath, playbook, bareTag) for a tag.
func routeTag(c *config.Config, tag string) (string, string, string) {
	switch {
	case strings.HasPrefix(tag, "sandbox-"):
		return c.SandboxRepo, c.SandboxPlaybook(), strings.TrimPrefix(tag, "sandbox-")
	case strings.HasPrefix(tag, "mod-"):
		return c.SaltboxModRepo, c.SaltboxModPlaybook(), strings.TrimPrefix(tag, "mod-")
	default:
		return c.SaltboxRepo, c.SaltboxPlaybook(), tag
	}
}

// RunPlaybook runs `ansible-playbook <pb> --tags <bare>` for one tag, streaming
// into the job. Blocks until done.
func RunPlaybook(ctx context.Context, jobID, tag string) int {
	c := config.Get()
	repo, pb, bare := routeTag(c, tag)
	cmd := []string{"sudo", "-n", c.AnsibleBin, pb, "--become", "--tags", bare}

	jobs.SetStatus(jobID, "running")
	jobs.PushLog(jobID, "$ "+strings.Join(cmd, " "))

	s, err := executor.Get().RunStream(ctx, cmd, repo, false)
	if err != nil {
		jobs.PushLog(jobID, "ERROR: "+err.Error())
		jobs.SetStatus(jobID, "failed")
		return -1
	}
	for line := range s.Lines {
		jobs.PushLog(jobID, line)
	}
	code := s.Exit()
	if code == 0 {
		jobs.SetStatus(jobID, "completed")
	} else {
		jobs.PushLog(jobID, "\nPlaybook exited with code "+strconv.Itoa(code))
		jobs.SetStatus(jobID, "failed")
	}
	return code
}

// RunMulti installs several tags at once (sb install a,b,sandbox-c), grouping by
// playbook and running each group sequentially. Stops at the first failure.
func RunMulti(ctx context.Context, jobID string, tags []string) int {
	c := config.Get()
	type group struct {
		repo, pb string
		bares    []string
	}
	order := []string{}
	groups := map[string]*group{}
	for _, t := range tags {
		repo, pb, bare := routeTag(c, t)
		key := pb
		g := groups[key]
		if g == nil {
			g = &group{repo: repo, pb: pb}
			groups[key] = g
			order = append(order, key)
		}
		g.bares = append(g.bares, bare)
	}

	jobs.SetStatus(jobID, "running")
	final := 0
	for _, key := range order {
		g := groups[key]
		cmd := []string{"sudo", "-n", c.AnsibleBin, g.pb, "--become", "--tags", strings.Join(g.bares, ",")}
		jobs.PushLog(jobID, "\n$ "+strings.Join(cmd, " "))
		s, err := executor.Get().RunStream(ctx, cmd, g.repo, false)
		if err != nil {
			jobs.PushLog(jobID, "ERROR: "+err.Error())
			final = -1
			break
		}
		for line := range s.Lines {
			jobs.PushLog(jobID, line)
		}
		if code := s.Exit(); code != 0 {
			jobs.PushLog(jobID, "\nExited with code "+strconv.Itoa(code)+" — stopping.")
			final = code
			break
		}
	}
	if final == 0 {
		jobs.SetStatus(jobID, "completed")
	} else {
		jobs.SetStatus(jobID, "failed")
	}
	return final
}

