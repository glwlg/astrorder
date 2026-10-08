package protocol_test

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"astrorder.dev/session-daemon/internal/protocol"
	"astrorder.dev/session-daemon/internal/runtime/codex"
	"github.com/coder/websocket"
)

func TestCodexCompletionRecordedWhileDisconnectedReplaysOnce(t *testing.T) {
	if os.Getenv("ASTRORDER_FAKE_CODEX_OFFLINE") == "1" {
		fakeOfflineCompletion()
		return
	}
	root := t.TempDir()
	daemon, err := protocol.Open("isolated-key", filepath.Join(root, "events.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer daemon.Close()
	adapter := codex.NewAdapter(codex.Config{
		Executable:  os.Args[0],
		Arguments:   []string{"-test.run=TestCodexCompletionRecordedWhileDisconnectedReplaysOnce"},
		Environment: []string{"ASTRORDER_FAKE_CODEX_OFFLINE=1"},
		Workspace:   root,
		Allowed:     []string{root},
		AgentID:     "daemon-codex",
	}, daemon.Emit)
	defer adapter.Close()
	daemon.RegisterRuntime("codex", adapter)
	server := httptest.NewServer(daemon.Handler())
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
	rpc := func(socket *websocket.Conn, request map[string]any) map[string]any {
		t.Helper()
		request["request_id"] = "offline"
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
	created := rpc(first, map[string]any{"action": "session.spawn", "agent_type": "codex", "session_id": "offline-thread", "cwd": root})
	if created["session_id"] != "offline-thread" {
		t.Fatal(created)
	}
	first.CloseNow()

	second := dial()
	defer second.CloseNow()
	rpc(second, map[string]any{"action": "daemon.handshake", "secret": "isolated-key"})
	rpc(second, map[string]any{"action": "daemon.status", "prepare_replay": true})
	replay := rpc(second, map[string]any{"action": "session.sync", "sessions": map[string]any{"offline-thread": 0}, "defer_live": true})
	page := replay["sessions"].(map[string]any)["offline-thread"].(map[string]any)
	frames := page["frames"].([]any)
	completions := 0
	for _, raw := range frames {
		frame := raw.(map[string]any)
		if frame["event"] != "codex.notification" {
			continue
		}
		payload := frame["payload"].(map[string]any)
		native := payload["frame"].(map[string]any)
		if native["method"] == "turn/completed" && payload["agent_id"] == "daemon-codex" {
			completions++
		}
	}
	if completions != 1 {
		t.Fatalf("offline completion was not replayed once: %#v", frames)
	}
	again := rpc(second, map[string]any{"action": "session.sync", "sessions": map[string]any{"offline-thread": page["max_seq_id"]}, "defer_live": true})
	secondPage := again["sessions"].(map[string]any)["offline-thread"].(map[string]any)
	if len(secondPage["frames"].([]any)) != 0 {
		t.Fatal("offline completion was replayed twice", secondPage)
	}
}

func fakeOfflineCompletion() {
	decoder := json.NewDecoder(os.Stdin)
	encoder := json.NewEncoder(os.Stdout)
	for {
		var request map[string]any
		if err := decoder.Decode(&request); err != nil {
			return
		}
		switch request["method"] {
		case "initialize":
			_ = encoder.Encode(map[string]any{"id": request["id"], "result": map[string]any{}})
		case "thread/resume":
			_ = encoder.Encode(map[string]any{"id": request["id"], "result": map[string]any{"thread": map[string]any{"id": "offline-thread"}}})
			_ = encoder.Encode(map[string]any{"method": "turn/completed", "params": map[string]any{"threadId": "offline-thread", "turn": map[string]any{"id": "offline-turn", "status": "completed"}}})
		}
	}
}
