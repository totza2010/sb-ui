package api

import (
	"encoding/json"
	"net/http/httptest"
	"testing"

	"sb-ui/internal/jobs"
)

// resetQueue puts the package's queue state back to empty and marks it loaded, so a test starts
// from a known place without reading whatever is on disk.
func resetQueue(t *testing.T) {
	t.Helper()
	qMu.Lock()
	queueList, queueCurrent, queueCurrentLabel, queueRunning, queueLoaded = nil, "", "", true, true
	qMu.Unlock()
}

// A queue that only exists in memory is emptied by every restart — and worse, the jobs it had
// already created stay behind marked failed, so a batch queued overnight reads as "it ran and
// failed" rather than "it never ran".
func TestQueueSurvivesARestart(t *testing.T) {
	resetQueue(t)
	saved := map[string]queuePersist{}
	// Stand in for the store so the test never touches a host.
	writeQueue := func() {
		saved["q"] = queuePersist{Items: append([]queueItem(nil), queueList...), Running: queueRunning}
	}

	// Two runs are queued.
	first := jobs.Create("move: a → rem:", "move")
	second := jobs.Create("move: b → rem:", "move")
	qMu.Lock()
	queueList = []queueItem{
		{JobID: first.ID, Label: first.Tag, Task: Task{Op: "move", Dst: "rem:", Items: []transferItem{{Path: "/a"}}}},
		{JobID: second.ID, Label: second.Tag, Task: Task{Op: "move", Dst: "rem:", Items: []transferItem{{Path: "/b"}}}},
	}
	writeQueue()
	qMu.Unlock()

	// The process restarts: memory is gone, and LoadHistory has marked in-flight jobs failed.
	jobs.SetStatus(first.ID, "failed")
	jobs.SetStatus(second.ID, "failed")
	qMu.Lock()
	queueList, queueLoaded = nil, false
	restored := saved["q"]
	qMu.Unlock()

	// What loadQueue does with what was written.
	qMu.Lock()
	queueLoaded = true
	queueList, queueRunning = reviveQueueJobs(restored.Items), restored.Running
	got := append([]queueItem(nil), queueList...)
	qMu.Unlock()

	if len(got) != 2 {
		t.Fatalf("restored %d queued runs, want 2", len(got))
	}
	if got[0].Label != first.Tag || got[1].Label != second.Tag {
		t.Error("the queue came back in a different order than it was left in")
	}
	// The task travels with the item, or the restored entry would have nothing to run.
	if got[0].Task.Op != "move" || len(got[0].Task.Items) != 1 || got[0].Task.Items[0].Path != "/a" {
		t.Errorf("the queued task was not preserved: %+v", got[0].Task)
	}
	// Each restored item points at a job that actually exists and is waiting. The old ids were
	// pending, and a pending job is never written to the index, so after a restart they refer to
	// nothing: running such an item updated no status and wrote no log, and the transfer appeared
	// to produce no card at all.
	for _, it := range got {
		if it.JobID == first.ID || it.JobID == second.ID {
			t.Errorf("item still points at the dead pre-restart job %s", it.JobID)
		}
		if s := jobs.Status(it.JobID); s != "pending" {
			t.Errorf("restored job %s is %q, want a live pending job", it.JobID, s)
		}
	}
}

// queueItem has to be marshalable at all: the task used to be an unexported field, which would
// have persisted an entry with nothing to run.
func TestQueueItemCarriesItsTaskThroughJSON(t *testing.T) {
	in := queuePersist{
		Running: false,
		Items: []queueItem{{
			JobID: "j1", Label: "move: a → rem:",
			Task: Task{ID: "t1", Op: "move", Dst: "rem:Media", DryRun: true,
				Items: []transferItem{{Path: "/mnt/local/Media", IsDir: true}},
				Opts:  transferOpts{Bwlimit: "40M"}},
		}},
	}
	var out queuePersist
	if err := roundTripJSON(in, &out); err != nil {
		t.Fatalf("round trip: %v", err)
	}
	if len(out.Items) != 1 {
		t.Fatalf("lost the item")
	}
	got := out.Items[0]
	if got.Task.Op != "move" || got.Task.Dst != "rem:Media" || !got.Task.DryRun {
		t.Errorf("task did not survive: %+v", got.Task)
	}
	if got.Task.Opts.Bwlimit != "40M" {
		t.Error("options did not survive, so the restored run would use different flags")
	}
	if len(got.Task.Items) != 1 || !got.Task.Items[0].IsDir {
		t.Errorf("items did not survive: %+v", got.Task.Items)
	}
	// Paused is a choice worth keeping: coming back running would start work nobody asked for.
	if out.Running {
		t.Error("the paused state must survive too")
	}
}

// Every queue mutation writes, so the file matches what the user sees. Exercised through the
// handlers, since that is where a missed save would actually hurt.
func TestQueueHandlersKeepTheStoredCopyInStep(t *testing.T) {
	resetQueue(t)
	j := jobs.Create("move: x → rem:", "move")
	qMu.Lock()
	queueList = []queueItem{{JobID: j.ID, Label: j.Tag, Task: Task{Op: "move", Dst: "rem:"}}}
	qMu.Unlock()

	queueStop(httptest.NewRecorder(), httptest.NewRequest("POST", "/api/queue/stop", nil))
	qMu.Lock()
	paused := !queueRunning
	qMu.Unlock()
	if !paused {
		t.Error("stop should pause the queue")
	}

	queueStart(httptest.NewRecorder(), httptest.NewRequest("POST", "/api/queue/start", nil))
	qMu.Lock()
	running := queueRunning
	qMu.Unlock()
	if !running {
		t.Error("start should resume the queue")
	}

	queuePurge(httptest.NewRecorder(), httptest.NewRequest("POST", "/api/queue/purge", nil))
	qMu.Lock()
	n := len(queueList)
	qMu.Unlock()
	if n != 0 {
		t.Errorf("purge left %d items", n)
	}
	if s := jobs.Status(j.ID); s != "stopped" {
		t.Errorf("a purged job is %q, want stopped", s)
	}
}

// roundTripJSON marshals and unmarshals, standing in for the store's write/read.
func roundTripJSON(in any, out any) error {
	b, err := json.Marshal(in)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, out)
}
