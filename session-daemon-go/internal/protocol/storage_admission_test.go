package protocol

import (
	core "astrorder.dev/session-daemon/internal/runtime"
	"context"
	"path/filepath"
	"testing"
)

type admissionFixture struct{ creates int }

func (f *admissionFixture) Create(context.Context, core.Request) (core.Result, error) {
	f.creates++
	return core.Result{SessionID: "created", Status: "idle"}, nil
}

func TestUnavailableJournalRejectsWorkBeforeNativeDispatch(t *testing.T) {
	d, err := Open("key", filepath.Join(t.TempDir(), "events.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	f := &admissionFixture{}
	d.RegisterRuntime("fixture", f)
	if err = d.journal.Close(); err != nil {
		t.Fatal(err)
	}
	result := d.control(context.Background(), map[string]any{"action": "session.create", "agent_type": "fixture"})
	if result["action"] != "error" || f.creates != 0 {
		t.Fatalf("native work dispatched without durable storage: creates=%d result=%v", f.creates, result)
	}
	health, ok := d.status(nil)["storage"].(map[string]any)
	if !ok || health["degraded"] != true {
		t.Fatalf("storage failure invisible: %v", health)
	}
}
