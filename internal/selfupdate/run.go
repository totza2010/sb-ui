package selfupdate

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"sb-ui/internal/jobs"
)

// Run performs an in-place update as a streamed job: download the latest asset,
// atomically swap the running binary, then re-exec. On success the process is
// replaced and never returns; the job log is persisted before the swap so the
// frontend sees it after reconnecting to the new version.
func Run(jobID string) {
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

	log("Checking latest release…")
	info := Check(ctx)
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

	log("Update applied — restarting into " + info.Latest + "…")
	jobs.SetStatus(jobID, "completed") // persists the log before we exec away

	// Give the WS a moment to flush the final lines, then re-exec.
	go func() {
		time.Sleep(1500 * time.Millisecond)
		if err := relaunch(self); err != nil {
			// relaunch failed; exit so the supervisor (systemd) restarts us.
			os.Exit(0)
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
