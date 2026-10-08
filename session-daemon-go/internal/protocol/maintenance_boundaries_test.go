package protocol_test

import (
	"context"
	"github.com/coder/websocket"
	"testing"
	"time"
)

func TestUnreconciledErrorStateCannotAuthorizeShutdown(t *testing.T) {
	listener := startDaemon(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	socket, _, err := websocket.Dial(ctx, "ws://"+listener.Addr().String()+"/", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer socket.CloseNow()
	authenticate(t, ctx, socket)
	writeJSON(t, ctx, socket, map[string]any{"action": "session.event", "request_id": "failed-state", "session_id": "unreconciled", "event": "session.error", "payload": map[string]any{}, "status": "error"})
	if response := readJSON(t, ctx, socket); response["action"] != "session.event.result" {
		t.Fatal(response)
	}
	writeJSON(t, ctx, socket, map[string]any{"action": "daemon.shutdown", "request_id": "must-refuse"})
	if response := readJSON(t, ctx, socket); response["action"] != "error" {
		t.Fatal("error/unknown lifecycle was accepted as proof of no activity", response)
	}
	writeJSON(t, ctx, socket, map[string]any{"action": "daemon.shutdown", "request_id": "confirmed", "confirm_active": true})
	if response := readJSON(t, ctx, socket); response["action"] != "daemon.shutdown.result" {
		t.Fatal(response)
	}
}
