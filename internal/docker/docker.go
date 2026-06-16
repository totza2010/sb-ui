// Package docker wraps docker + systemctl via the executor, and tracks image
// update results (persisted to /opt/saltbox-ui). Port of docker_client.py.
package docker

import (
	"context"
	"strings"
	"time"

	"sb-ui/internal/executor"
)

func run(cmd ...string) (int, string) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	rc, out, err := executor.Get().Run(ctx, cmd, "")
	if err != nil {
		return -1, ""
	}
	return rc, out
}

// ActiveServices returns names (without .service) of active systemd services.
func ActiveServices() map[string]bool {
	rc, out := run("systemctl", "list-units", "--type=service", "--state=active",
		"--plain", "--no-legend", "--no-pager")
	set := map[string]bool{}
	if rc != 0 {
		return set
	}
	for _, l := range strings.Split(out, "\n") {
		f := strings.Fields(l)
		if len(f) > 0 {
			set[strings.TrimSuffix(f[0], ".service")] = true
		}
	}
	return set
}

// KnownServices returns saltbox_managed_* unit names (without .service) → state.
func KnownServices() map[string]string {
	rc, out := run("systemctl", "list-unit-files", "saltbox_managed_*",
		"--type=service", "--plain", "--no-legend", "--no-pager")
	m := map[string]string{}
	if rc != 0 {
		return m
	}
	for _, l := range strings.Split(out, "\n") {
		f := strings.Fields(l)
		if len(f) >= 2 {
			m[strings.TrimSuffix(f[0], ".service")] = f[1]
		}
	}
	return m
}

// FuseActive reports whether any rclone/mergerfs FUSE mount is live.
func FuseActive() bool {
	rc, out := run("findmnt", "-rno", "FSTYPE", "-t", "fuse.rclone,fuse.mergerfs,fuse.rclone-mount")
	return rc == 0 && strings.TrimSpace(out) != ""
}

// ContainerAction: start/stop/restart/rm a container.
func ContainerAction(name, action string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	rc, out, err := executor.Get().Run(ctx, []string{"docker", action, name}, "")
	if err != nil {
		return err
	}
	if rc != 0 {
		return &cmdErr{strings.TrimSpace(out)}
	}
	invalidate() // container state changed — force a fresh snapshot next read
	return nil
}

// ServiceAction: start/stop/restart a systemd unit (needs root).
func ServiceAction(unit, action string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	rc, out, err := executor.Get().Run(ctx, []string{"sudo", "-n", "systemctl", action, unit}, "")
	if err != nil {
		return err
	}
	if rc != 0 {
		return &cmdErr{strings.TrimSpace(out)}
	}
	return nil
}

type cmdErr struct{ msg string }

func (e *cmdErr) Error() string {
	if e.msg == "" {
		return "command failed"
	}
	return e.msg
}
