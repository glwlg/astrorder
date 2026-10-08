package protocol_test

import (
	"context"
	"github.com/coder/websocket"
	"testing"
	"time"
)

func TestSyncHandsOffToLiveWithoutDuplicate(t *testing.T) {
	listener := startDaemon(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	publisher, _, err := websocket.Dial(ctx, "ws://"+listener.Addr().String()+"/", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer publisher.CloseNow()
	authenticate(t, ctx, publisher)
	publish := func() {
		writeJSON(t, ctx, publisher, map[string]any{"action": "session.event", "session_id": "s", "event": "token", "payload": map[string]any{}, "status": "running"})
		readJSON(t, ctx, publisher)
	}
	publish()
	subscriber, _, err := websocket.Dial(ctx, "ws://"+listener.Addr().String()+"/", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer subscriber.CloseNow()
	authenticate(t, ctx, subscriber)
	writeJSON(t, ctx, subscriber, map[string]any{"action": "session.sync", "sessions": map[string]any{"s": 0}})
	if r := readJSON(t, ctx, subscriber); r["action"] != "session.sync.result" {
		t.Fatal(r)
	}
	publish()
	f := readJSON(t, ctx, subscriber)
	if f["seq_id"] != float64(2) || f["session_id"] != "s" || f["timestamp"] == nil {
		t.Fatalf("bad live handoff: %v", f)
	}
}
