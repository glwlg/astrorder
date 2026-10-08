package events

import (
	"encoding/json"
	"path/filepath"
	"testing"
)

func TestDurablePayloadPreservesNativeNumericID(t *testing.T) {
	j, err := Open(filepath.Join(t.TempDir(), "journal.db"), 16)
	if err != nil {
		t.Fatal(err)
	}
	defer j.Close()
	const id = "9007199254740993"
	f, err := j.Commit("session", "native", map[string]any{"id": json.Number(id)}, "idle")
	if err != nil {
		t.Fatal(err)
	}
	assertID := func(label string, payload map[string]any) {
		t.Helper()
		b, e := json.Marshal(payload)
		if e != nil || string(b) != `{"id":9007199254740993}` {
			t.Errorf("%s numeric identity changed: %s (%v)", label, b, e)
		}
	}
	assertID("committed", f.Payload)
	frames, err := j.Replay("session", 0)
	if err != nil || len(frames) != 1 {
		t.Fatalf("replay: %v %v", frames, err)
	}
	assertID("replay", frames[0].Payload)
	page, err := j.Page("session", 0, 0, 1)
	if err != nil || len(page.Frames) != 1 {
		t.Fatalf("page: %v %v", page, err)
	}
	assertID("page", page.Frames[0].Payload)
}
