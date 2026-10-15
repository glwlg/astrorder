package codex

import (
	core "astrorder.dev/session-daemon/internal/runtime"
	"context"
	"testing"
)

func TestQueuedSendNeverSteersActiveTurn(t *testing.T) {
	rt := New(Config{})
	defer rt.Close()
	rt.setSession("thread", "running")
	rt.activeTurnID = "active"
	frames := make(chan map[string]any, 2)
	rt.stdin = captureWriter{frames}
	a := NewAdapter(Config{AgentID: "a"}, nil)
	a.sessions["thread"] = &adapterSession{runtime: rt}
	defer a.Close()
	_, err := a.Command(context.Background(), core.Request{Action: "session.send", SessionID: "thread", Fields: map[string]any{"prompt": "queued", "params": map[string]any{"idle_only": true}}})
	if err == nil || err.Error() != "queued send requires an idle session" {
		t.Fatalf("queue crossed into steering: %v", err)
	}
	if len(frames) != 0 {
		t.Fatal("queue wrote native input")
	}
}
