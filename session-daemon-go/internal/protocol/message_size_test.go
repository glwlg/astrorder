package protocol

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	core "astrorder.dev/session-daemon/internal/runtime"
	"github.com/coder/websocket"
)

type largePromptRuntime struct {
	fixtureRuntime
	received chan string
}

func (f *largePromptRuntime) Command(_ context.Context, r core.Request) (core.Result, error) {
	f.received <- r.Fields["prompt"].(string)
	return core.Result{SessionID: r.SessionID, Status: "idle", Payload: map[string]any{"accepted": true}}, nil
}

func TestWebSocketConnectorAcceptsLongMessages(t *testing.T) {
	d := New("key")
	defer d.Close()
	d.SetConnectorSecret("connector-key")
	server := httptest.NewServer(d.Handler())
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http")+"/ws/v1/connector", &websocket.DialOptions{HTTPHeader: http.Header{"Authorization": []string{"Bearer connector-key"}}})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.CloseNow()
	hello, _ := json.Marshal(map[string]any{"type": "hello", "protocol_version": 1, "agent": map[string]any{"id": "large-connector", "name": strings.Repeat("x", 65536)}})
	if err := conn.Write(ctx, websocket.MessageText, hello); err != nil {
		t.Fatal(err)
	}
	if err := conn.Write(ctx, websocket.MessageText, []byte(`{"type":"ping"}`)); err != nil {
		t.Fatal(err)
	}
	_, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var reply map[string]any
	if err := json.Unmarshal(data, &reply); err != nil {
		t.Fatal(err)
	}
	if reply["type"] != "pong" {
		t.Fatal(reply)
	}
}

func TestWebSocketLongPromptReachesRuntimeAndReceipt(t *testing.T) {
	d, err := Open("key", filepath.Join(t.TempDir(), "journal.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	f := &largePromptRuntime{received: make(chan string, 1)}
	d.RegisterRuntime("test", f)
	server := httptest.NewServer(d.Handler())
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.CloseNow()
	call := func(req map[string]any) map[string]any {
		t.Helper()
		data, err := json.Marshal(req)
		if err != nil {
			t.Fatal(err)
		}
		if err := conn.Write(ctx, websocket.MessageText, data); err != nil {
			t.Fatal(err)
		}
		_, data, err = conn.Read(ctx)
		if err != nil {
			t.Fatal(err)
		}
		var result map[string]any
		if err := json.Unmarshal(data, &result); err != nil {
			t.Fatal(err)
		}
		if result["action"] == "error" {
			t.Fatal(result)
		}
		return result
	}
	call(map[string]any{"action": "daemon.handshake", "secret": "key"})
	call(map[string]any{"action": "session.create", "agent_type": "test"})
	prompt := strings.Repeat("日志行 long log line\n", 4096)
	reply := call(map[string]any{"action": "session.send", "session_id": "created", "prompt": prompt, "operation_id": "long-prompt"})
	if reply["action"] != "session.send.result" {
		t.Fatal(reply)
	}
	select {
	case got := <-f.received:
		if got != prompt {
			t.Fatal("prompt was truncated")
		}
	default:
		t.Fatal("prompt never reached runtime")
	}
	receipt := call(map[string]any{"action": "operation.read", "operation_action": "session.send", "session_id": "created", "operation_id": "long-prompt"})
	if receipt["operation_state"] != "completed" {
		t.Fatal(receipt)
	}
}
