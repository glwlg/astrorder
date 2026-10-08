package events

import (
	"path/filepath"
	"testing"
)

func TestFailedCommitIsNotBroadcast(t *testing.T) {
	j, err := Open(filepath.Join(t.TempDir(), "events.db"), 2)
	if err != nil {
		t.Fatal(err)
	}
	defer j.Close()
	sub := j.Subscribe()
	if _, err = j.Commit("s", "token", map[string]any{"bad": make(chan int)}, "running"); err == nil {
		t.Fatal("invalid payload was accepted")
	}
	select {
	case <-sub.Frames():
		t.Fatal("failed commit broadcast")
	default:
	}
	f, err := j.Commit("s", "done", map[string]any{}, "idle")
	if err != nil || f.SeqID != 1 {
		t.Fatalf("failed commit consumed sequence: %v %v", f, err)
	}
	j.Close()
	if _, err = j.Commit("s", "token", nil, "running"); err == nil {
		t.Fatal("closed storage accepted commit")
	}
}
