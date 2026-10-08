package protocol_test

import (
	"astrorder.dev/session-daemon/internal/protocol"
	"context"
	"github.com/coder/websocket"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestOverflowDuringPagedSyncDisconnectsSubscriber(t *testing.T) {
	d := protocol.New("test-secret")
	server := httptest.NewServer(d.Handler())
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	dial := func() *websocket.Conn {
		c, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http"), nil)
		if err != nil {
			t.Fatal(err)
		}
		authenticate(t, ctx, c)
		return c
	}
	pub := dial()
	defer pub.CloseNow()
	publish := func() {
		writeJSON(t, ctx, pub, map[string]any{"action": "session.event", "session_id": "s", "event": "token", "payload": map[string]any{}, "status": "running"})
		readJSON(t, ctx, pub)
	}
	publish()
	publish()
	sub := dial()
	defer sub.CloseNow()
	writeJSON(t, ctx, sub, map[string]any{"action": "session.sync", "sessions": map[string]any{"s": 0}, "limit": 1})
	readJSON(t, ctx, sub)
	for i := 0; i < 257; i++ {
		publish()
	}
	closeCtx, stop := context.WithTimeout(ctx, time.Second)
	defer stop()
	if _, _, err := sub.Read(closeCtx); err == nil || closeCtx.Err() != nil {
		t.Fatalf("overflow did not disconnect promptly: %v", err)
	}
	writeJSON(t, ctx, pub, map[string]any{"action": "daemon.status"})
	if r := readJSON(t, ctx, pub); r["action"] != "daemon.status.result" {
		t.Fatal("overflow affected publisher", r)
	}
}
