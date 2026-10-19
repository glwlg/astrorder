package antigravity

import (
	core "astrorder.dev/session-daemon/internal/runtime"
	"context"
	"os"
	"testing"
	"time"
)

func TestSwitchModelKeepsConversationAndConfirmsNativeModel(t *testing.T) {
	exe, _ := os.Executable()
	dir := t.TempDir()
	a := New(Config{Executable: exe, Arguments: []string{"-test.run=TestStreamHelper", "--"}, Environment: []string{"ASTRORDER_AGY_HELPER=1"}, Workspace: dir, Allowed: []string{dir}})
	defer a.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	initial, err := a.Create(ctx, core.Request{Fields: map[string]any{}})
	if err != nil {
		t.Fatal(err)
	}
	changed, err := a.Command(ctx, core.Request{SessionID: initial.SessionID, Action: "session.model.set", Fields: map[string]any{"provider": "antigravity", "model": "claude-test"}})
	if err != nil {
		t.Fatal(err)
	}
	if changed.SessionID != initial.SessionID || changed.Payload["model"] != "claude-test" {
		t.Fatalf("switch: %#v", changed)
	}
	read, err := a.Command(ctx, core.Request{SessionID: initial.SessionID, Action: "session.model.read"})
	if err != nil || read.Payload["model"] != "claude-test" {
		t.Fatalf("readback: %#v %v", read, err)
	}
	sent, err := a.Command(ctx, core.Request{SessionID: initial.SessionID, Action: "session.send", Fields: map[string]any{"text": "hello"}})
	if err != nil || sent.Payload["accepted"] != true {
		t.Fatalf("send after switch: %#v %v", sent, err)
	}
}
