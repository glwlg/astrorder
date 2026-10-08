package protocol_test

import (
	"context"
	"github.com/coder/websocket"
	"testing"
	"time"
)

func TestPagedSyncBuffersEventsUntilFinalPage(t *testing.T) {
	listener := startDaemon(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	pub, _, err := websocket.Dial(ctx, "ws://"+listener.Addr().String()+"/", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer pub.CloseNow()
	authenticate(t, ctx, pub)
	publish := func() {
		writeJSON(t, ctx, pub, map[string]any{"action": "session.event", "session_id": "s", "event": "token", "payload": map[string]any{}, "status": "running"})
		readJSON(t, ctx, pub)
	}
	publish()
	publish()
	publish()
	sub, _, err := websocket.Dial(ctx, "ws://"+listener.Addr().String()+"/", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer sub.CloseNow()
	authenticate(t, ctx, sub)
	writeJSON(t, ctx, sub, map[string]any{"action": "session.sync", "sessions": map[string]any{"s": 0}, "limit": 1})
	readJSON(t, ctx, sub)
	publish()
	for after := 1; after < 3; after++ {
		writeJSON(t, ctx, sub, map[string]any{"action": "session.sync", "sessions": map[string]any{"s": after}, "through": map[string]any{"s": 3}, "limit": 1})
		if r := readJSON(t, ctx, sub); r["action"] != "session.sync.result" {
			t.Fatalf("live event interrupted pagination: %v", r)
		}
	}
	if f := readJSON(t, ctx, sub); f["seq_id"] != float64(4) {
		t.Fatal("missing buffered event", f)
	}
}
