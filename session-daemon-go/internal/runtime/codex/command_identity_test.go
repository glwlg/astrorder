package codex

import (
	core "astrorder.dev/session-daemon/internal/runtime"
	"context"
	"encoding/json"
	"testing"
	"time"
)

func TestUserNotificationRetainsCommandIdentity(t *testing.T) {
	frames := make(chan Notification, 8)
	rt := New(Config{OnNotification: func(n Notification) { frames <- n }})
	defer rt.Close()
	rt.setSession("thread", "idle")
	sent := make(chan map[string]any, 2)
	rt.stdin = captureWriter{sent}
	adapter := NewAdapter(Config{}, nil)
	adapter.sessions["thread"] = &adapterSession{runtime: rt}
	defer adapter.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	go func() {
		request := <-sent
		params := request["params"].(map[string]any)
		if params["approvalPolicy"] != "untrusted" || params["approvalsReviewer"] != "user" {
			t.Errorf("App params policy was lost: %v", params)
		}
		frame := map[string]any{"method": "item/started", "params": map[string]any{"threadId": "thread", "turnId": "turn", "item": map[string]any{"id": "user", "type": "userMessage"}}}
		rt.handleNotification("item/started", frame)
		id := int(request["id"].(float64))
		rt.mu.Lock()
		waiter := rt.pending[id]
		delete(rt.pending, id)
		rt.mu.Unlock()
		waiter.response <- map[string]any{"turn": map[string]any{"id": "turn"}}
	}()
	_, err := adapter.Command(ctx, core.Request{Action: "session.send", SessionID: "thread", Fields: map[string]any{"prompt": "hello", "command_id": "browser-command", "params": map[string]any{"approvalPolicy": "untrusted", "approvalsReviewer": "user"}}})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case n := <-frames:
		raw, _ := json.Marshal(n)
		var value map[string]any
		_ = json.Unmarshal(raw, &value)
		if value["CommandID"] != "browser-command" {
			t.Fatalf("native echo lost command identity: %s", raw)
		}
	case <-ctx.Done():
		t.Fatal("no user notification")
	}
}
