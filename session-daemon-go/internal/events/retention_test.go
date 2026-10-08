package events

import (
	storage "astrorder.dev/session-daemon/internal/journal"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRetentionStatusMatchesDurableReplay(t *testing.T) {
	j, err := Open(filepath.Join(t.TempDir(), "events.db"), 100)
	if err != nil {
		t.Fatal(err)
	}
	defer j.Close()
	if err = j.store.SetRetention(storage.Retention{TotalBytes: 2048, SessionBytes: 600, MaxAge: time.Hour}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 6; i++ {
		if _, err = j.Commit("s", "text", map[string]any{"text": strings.Repeat("x", 200)}, "idle"); err != nil {
			t.Fatal(err)
		}
	}
	p, err := j.Page("s", 0, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	state := j.Statuses()["s"]
	if state["min_seq_id"] != p.MinSeq || state["max_seq_id"] != p.Through {
		t.Fatalf("stale status: %v page=%+v", state, p)
	}
	if err = j.store.Prune(time.Now().Add(2 * time.Hour)); err != nil {
		t.Fatal(err)
	}
	state = j.Statuses()["s"]
	if state["min_seq_id"] != int64(7) || state["max_seq_id"] != int64(6) {
		t.Fatalf("expired status: %v", state)
	}
}
