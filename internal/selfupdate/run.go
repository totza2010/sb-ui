package selfupdate

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"sb-ui/internal/jobs"
)

// saltboxModRolesDir is where the sb-ui role lives so `sb install mod-sbui` finds it.
const (
	saltboxModRolesDir = "/opt/saltbox_mod/roles"
	roleVersionMarker  = saltboxModRolesDir + "/sbui/.sbui-version"
)

// refreshRole downloads the role tarball and extracts it over /opt/saltbox_mod/roles,
// keeping the ansible role in lockstep with the running binary/channel. version is
// stamped into a marker so we can tell later whether the on-disk role is current.
func refreshRole(ctx context.Context, url, version string) error {
	tmp, err := os.CreateTemp("", "sbui-role-*.tar.gz")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	_ = tmp.Close()
	defer os.Remove(tmpPath)
	if err := download(ctx, url, tmpPath); err != nil {
		return err
	}
	if err := extractTarGz(tmpPath, saltboxModRolesDir); err != nil {
		return err
	}
	_ = os.WriteFile(roleVersionMarker, []byte(version), 0o644)
	return nil
}

// extractTarGz unpacks a .tar.gz into dst (creating dirs), skipping any path that would
// escape dst.
func extractTarGz(src, dst string) error {
	f, err := os.Open(src)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer gz.Close()
	root := filepath.Clean(dst)
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		target := filepath.Join(root, filepath.Clean("/"+hdr.Name))
		if target != root && !strings.HasPrefix(target, root+string(os.PathSeparator)) {
			continue // path traversal — skip
		}
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			out, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, os.FileMode(hdr.Mode)&0o777)
			if err != nil {
				return err
			}
			if _, err := io.Copy(out, tr); err != nil { //nolint:gosec // trusted release asset
				_ = out.Close()
				return err
			}
			if err := out.Close(); err != nil {
				return err
			}
		}
	}
	return nil
}

// Run performs an in-place update as a streamed job: download the latest asset,
// atomically swap the running binary, then re-exec. On success the process is
// replaced and never returns; the job log is persisted before the swap so the
// frontend sees it after reconnecting to the new version.
func Run(jobID, channel string) {
	jobs.SetStatus(jobID, "running")
	log := func(s string) { jobs.PushLog(jobID, s) }
	fail := func(format string, a ...any) {
		log("ERROR: " + fmt.Sprintf(format, a...))
		jobs.SetStatus(jobID, "failed")
	}

	if runtime.GOOS != "linux" {
		fail("self-update is only supported on Linux (running %s)", runtime.GOOS)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	log("Checking " + channel + " release…")
	info := Check(ctx, channel)
	if info.Note != "" {
		log(info.Note)
	}
	if info.AssetURL == "" {
		fail("no downloadable asset (%s) found", info.Asset)
		return
	}
	if !info.Available {
		log(fmt.Sprintf("Already up to date (%s).", info.Current))
		jobs.SetStatus(jobID, "completed")
		return
	}
	log(fmt.Sprintf("Updating %s → %s", info.Current, info.Latest))

	self, err := os.Executable()
	if err != nil {
		fail("locate self: %v", err)
		return
	}
	self, _ = filepath.EvalSymlinks(self)

	log("Downloading " + info.Asset + "…")
	tmp := self + ".new"
	if err := download(ctx, info.AssetURL, tmp); err != nil {
		fail("download: %v", err)
		_ = os.Remove(tmp)
		return
	}

	log("Swapping binary…")
	if err := os.Rename(tmp, self); err != nil {
		fail("swap binary: %v", err)
		_ = os.Remove(tmp)
		return
	}

	// Refresh our own saltbox_mod role so `sb install mod-sbui` matches this channel
	// (otherwise a stale role reinstalls the old/stable binary and reverts the update).
	// Best-effort: a role-refresh failure must not abort a successful binary update.
	if info.RoleURL != "" {
		if err := refreshRole(ctx, info.RoleURL, info.Latest); err != nil {
			log("WARNING: couldn't refresh the saltbox_mod role (sb install may reinstall an older build): " + err.Error())
		} else {
			log("Refreshed the saltbox_mod role.")
		}
	}

	log("Update applied — restarting into " + info.Latest + "…")
	jobs.SetStatus(jobID, "completed") // persists the log before we exec away

	// Give the WS a moment to flush the final lines, then re-exec.
	go func() {
		time.Sleep(1500 * time.Millisecond)
		if err := relaunch(self); err != nil {
			// re-exec failed — exit non-zero so systemd (Restart=on-failure)
			// restarts the service, which now points at the swapped binary.
			os.Exit(1)
		}
	}()
}

// download fetches url to dest with mode 0755 (atomic-ish: caller renames).
func download(ctx context.Context, url, dest string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("http %s", resp.Status)
	}
	f, err := os.OpenFile(dest, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, resp.Body); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}
