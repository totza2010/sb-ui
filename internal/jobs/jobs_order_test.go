package jobs

import (
	"testing"
	"time"
)

// The list must come back in the same order every time it is asked. It did not: jobs live in a
// map, Go randomises map iteration, sort.Slice is not stable, and timestamps only had second
// precision — so four jobs created in the same second came back arranged differently on every
// poll and the Activity list visibly shuffled itself while nothing was happening.
func TestListOrderIsStableAcrossCalls(t *testing.T) {
	mu.Lock()
	jobs = map[string]*Job{}
	mu.Unlock()

	// Several jobs created in the same instant, as a restored queue produces.
	same := time.Now().UTC()
	for _, id := range []string{"d", "a", "c", "b"} {
		mu.Lock()
		jobs[id] = &Job{ID: id, Tag: "move: " + id, Action: "move", Status: "completed",
			CreatedAt: same, subs: map[chan Msg]struct{}{}, loaded: true}
		mu.Unlock()
	}

	first := idsOf(ListDicts())
	for i := 0; i < 20; i++ {
		if got := idsOf(ListDicts()); !equal(got, first) {
			t.Fatalf("call %d returned a different order: %v then %v", i, first, got)
		}
	}
	// Ties resolve by id, so the order is not just stable but predictable.
	if !equal(first, []string{"a", "b", "c", "d"}) {
		t.Errorf("equal timestamps should order by id, got %v", first)
	}
}

// Newer work belongs at the top regardless of id.
func TestListPutsNewestFirst(t *testing.T) {
	mu.Lock()
	jobs = map[string]*Job{}
	now := time.Now().UTC()
	jobs["zzz-older"] = &Job{ID: "zzz-older", Action: "move", CreatedAt: now.Add(-time.Minute), subs: map[chan Msg]struct{}{}, loaded: true}
	jobs["aaa-newer"] = &Job{ID: "aaa-newer", Action: "move", CreatedAt: now, subs: map[chan Msg]struct{}{}, loaded: true}
	mu.Unlock()

	if got := idsOf(ListDicts()); !equal(got, []string{"aaa-newer", "zzz-older"}) {
		t.Errorf("newest should come first, got %v", got)
	}
}

// Sub-second precision is what makes jobs created moments apart distinguishable at all, and the
// width is fixed so the values also compare correctly as plain strings.
func TestCreatedAtCarriesFixedWidthSubSeconds(t *testing.T) {
	mu.Lock()
	jobs = map[string]*Job{}
	base := time.Date(2026, 8, 18, 3, 19, 3, 500000000, time.UTC)
	jobs["j1"] = &Job{ID: "j1", Action: "move", CreatedAt: base, subs: map[chan Msg]struct{}{}, loaded: true}
	jobs["j2"] = &Job{ID: "j2", Action: "move", CreatedAt: base.Add(time.Millisecond), subs: map[chan Msg]struct{}{}, loaded: true}
	mu.Unlock()

	got := ListDicts()
	a, b := got[0]["created_at"].(string), got[1]["created_at"].(string)
	if a == b {
		t.Fatal("timestamps a millisecond apart serialised identically — the shuffling comes back")
	}
	if len(a) != len(b) {
		t.Errorf("timestamps must be fixed width to compare as strings: %q vs %q", a, b)
	}
	if a <= b {
		t.Errorf("the newer timestamp should sort above as a string too: %q then %q", a, b)
	}
	// And it still parses as RFC3339, which is what the stored history is read back with.
	if _, err := time.Parse(time.RFC3339, a); err != nil {
		t.Errorf("created_at %q is no longer RFC3339-parseable: %v", a, err)
	}
}

func idsOf(list []map[string]any) []string {
	out := make([]string, len(list))
	for i, m := range list {
		out[i], _ = m["id"].(string)
	}
	return out
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
