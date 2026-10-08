package protocol

import (
	"encoding/json"
	"path/filepath"
	"testing"
)

func TestSyncPreservesEventTimestamp(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.db")
	d, err := Open("key", path)
	if err != nil {
		t.Fatal(err)
	}
	auth := true
	response, _ := d.handle([]byte(`{"action":"session.event","session_id":"s","event":"token","payload":{},"status":"running","timestamp":1234.5}`), &auth)
	if response["action"] == "error" {
		t.Fatal(response)
	}
	d.Close()
	d, err = Open("key", path)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	response, _ = d.handle([]byte(`{"action":"session.sync","sessions":{"s":0}}`), &auth)
	raw, _ := json.Marshal(response)
	var decoded map[string]any
	json.Unmarshal(raw, &decoded)
	f := decoded["sessions"].(map[string]any)["s"].(map[string]any)["frames"].([]any)[0].(map[string]any)
	if f["timestamp"] != 1234.5 {
		t.Fatalf("lost timestamp: %+v", f)
	}
	response, _ = d.handle([]byte(`{"action":"session.event","session_id":"s","event":"token","payload":{},"status":"running","timestamp":"bad"}`), &auth)
	if response["action"] != "error" {
		t.Fatal("accepted non-numeric timestamp")
	}
}
