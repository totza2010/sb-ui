package api

import (
	"strings"
	"testing"
)

func candsOf(names ...string) []upCand {
	out := make([]upCand, len(names))
	for i, n := range names {
		out[i] = upCand{r: uploaderRemote{Name: n}, free: -1}
	}
	return out
}

// The picker walks the sequence in order and skips any step whose remote isn't currently
// eligible, without ever fabricating a repeat or stalling.
func TestPickBySequenceSkipsIneligible(t *testing.T) {
	seq := []string{"A", "B", "A", "C"}
	// B is capped this round (absent from cands); expect A, A, C, A, A, C…
	cands := candsOf("A", "C")
	cursor := 0
	var got []string
	for i := 0; i < 6; i++ {
		idx := pickBySequence(cands, seq, &cursor)
		if idx < 0 {
			t.Fatalf("no pick at step %d", i)
		}
		got = append(got, cands[idx].r.Name)
	}
	want := "A A C A A C"
	if strings.Join(got, " ") != want {
		t.Errorf("sequence with B ineligible = %v, want %q", got, want)
	}

	// Fresh cursor picks seq[0] first, not seq[1].
	c2 := 0
	if idx := pickBySequence(candsOf("A", "B", "C"), seq, &c2); cands[0].r.Name != "A" || idx != 0 {
		t.Errorf("fresh cursor should pick seq[0]=A (idx 0), got idx %d", idx)
	}

	// Nobody in the sequence eligible → -1 (caller waits for a reset).
	c3 := 0
	if idx := pickBySequence(candsOf("Z"), seq, &c3); idx != -1 {
		t.Errorf("expected -1 when no sequenced remote is eligible, got %d", idx)
	}
}

// An empty sequence degrades to plain round-robin so the uploader still works pre-authoring.
func TestPickBySequenceEmptyFallback(t *testing.T) {
	cands := candsOf("A", "B", "C")
	cursor := 0
	var got []string
	for i := 0; i < 4; i++ {
		got = append(got, cands[pickBySequence(cands, nil, &cursor)].r.Name)
	}
	if strings.Join(got, " ") != "A B C A" {
		t.Errorf("empty-seq fallback = %v, want A B C A", got)
	}
}

// Weighted generation respects the ratio and interleaves rather than blocking.
func TestWrrSequenceRatio(t *testing.T) {
	seq := wrrSequence([]string{"03", "02", "adult", "01"}, map[string]int{"03": 3, "02": 3, "adult": 1, "01": 1})
	counts := map[string]int{}
	for _, n := range seq {
		counts[n]++
	}
	if counts["03"] != 3 || counts["02"] != 3 || counts["adult"] != 1 || counts["01"] != 1 {
		t.Fatalf("weighted counts = %v, want 3/3/1/1", counts)
	}
	// Interleaved, not blocked: the two heavy remotes must not run 3× back-to-back.
	if strings.Contains(strings.Join(seq, " "), "03 03 03") {
		t.Errorf("weighted sequence is blocked, not interleaved: %v", seq)
	}
}

// By-fill orders emptiest-first and gives the emptiest the most slots.
func TestGenByRankEmptiestFirst(t *testing.T) {
	fill := map[string]int64{"a": 140, "b": 35, "c": 23} // c emptiest
	seq := genByRank([]string{"a", "b", "c"}, fill, true)
	if seq[0] != "c" {
		t.Errorf("by-fill should start with the emptiest (c), got %v", seq)
	}
	counts := map[string]int{}
	for _, n := range seq {
		counts[n]++
	}
	if !(counts["c"] >= counts["b"] && counts["b"] >= counts["a"]) {
		t.Errorf("emptier accounts should get more slots: %v (%v)", counts, seq)
	}
}

// Migration: an old config with no sequence gets an even rotation over its selected
// remotes, and stale entries (unselected remotes) are dropped.
func TestNormSequenceMigratesAndFilters(t *testing.T) {
	cfg := uploaderConfig{Remotes: []uploaderRemote{{Name: "A"}, {Name: "B"}}}
	if got := normSequence(cfg); strings.Join(got, ",") != "A,B" {
		t.Errorf("migration to even = %v, want [A B]", got)
	}
	cfg.Sequence = []string{"A", "gone", "B", "A"}
	if got := normSequence(cfg); strings.Join(got, ",") != "A,B,A" {
		t.Errorf("filter stale = %v, want [A B A]", got)
	}
}
