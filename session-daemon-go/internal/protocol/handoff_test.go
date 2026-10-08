package protocol_test

import (
	"context"
	"github.com/coder/websocket"
	"testing"
	"time"
)

func TestExplicitHandoffDefersLiveAcrossSessionBatches(t *testing.T) {
	listener := startDaemon(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	dial := func() *websocket.Conn {
		c, _, err := websocket.Dial(ctx, "ws://"+listener.Addr().String()+"/", nil)
		if err != nil {
			t.Fatal(err)
		}
		authenticate(t, ctx, c)
		return c
	}
	pub := dial()
	defer pub.CloseNow()
	publish := func(id string) {
		writeJSON(t, ctx, pub, map[string]any{"action": "session.event", "session_id": id, "event": "token", "payload": map[string]any{}, "status": "idle"})
		readJSON(t, ctx, pub)
	}
	publish("a")
	publish("b")
	sub := dial()
	defer sub.CloseNow()
	writeJSON(t, ctx, sub, map[string]any{"action": "session.sync", "sessions": map[string]any{"a": 0}, "defer_live": true})
	readJSON(t, ctx, sub)
	publish("a")
	// If live was activated after the first batch, the next read may be a token.
	writeJSON(t, ctx, sub, map[string]any{"action": "session.sync", "sessions": map[string]any{"b": 0}, "defer_live": true})
	if r := readJSON(t, ctx, sub); r["action"] != "session.sync.result" {
		t.Fatal("live frame interleaved with batch", r)
	}
	writeJSON(t, ctx, sub, map[string]any{"action": "session.sync", "sessions": map[string]any{}, "defer_live": false})
	if r := readJSON(t, ctx, sub); r["action"] != "session.sync.result" {
		t.Fatal("handoff acknowledgement overtaken", r)
	}
	if f := readJSON(t, ctx, sub); f["session_id"] != "a" || f["seq_id"] != float64(2) {
		t.Fatal("lost deferred event", f)
	}
}
