package events

import (
	"path/filepath"
	"testing"
)

func TestStatuslessDurableEventsAppearInReplaySnapshot(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.db")
	j, err := Open(path, 8)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = j.Close() })
	for seq := int64(1); seq <= 130; seq++ {
		f, err := j.Commit("probe-antigravity-local", "connector.event", map[string]any{"type": "session.upsert"}, "")
		if err != nil {
			t.Fatal(err)
		}
		if f.SeqID != seq {
			t.Fatalf("seq=%d want %d", f.SeqID, seq)
		}
	}
	assertSnapshot := func() {
		t.Helper()
		plan, err := j.BeginSubscription(256)
		if err != nil {
			t.Fatal(err)
		}
		defer j.Unsubscribe(plan.Subscription)
		s, ok := plan.Sessions["probe-antigravity-local"]
		if !ok || s.MinSeqID != 1 || s.MaxSeqID != 130 || s.Status != "idle" {
			t.Fatalf("durable statusless stream absent/invalid in snapshot: %+v", plan.Sessions)
		}
		page, err := j.Page("probe-antigravity-local", 0, s.MaxSeqID, 256)
		if err != nil {
			t.Fatal(err)
		}
		if len(page.Frames) != 130 {
			t.Fatalf("replay lost events: %d", len(page.Frames))
		}
	}
	assertSnapshot()
	if err := j.Close(); err != nil {
		t.Fatal(err)
	}
	j, err = Open(path, 8)
	if err != nil {
		t.Fatal(err)
	}
	defer j.Close()
	assertSnapshot()
}
