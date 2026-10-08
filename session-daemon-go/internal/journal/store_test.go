package journal_test

import (
	"path/filepath"
	"testing"

	"astrorder.dev/session-daemon/internal/journal"
)

func TestCommittedFramesSurviveReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sessiond.sqlite3")
	store := journal.Open(path)
	frame := store.Append("daemon-a", "session-1", "token", map[string]any{"text": "kept"}, "running")
	store.Close()

	reopened := journal.Open(path)
	defer reopened.Close()
	replay := reopened.After("daemon-a", "session-1", 0)
	if len(replay) != 1 || replay[0].SeqID != frame.SeqID || replay[0].Payload["text"] != "kept" {
		t.Fatalf("reopened journal lost the committed frame: %+v", replay)
	}
	next := reopened.Append("daemon-a", "session-1", "done", map[string]any{}, "idle")
	if next.SeqID != frame.SeqID+1 {
		t.Fatalf("sequence did not continue after reopen: %+v", next)
	}
}
