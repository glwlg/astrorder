package protocol

import (
	"context"
	"testing"

	core "astrorder.dev/session-daemon/internal/runtime"
)

type dummyControlAdapter struct {
	stopped bool
}

func (d *dummyControlAdapter) Create(ctx context.Context, r core.Request) (core.Result, error) {
	return core.Result{SessionID: r.SessionID, Status: "idle"}, nil
}

func (d *dummyControlAdapter) Close() error {
	d.stopped = true
	return nil
}

func TestControlActionRuntimeDisconnect(t *testing.T) {
	d := New("test-secret")
	adapter := &dummyControlAdapter{}
	d.RegisterRuntime("codex", adapter)

	ctx := context.Background()
	createReq := map[string]any{
		"action":     "session.create",
		"request_id": "c-1",
		"agent_type": "codex",
		"session_id": "sess-codex-1",
	}
	createResp := d.control(ctx, createReq)
	if createResp["action"] != "session.create.result" {
		t.Fatalf("unexpected create response: %+v", createResp)
	}

	// Disconnect codex
	disReq := map[string]any{
		"action":     "runtime.disconnect",
		"request_id": "dis-1",
		"agent_type": "codex",
	}
	disResp := d.control(ctx, disReq)
	if disResp["action"] != "runtime.disconnect.result" {
		t.Fatalf("expected runtime.disconnect.result, got: %+v", disResp)
	}
	result, _ := disResp["result"].(map[string]any)
	released, _ := result["released_sessions"].([]string)
	if len(released) != 1 || released[0] != "sess-codex-1" {
		t.Fatalf("expected [sess-codex-1] released, got: %v", released)
	}
	if !adapter.stopped {
		t.Fatalf("expected adapter to be stopped")
	}
}
