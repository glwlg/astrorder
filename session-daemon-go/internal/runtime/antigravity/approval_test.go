package antigravity

import (
	core "astrorder.dev/session-daemon/internal/runtime"
	"context"
	"os"
	"testing"
	"time"
)

func TestApprovalModeNativeReadback(t *testing.T) {
	exe, _ := os.Executable()
	dir := t.TempDir()
	a := New(Config{Executable: exe, Arguments: []string{"-test.run=TestStreamHelper", "--"}, Environment: []string{"ASTRORDER_AGY_HELPER=1"}, Workspace: dir, Allowed: []string{dir}})
	defer a.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	created, err := a.Create(ctx, core.Request{})
	if err != nil {
		t.Fatal(err)
	}
	read, err := a.Command(ctx, core.Request{SessionID: created.SessionID, Action: "session.approval.read"})
	if err != nil || read.Payload["mode"] != "manual" {
		t.Fatalf("initial read: %+v %v", read, err)
	}
	for _, mode := range []string{"full_access", "manual"} {
		changed, e := a.Command(ctx, core.Request{SessionID: created.SessionID, Action: "session.approval.set", Fields: map[string]any{"mode": mode}})
		if e != nil || changed.SessionID != created.SessionID || changed.Payload["accepted"] != true {
			t.Fatalf("switch %s: %+v %v", mode, changed, e)
		}
		read, e = a.Command(ctx, core.Request{SessionID: created.SessionID, Action: "session.approval.read"})
		if e != nil || read.Payload["mode"] != mode {
			t.Fatalf("readback %s: %+v %v", mode, read, e)
		}
		// A model reconnect must retain the permission selection.
		if _, e = a.Command(ctx, core.Request{SessionID: created.SessionID, Action: "session.model.set", Fields: map[string]any{"model": "claude-test"}}); e != nil {
			t.Fatal(e)
		}
		read, e = a.Command(ctx, core.Request{SessionID: created.SessionID, Action: "session.approval.read"})
		if e != nil || read.Payload["mode"] != mode {
			t.Fatalf("model change lost permissions: %+v %v", read, e)
		}
	}
	if _, err = a.Command(ctx, core.Request{SessionID: created.SessionID, Action: "session.approval.set", Fields: map[string]any{"mode": "auto"}}); err == nil {
		t.Fatal("unsupported auto mode accepted")
	}
}
