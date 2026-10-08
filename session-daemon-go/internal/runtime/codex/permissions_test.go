package codex

import (
	"context"
	"encoding/json"
	"io"
	"testing"
)

type captureWriter struct{ frames chan map[string]any }

func (w captureWriter) Write(b []byte) (int, error) {
	var f map[string]any
	if err := json.Unmarshal(b, &f); err != nil {
		return 0, err
	}
	w.frames <- f
	return len(b), nil
}
func (w captureWriter) Close() error { return nil }

var _ io.WriteCloser = captureWriter{}

func TestPermissionApprovalRequiresExplicitGrant(t *testing.T) {
	r := New(Config{})
	defer r.Close()
	r.setSession("thread", "idle")
	frames := make(chan map[string]any, 2)
	r.stdin = captureWriter{frames}
	r.handleNotification("item/permissions/requestApproval", map[string]any{"id": "permission-1", "params": map[string]any{"threadId": "thread"}})
	cmd := Command{Action: "session.approve", SessionID: "thread", ApprovalID: "codex:permission-1", Decision: "accept"}
	if _, err := r.Command(context.Background(), cmd); err == nil {
		t.Fatal("implicit permission grant accepted")
	}
	cmd.Decision = "decline"
	if _, err := r.Command(context.Background(), cmd); err != nil {
		t.Fatal(err)
	}
	result := (<-frames)["result"].(map[string]any)
	if _, ok := result["decision"]; ok {
		t.Fatal("permissions response used command decision schema")
	}
	if len(result["permissions"].(map[string]any)) != 0 {
		t.Fatal("decline granted permissions")
	}
	if _, err := r.Command(context.Background(), cmd); err == nil {
		t.Fatal("duplicate approval accepted")
	}
}

func TestUnsupportedServerRequestGetsRPCError(t *testing.T) {
	r := New(Config{})
	defer r.Close()
	frames := make(chan map[string]any, 1)
	r.stdin = captureWriter{frames}
	r.handleNotification("item/tool/call", map[string]any{"id": float64(8), "params": map[string]any{}})
	frame := <-frames
	if frame["id"] != float64(8) || frame["error"] == nil {
		t.Fatal(frame)
	}
}
