package codex

import (
	"context"
	"testing"
	"time"

	core "astrorder.dev/session-daemon/internal/runtime"
)

func TestGuidanceWaitsForPendingStartReceipt(t *testing.T) {
	rt := New(Config{})
	defer rt.Close()
	rt.setSession("thread", "idle")
	frames := make(chan map[string]any, 4)
	rt.stdin = captureWriter{frames}
	a := NewAdapter(Config{}, nil)
	a.sessions["thread"] = &adapterSession{runtime: rt}
	defer a.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	first := make(chan error, 1)
	go func() {
		_, err := a.Command(ctx, core.Request{Action: "session.send", SessionID: "thread", Fields: map[string]any{"prompt": "initial", "command_id": "initial"}})
		first <- err
	}()
	start := <-frames
	if start["method"] != "turn/start" {
		t.Fatalf("expected initial start: %v", start)
	}
	guided := make(chan error, 1)
	go func() {
		_, err := a.Command(ctx, core.Request{Action: "session.send", SessionID: "thread", Fields: map[string]any{"prompt": "follow-up", "command_id": "guidance"}})
		guided <- err
	}()
	select {
	case err := <-guided:
		t.Fatalf("guidance refused while initial receipt pending: %v", err)
	case <-time.After(30 * time.Millisecond):
	}
	respond := func(frame map[string]any, result map[string]any) {
		id := int(frame["id"].(float64))
		rt.mu.Lock()
		waiter := rt.pending[id]
		delete(rt.pending, id)
		rt.mu.Unlock()
		waiter.response <- result
	}
	respond(start, map[string]any{"turn": map[string]any{"id": "original-turn"}})
	select {
	case guidance := <-frames:
		if guidance["method"] != "turn/steer" || guidance["params"].(map[string]any)["expectedTurnId"] != "original-turn" {
			t.Fatalf("pending send started another turn: %v", guidance)
		}
		respond(guidance, map[string]any{"turnId": "original-turn"})
	case <-ctx.Done():
		t.Fatal("pending guidance was never sent")
	}
	if err := <-first; err != nil {
		t.Fatal(err)
	}
	if err := <-guided; err != nil {
		t.Fatal(err)
	}
}
