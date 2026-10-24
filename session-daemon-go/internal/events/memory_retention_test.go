package events

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestDurableJournalDoesNotRetainPayloads(t *testing.T) {
	j, err := Open(filepath.Join(t.TempDir(), "journal.db"), 2000)
	if err != nil {
		t.Fatal(err)
	}
	defer j.Close()
	payload := strings.Repeat("x", 256*1024)
	for i := 0; i < 8; i++ {
		if _, err := j.Commit("session", "native", map[string]any{"text": payload}, "idle"); err != nil {
			t.Fatal(err)
		}
	}
	retained := j.retained["session"]
	if len(retained) != 1 || retained[0].Payload != nil {
		t.Fatal("durable journal retained native payloads")
	}
	replay, err := j.Replay("session", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(replay) != 8 || replay[0].Payload["text"] != payload {
		t.Fatal("durable replay lost payload")
	}
}
