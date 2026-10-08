package protocol_test

import (
	"astrorder.dev/session-daemon/internal/protocol"
	"astrorder.dev/session-daemon/internal/runtime/pty"
	"context"
	"encoding/json"
	"github.com/coder/websocket"
	"net/http/httptest"
	"os"
	"path/filepath"
	platform "runtime"
	"strings"
	"testing"
	"time"
)

func TestRealPTYControlReconnectAndOutputReplay(t *testing.T) {
	root := t.TempDir()
	d, err := protocol.Open("isolated-key", filepath.Join(root, "events.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	shell := "/bin/sh"
	input := "stty -echo; printf '%s%s\\n' PTY_REPLAY_ VERIFIED\n"
	if platform.GOOS == "windows" {
		shell = os.Getenv("COMSPEC")
		if shell == "" {
			shell = "cmd.exe"
		}
		input = "@echo off\r\nset PTY_MARKER=PTY_REPLAY_\r\necho %PTY_MARKER%VERIFIED\r\n"
	}
	native, err := pty.New(pty.Config{Allowed: []string{root}, Shell: shell, Emit: d.Emit})
	if err != nil {
		t.Fatal(err)
	}
	defer native.Close()
	d.RegisterRuntime("pty", native)
	server := httptest.NewServer(d.Handler())
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	dial := func() *websocket.Conn {
		t.Helper()
		socket, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http"), nil)
		if err != nil {
			t.Fatal(err)
		}
		return socket
	}
	var received []map[string]any
	rpc := func(socket *websocket.Conn, request map[string]any) map[string]any {
		t.Helper()
		request["request_id"] = "control"
		raw, err := json.Marshal(request)
		if err != nil {
			t.Fatal(err)
		}
		if err = socket.Write(ctx, websocket.MessageText, raw); err != nil {
			t.Fatal(err)
		}
		for {
			_, raw, err = socket.Read(ctx)
			if err != nil {
				t.Fatal(err)
			}
			var response map[string]any
			if err = json.Unmarshal(raw, &response); err != nil {
				t.Fatal(err)
			}
			if response["event"] != nil {
				received = append(received, response)
				continue
			}
			if response["action"] == "error" {
				t.Fatal(response)
			}
			return response
		}
	}
	first := dial()
	rpc(first, map[string]any{"action": "daemon.handshake", "secret": "isolated-key"})
	result := rpc(first, map[string]any{"action": "session.spawn", "agent_type": "pty", "session_id": "terminal", "cwd": root})
	if result["action"] != "session.spawn.result" {
		t.Fatal(result)
	}
	first.CloseNow()
	if native.Snapshot()["terminal"].Status != "running" {
		t.Fatal("connector close killed native terminal")
	}
	second := dial()
	defer second.CloseNow()
	rpc(second, map[string]any{"action": "daemon.handshake", "secret": "isolated-key"})
	rpc(second, map[string]any{"action": "daemon.status", "prepare_replay": true})
	rpc(second, map[string]any{"action": "session.send", "session_id": "terminal", "input": input})
	replay := rpc(second, map[string]any{"action": "session.sync", "sessions": map[string]any{"terminal": 0}, "defer_live": true})
	page := replay["sessions"].(map[string]any)["terminal"].(map[string]any)
	for _, raw := range page["frames"].([]any) {
		received = append(received, raw.(map[string]any))
	}
	rpc(second, map[string]any{"action": "session.sync", "sessions": map[string]any{}, "defer_live": false})
	output := ""
	inspect := func() {
		for _, frame := range received {
			if frame["event"] == "pty.output" {
				payload := frame["payload"].(map[string]any)
				data, _ := payload["data"].(string)
				output += data
			}
		}
		received = nil
	}
	inspect()
	for !strings.Contains(output, "PTY_REPLAY_VERIFIED") {
		_, raw, err := second.Read(ctx)
		if err != nil {
			t.Fatal("native output absent after reconnect", err)
		}
		var frame map[string]any
		if err = json.Unmarshal(raw, &frame); err != nil {
			t.Fatal(err)
		}
		received = append(received, frame)
		inspect()
	}
	rpc(second, map[string]any{"action": "session.resize", "session_id": "terminal", "cols": 120, "rows": 30})
	rpc(second, map[string]any{"action": "session.close", "session_id": "terminal"})
	status := rpc(second, map[string]any{"action": "daemon.status"})
	if _, exists := status["sessions"].(map[string]any)["terminal"]; exists {
		t.Fatal("closed binding remained in authoritative status", status)
	}
}
