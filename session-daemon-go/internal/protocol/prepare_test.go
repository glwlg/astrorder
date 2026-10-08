package protocol_test

import (
	"context"
	"github.com/coder/websocket"
	"testing"
	"time"
)

func TestPreparedStatusCapturesNewSessionsBeforeFirstSync(t *testing.T) {
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
	sub := dial()
	defer sub.CloseNow()
	writeJSON(t, ctx, sub, map[string]any{"action": "daemon.status", "prepare_replay": true})
	readJSON(t, ctx, sub)
	pub := dial()
	defer pub.CloseNow()
	for i := 0; i < 2; i++ {
		writeJSON(t, ctx, pub, map[string]any{"action": "session.event", "session_id": "new", "event": "token", "payload": map[string]any{}, "status": "idle"})
		readJSON(t, ctx, pub)
	}
	writeJSON(t, ctx, sub, map[string]any{"action": "session.sync", "sessions": map[string]any{}, "defer_live": false})
	readJSON(t, ctx, sub)
	for seq := 1; seq <= 2; seq++ {
		f := readJSON(t, ctx, sub)
		if f["seq_id"] != float64(seq) {
			t.Fatal("lost frame in status/sync gap", f)
		}
	}
}
