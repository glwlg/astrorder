package protocol_test

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"astrorder.dev/session-daemon/internal/protocol"
	"github.com/coder/websocket"
)

func TestAttachmentRecordedWhileDisconnectedReplaysOnce(t *testing.T) {
	root := t.TempDir()
	daemon, err := protocol.Open("isolated-key", filepath.Join(root, "events.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer daemon.Close()
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
		request["request_id"] = "attachment"
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

	payload := map[string]any{
		"name":           "notes.txt",
		"media_type":     "text/plain",
		"content_base64": "bm90ZXM=",
	}
	first := dial()
	rpc(first, map[string]any{"action": "daemon.handshake", "secret": "isolated-key"})
	recorded := rpc(first, map[string]any{"action": "session.event", "session_id": "attachment-session", "event": "attachment.accepted", "status": "idle", "payload": payload})
	if recorded["seq_id"] == nil {
		t.Fatal(recorded)
	}
	first.CloseNow()

	second := dial()
	defer second.CloseNow()
	rpc(second, map[string]any{"action": "daemon.handshake", "secret": "isolated-key"})
	replay := rpc(second, map[string]any{"action": "session.sync", "sessions": map[string]any{"attachment-session": 0}, "defer_live": true})
	page := replay["sessions"].(map[string]any)["attachment-session"].(map[string]any)
	frames := page["frames"].([]any)
	if len(frames) != 1 || frames[0].(map[string]any)["event"] != "attachment.accepted" {
		t.Fatalf("attachment was not replayed once: %#v", frames)
	}
	got := frames[0].(map[string]any)["payload"].(map[string]any)
	if got["name"] != "notes.txt" || got["media_type"] != "text/plain" || got["content_base64"] != "bm90ZXM=" {
		t.Fatalf("attachment payload changed: %#v", got)
	}
	again := rpc(second, map[string]any{"action": "session.sync", "sessions": map[string]any{"attachment-session": page["max_seq_id"]}, "defer_live": true})
	if len(again["sessions"].(map[string]any)["attachment-session"].(map[string]any)["frames"].([]any)) != 0 {
		t.Fatal("attachment was replayed twice")
	}
}
