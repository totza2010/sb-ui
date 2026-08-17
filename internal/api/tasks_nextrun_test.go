package api

import (
	"strings"
	"testing"
	"time"
)

// Editing a task's schedule must change the next run it reports. The next-run value is
// cached and only recomputed once it has passed, so when the cache was keyed by task id
// alone, changing 20:15 to 20:21 kept reporting the still-future 20:15 — the UI showed the
// old time until that moment arrived, and only a restart cleared it. The schedule is part of
// the cache key now, so the stale answer cannot be produced.
func TestNextRunFollowsAScheduleChange(t *testing.T) {
	const id = "task-1"
	pruneNextRunCache(nil) // start from a clean cache

	first := taskNextRun(id, "15 20 * * *", false)
	if first == "" {
		t.Fatal("a scheduled task should report a next run")
	}
	second := taskNextRun(id, "21 20 * * *", false)
	if second == "" {
		t.Fatal("the edited schedule should report a next run")
	}
	if first == second {
		t.Fatalf("next run did not follow the schedule change: still %s", first)
	}

	// And it reports the NEW schedule, not merely something different.
	got, err := time.Parse(time.RFC3339, second)
	if err != nil {
		t.Fatalf("next run %q is not a timestamp: %v", second, err)
	}
	local := got.Local()
	if local.Hour() != 20 || local.Minute() != 21 {
		t.Errorf("next run = %s (%02d:%02d local), want the 20:21 the new cron asks for",
			second, local.Hour(), local.Minute())
	}
	if !got.After(time.Now()) {
		t.Errorf("next run %s is not in the future", second)
	}
}

// A disabled task reports nothing, and re-enabling it reports a time again — the toggle no
// longer has to clear any cache for that to hold.
func TestNextRunEmptyWhileDisabled(t *testing.T) {
	const id = "task-2"
	pruneNextRunCache(nil)

	if got := taskNextRun(id, "0 3 * * *", true); got != "" {
		t.Errorf("disabled task reported next run %q, want empty", got)
	}
	if got := taskNextRun(id, "", false); got != "" {
		t.Errorf("task with no schedule reported next run %q, want empty", got)
	}
	if got := taskNextRun(id, "0 3 * * *", false); got == "" {
		t.Error("re-enabled task should report a next run again")
	}
}

// The cache must not accumulate entries for schedules nobody uses any more.
func TestPruneNextRunCacheDropsDeadEntries(t *testing.T) {
	pruneNextRunCache(nil)
	taskNextRun("keep", "0 4 * * *", false)
	taskNextRun("gone", "0 5 * * *", false)

	pruneNextRunCache(map[string]bool{nrKey("keep", "0 4 * * *"): true})

	nrMu.Lock()
	defer nrMu.Unlock()
	if len(nextRunCache) != 1 {
		t.Fatalf("cache holds %d entries, want only the live one", len(nextRunCache))
	}
	for k := range nextRunCache {
		if !strings.HasPrefix(k, "keep\x00") {
			t.Errorf("kept the wrong entry: %q", k)
		}
	}
}
