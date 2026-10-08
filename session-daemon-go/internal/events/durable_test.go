package events

import (
	"path/filepath"
	"testing"
)

func TestDurablePublicationSurvivesReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.sqlite3")
	j, err := Open(path, 1)
	if err != nil {
		t.Fatal(err)
	}
	identity := j.DaemonID()
	sub := j.Subscribe()
	frame, err := j.Commit("session", "token", map[string]any{"text": "kept"}, "running")
	if err != nil {
		t.Fatal(err)
	}
	delivered := <-sub.Frames()
	if delivered.SeqID != frame.SeqID {
		t.Fatal("broadcast sequence mismatch")
	}
	// Query a second connection while the publisher is still open: delivery implies durable commit.
	observer, err := Open(path, 1)
	if err != nil {
		t.Fatal(err)
	}
	replay, err := observer.Replay("session", 0)
	if err != nil || len(replay) != 1 {
		t.Fatalf("delivery preceded durable commit: %v %v", replay, err)
	}
	observer.Close()
	j.Close()
	restored, err := Open(path, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	if restored.DaemonID() != identity {
		t.Fatal("daemon identity changed on reopen")
	}
	if restored.Statuses()["session"]["status"] != "running" {
		t.Fatal("lost durable session status")
	}
	next, err := restored.Commit("session", "done", map[string]any{}, "idle")
	if err != nil {
		t.Fatal(err)
	}
	if next.SeqID != frame.SeqID+1 {
		t.Fatal("sequence reset")
	}
	replay, err = restored.Replay("session", 0)
	if err != nil || len(replay) != 2 {
		t.Fatalf("memory retention truncated durable replay: %v %v", replay, err)
	}
}
