package codex

import (
	"context"
	"testing"
	"time"

	core "astrorder.dev/session-daemon/internal/runtime"
)

func TestLateNativeCompletionReconcilesUnconfirmedSendAdmission(t *testing.T) {
	rt := New(Config{})
	defer rt.Close()
	rt.setSession("thread", "idle")
	frames := make(chan map[string]any, 2)
	rt.stdin = captureWriter{frames}
	a := NewAdapter(Config{}, nil)
	a.sessions["thread"] = &adapterSession{runtime: rt, inputUnconfirmed: true}
	defer a.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := a.Command(ctx, core.Request{Action: "session.send", SessionID: "thread", Fields: map[string]any{"prompt": "blocked"}}); err == nil || len(frames) != 0 {
		t.Fatalf("unconfirmed send bypassed admission: %v", err)
	}
	rt.handleNotification("turn/started", map[string]any{"params": map[string]any{"threadId": "thread", "turn": map[string]any{"id": "late-turn"}}})
	rt.handleNotification("turn/completed", map[string]any{"params": map[string]any{"threadId": "thread", "turn": map[string]any{"id": "late-turn", "status": "completed"}}})
	go func() {
		select {
		case frame := <-frames:
			if frame["method"] != "turn/start" {
				t.Errorf("new idle input did not start: %v", frame)
			}
			id := int(frame["id"].(float64))
			rt.mu.Lock()
			waiter := rt.pending[id]
			delete(rt.pending, id)
			rt.mu.Unlock()
			waiter.response <- map[string]any{"turn": map[string]any{"id": "new-turn"}}
		case <-ctx.Done():
		}
	}()
	if _, err := a.Command(ctx, core.Request{Action: "session.send", SessionID: "thread", Fields: map[string]any{"prompt": "new intentional input"}}); err != nil {
		t.Fatalf("late authoritative completion left input permanently locked: %v", err)
	}
}
