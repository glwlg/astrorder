package protocol_test

import (
	"context"
	"net"
	"net/http"
	"testing"
	"time"

	"astrorder.dev/session-daemon/internal/protocol"
	"github.com/coder/websocket"
)

func TestSessionEventDoesNotBlockStatusOnAnotherConnection(t *testing.T) {
	listener := startDaemon(t)
	slowCtx, slowCancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer slowCancel()
	slow, _, err := websocket.Dial(slowCtx, "ws://"+listener.Addr().String()+"/", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer slow.CloseNow()
	authenticate(t, slowCtx, slow)
	writeJSON(t, slowCtx, slow, map[string]any{
		"action": "session.event", "request_id": "event-1", "session_id": "slow-session",
		"event": "token", "payload": map[string]any{"text": "held"}, "status": "running",
	})
	if accepted := readJSON(t, slowCtx, slow); accepted["action"] != "session.event.result" {
		t.Fatalf("event was not accepted: %#v", accepted)
	}

	fastCtx, fastCancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer fastCancel()
	fast, _, err := websocket.Dial(fastCtx, "ws://"+listener.Addr().String()+"/", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer fast.CloseNow()
	authenticate(t, fastCtx, fast)
	writeJSON(t, fastCtx, fast, map[string]any{"action": "daemon.status", "request_id": "status-fast"})
	status := readJSON(t, fastCtx, fast)
	sessions, _ := status["sessions"].(map[string]any)
	if status["action"] != "daemon.status.result" || sessions["slow-session"] == nil {
		t.Fatalf("status did not include the other session promptly: %#v", status)
	}
}

func startDaemon(t *testing.T) net.Listener {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: protocol.New("test-secret").Handler()}
	go server.Serve(listener)
	t.Cleanup(func() {
		server.Close()
		listener.Close()
	})
	return listener
}

func authenticate(t *testing.T, ctx context.Context, socket *websocket.Conn) {
	t.Helper()
	writeJSON(t, ctx, socket, map[string]any{"action": "daemon.handshake", "request_id": "auth", "secret": "test-secret"})
	if response := readJSON(t, ctx, socket); response["action"] != "daemon.handshake.result" {
		t.Fatalf("authentication failed: %#v", response)
	}
}
