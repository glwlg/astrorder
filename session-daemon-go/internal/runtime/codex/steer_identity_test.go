package codex

import (
	"context"
	"testing"
	"time"

	core "astrorder.dev/session-daemon/internal/runtime"
)

func TestGuidanceEchoKeepsItsOwnCommandAndOriginalCompletion(t *testing.T) {
	notifications := make(chan Notification, 3)
	rt := New(Config{OnNotification: func(n Notification) { notifications <- n }})
	defer rt.Close()
	rt.setSession("thread", "running")
	rt.activeTurnID = "turn"
	rt.turnCommands["turn"] = "original"
	frames := make(chan map[string]any, 2)
	rt.stdin = captureWriter{frames}
	a := NewAdapter(Config{}, nil)
	a.sessions["thread"] = &adapterSession{runtime: rt}
	defer a.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	go func() {
		request := <-frames
		params := request["params"].(map[string]any)
		if params["clientUserMessageId"] != "guidance" {
			t.Errorf("steer omitted native message identity: %+v", params)
		}
		rt.handleNotification("item/started", map[string]any{"method": "item/started", "params": map[string]any{"threadId": "thread", "turnId": "turn", "item": map[string]any{"type": "userMessage", "id": "native-guidance", "clientId": "guidance"}}})
		id := int(request["id"].(float64))
		rt.mu.Lock()
		waiter := rt.pending[id]
		delete(rt.pending, id)
		rt.mu.Unlock()
		waiter.response <- map[string]any{"turnId": "turn"}
	}()
	if _, err := a.Command(ctx, core.Request{Action: "session.send", SessionID: "thread", Fields: map[string]any{"command_id": "guidance", "prompt": "follow-up"}}); err != nil {
		t.Fatal(err)
	}
	select {
	case n := <-notifications:
		if n.CommandID != "guidance" {
			t.Fatalf("guidance echo attached to original: %+v", n)
		}
	case <-ctx.Done():
		t.Fatal("missing guidance echo")
	}
	rt.handleNotification("turn/completed", map[string]any{"method": "turn/completed", "params": map[string]any{"threadId": "thread", "turn": map[string]any{"id": "turn", "status": "completed"}}})
	select {
	case n := <-notifications:
		if n.CommandID != "original" {
			t.Fatalf("guidance stole original completion: %+v", n)
		}
	case <-ctx.Done():
		t.Fatal("missing original completion")
	}
}
