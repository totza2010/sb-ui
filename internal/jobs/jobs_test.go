package jobs

import "testing"

func TestJobStreaming(t *testing.T) {
	j := Create("btop", "install")
	SetStatus(j.ID, "running")
	PushLog(j.ID, "line1")

	// Subscribe mid-job: snapshot has line1, channel delivers new lines + close.
	snap, ch, cancel, ok := Subscribe(j.ID)
	if !ok {
		t.Fatal("subscribe failed")
	}
	defer cancel()
	if len(snap) != 1 || snap[0] != "line1" {
		t.Fatalf("snapshot=%v", snap)
	}

	PushLog(j.ID, "line2")
	if msg := <-ch; msg.Type != "log" || msg.Line != "line2" {
		t.Fatalf("expected log line2, got %+v", msg)
	}

	SetStatus(j.ID, "completed")
	// First a status message...
	got := <-ch
	if got.Type != "status" || got.Status != "completed" {
		t.Fatalf("expected status completed, got %+v", got)
	}
	// ...then the channel is closed.
	if _, open := <-ch; open {
		t.Fatal("channel should be closed after terminal status")
	}
}

func TestSubscribeFinishedJob(t *testing.T) {
	j := Create("plex", "install")
	PushLog(j.ID, "done-line")
	SetStatus(j.ID, "completed")

	snap, ch, _, ok := Subscribe(j.ID)
	if !ok || len(snap) != 1 || snap[0] != "done-line" {
		t.Fatalf("snapshot=%v ok=%v", snap, ok)
	}
	// Already terminal: get a status then immediate close, no live subscription.
	got, open := <-ch
	if !open || got.Type != "status" {
		t.Fatalf("expected status msg, got %+v open=%v", got, open)
	}
	if _, open := <-ch; open {
		t.Fatal("channel should be closed")
	}
}
