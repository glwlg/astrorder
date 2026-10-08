package protocol

import (
	"encoding/json"
	"path/filepath"
	"testing"
)

func TestSessionSyncReturnsCommittedPage(t *testing.T) {
	d, err := Open("key", filepath.Join(t.TempDir(), "events.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	for i := 0; i < 3; i++ {
		d.recordEvent(map[string]any{"session_id": "s", "event": "token", "payload": map[string]any{}, "status": "running"})
	}
	auth := true
	response, _ := d.handle([]byte(`{"action":"session.sync","request_id":"r","sessions":{"s":0},"limit":2}`), &auth)
	if response["action"] != "session.sync.result" {
		t.Fatal(response)
	}
	raw, _ := json.Marshal(response)
	var decoded map[string]any
	json.Unmarshal(raw, &decoded)
	page := decoded["sessions"].(map[string]any)["s"].(map[string]any)
	if len(page["frames"].([]any)) != 2 || page["has_more"] != true || page["max_seq_id"] != float64(3) || page["next_seq_id"] != float64(2) {
		t.Fatal(page)
	}
	for _, request := range []string{`{"action":"session.sync","sessions":{"s":-1}}`, `{"action":"session.sync","sessions":{"s":0.5}}`, `{"action":"session.sync","sessions":[]}`, `{"action":"session.sync","sessions":{"s":true}}`} {
		response, _ = d.handle([]byte(request), &auth)
		if response["action"] != "error" {
			t.Fatal("accepted invalid sync", response)
		}
	}
}
