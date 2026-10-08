package protocol_test

import (
	"context"
	"github.com/coder/websocket"
	"testing"
	"time"
)

func TestHandoffRejectsUnfinishedPagesAndInvalidFlag(t *testing.T) {
	listener := startDaemon(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	c, _, err := websocket.Dial(ctx, "ws://"+listener.Addr().String()+"/", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.CloseNow()
	authenticate(t, ctx, c)
	for i := 0; i < 2; i++ {
		writeJSON(t, ctx, c, map[string]any{"action": "session.event", "session_id": "s", "event": "token", "payload": map[string]any{}, "status": "idle"})
		readJSON(t, ctx, c)
	}
	writeJSON(t, ctx, c, map[string]any{"action": "session.sync", "sessions": map[string]any{"s": 0}, "limit": 1, "defer_live": true})
	readJSON(t, ctx, c)
	writeJSON(t, ctx, c, map[string]any{"action": "session.sync", "sessions": map[string]any{}, "defer_live": false})
	if r := readJSON(t, ctx, c); r["action"] != "error" {
		t.Fatal("accepted unfinished handoff", r)
	}
	writeJSON(t, ctx, c, map[string]any{"action": "session.sync", "sessions": map[string]any{"s": 1}, "through": map[string]any{"s": 2}, "defer_live": "true"})
	if r := readJSON(t, ctx, c); r["action"] != "error" {
		t.Fatal("accepted non-bool defer_live", r)
	}
}
