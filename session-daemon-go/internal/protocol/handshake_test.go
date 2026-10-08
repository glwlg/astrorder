package protocol_test

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"testing"
	"time"

	"astrorder.dev/session-daemon/internal/protocol"
	"github.com/coder/websocket"
)

func TestHandshakeRejectsStatusUntilTheSecretMatches(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	daemon := protocol.New("test-secret")
	server := &http.Server{Handler: daemon.Handler()}
	go server.Serve(listener)
	t.Cleanup(func() {
		server.Close()
		listener.Close()
	})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	socket, _, err := websocket.Dial(ctx, "ws://"+listener.Addr().String()+"/", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer socket.CloseNow()

	writeJSON(t, ctx, socket, map[string]any{"action": "daemon.status", "request_id": "status-1"})
	rejected := readJSON(t, ctx, socket)
	if rejected["action"] != "error" || rejected["detail"] != "daemon handshake is required" {
		t.Fatalf("unauthenticated status was not rejected: %#v", rejected)
	}

	writeJSON(t, ctx, socket, map[string]any{"action": "daemon.handshake", "request_id": "handshake-1", "secret": "wrong"})
	failed := readJSON(t, ctx, socket)
	if failed["action"] != "error" || failed["detail"] != "daemon authentication failed" {
		t.Fatalf("wrong secret was not rejected: %#v", failed)
	}

	writeJSON(t, ctx, socket, map[string]any{"action": "daemon.handshake", "request_id": "handshake-2", "secret": "test-secret"})
	accepted := readJSON(t, ctx, socket)
	if accepted["action"] != "daemon.handshake.result" || accepted["request_id"] != "handshake-2" || accepted["daemon_id"] == "" {
		t.Fatalf("valid handshake rejected: %#v", accepted)
	}

	writeJSON(t, ctx, socket, map[string]any{"action": "daemon.status", "request_id": "status-2"})
	status := readJSON(t, ctx, socket)
	if status["action"] != "daemon.status.result" || status["daemon_id"] != accepted["daemon_id"] {
		t.Fatalf("authenticated status failed: %#v", status)
	}
	runtimes, _ := status["runtimes"].(map[string]any)
	for _, name := range []string{"codex", "pty", "hermes", "ssh", "codex-ssh", "grok", "grok-ssh"} {
		if _, ok := runtimes[name]; !ok {
			t.Fatalf("runtime %s missing from status: %#v", name, status["runtimes"])
		}
	}
}

func writeJSON(t *testing.T, ctx context.Context, socket *websocket.Conn, value any) {
	t.Helper()
	payload, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err := socket.Write(ctx, websocket.MessageText, payload); err != nil {
		t.Fatal(err)
	}
}

func readJSON(t *testing.T, ctx context.Context, socket *websocket.Conn) map[string]any {
	t.Helper()
	_, payload, err := socket.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var value map[string]any
	if err := json.Unmarshal(payload, &value); err != nil {
		t.Fatal(err)
	}
	return value
}
