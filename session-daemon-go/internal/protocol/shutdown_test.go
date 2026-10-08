package protocol_test

import (
	"context"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func TestShutdownWithoutConfirmationRefusesActiveSessions(t *testing.T) {
	listener := startDaemon(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	socket, _, err := websocket.Dial(ctx, "ws://"+listener.Addr().String()+"/", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer socket.CloseNow()
	authenticate(t, ctx, socket)
	writeJSON(t, ctx, socket, map[string]any{
		"action": "session.event", "request_id": "event-approval", "session_id": "approval-session",
		"event": "approval.requested", "payload": map[string]any{"approval_id": "approval-1"}, "status": "waiting_approval",
	})
	if response := readJSON(t, ctx, socket); response["action"] != "session.event.result" {
		t.Fatalf("approval event rejected: %#v", response)
	}

	writeJSON(t, ctx, socket, map[string]any{"action": "daemon.shutdown", "request_id": "stop-1"})
	refused := readJSON(t, ctx, socket)
	if refused["action"] != "error" || refused["detail"] != "daemon has active sessions; explicit confirmation is required" {
		t.Fatalf("active shutdown was not refused: %#v", refused)
	}
}
