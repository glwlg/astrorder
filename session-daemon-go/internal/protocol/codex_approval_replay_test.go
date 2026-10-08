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

func TestCodexApprovalSurvivesAppReconnectWithoutAutoDecision(t *testing.T) {
	if os.Getenv("ASTRORDER_FAKE_CODEX_PENDING") == "1" {
		fakePendingApproval()
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
		Arguments:   []string{"-test.run=TestCodexApprovalSurvivesAppReconnectWithoutAutoDecision"},
		Environment: []string{"ASTRORDER_FAKE_CODEX_PENDING=1"},
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
		request["request_id"] = "approval"
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
	created := rpc(first, map[string]any{"action": "session.spawn", "agent_type": "codex", "session_id": "approval-thread", "cwd": root})
	if created["session_id"] != "approval-thread" {
		t.Fatal(created)
	}
	first.CloseNow()

	second := dial()
	defer second.CloseNow()
	rpc(second, map[string]any{"action": "daemon.handshake", "secret": "isolated-key"})
	status := rpc(second, map[string]any{"action": "daemon.status"})
	session := status["sessions"].(map[string]any)["approval-thread"].(map[string]any)
	if session["status"] != "waiting_approval" {
		replay := rpc(second, map[string]any{"action": "session.sync", "sessions": map[string]any{"approval-thread": 0}, "defer_live": true})
		t.Fatalf("pending approval became %v frames=%#v", session["status"], replay["sessions"].(map[string]any)["approval-thread"])
	}
	replay := rpc(second, map[string]any{"action": "session.sync", "sessions": map[string]any{"approval-thread": 0}, "defer_live": true})
	page := replay["sessions"].(map[string]any)["approval-thread"].(map[string]any)
	pending := 0
	for _, raw := range page["frames"].([]any) {
		frame := raw.(map[string]any)
		if frame["event"] != "codex.notification" || frame["status"] != "waiting_approval" {
			continue
		}
		payload := frame["payload"].(map[string]any)
		if payload["approval_id"] == "codex:91" {
			pending++
		}
	}
	if pending != 1 {
		t.Fatalf("pending approval was not replayed once: %#v", page["frames"])
	}
}

func fakePendingApproval() {
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
			_ = encoder.Encode(map[string]any{"id": request["id"], "result": map[string]any{"thread": map[string]any{"id": "approval-thread"}}})
			_ = encoder.Encode(map[string]any{
				"id":     91,
				"method": "item/commandExecution/requestApproval",
				"params": map[string]any{"threadId": "approval-thread"},
			})
		default:
			if request["id"] != nil {
				os.Exit(7)
			}
		}
	}
}
