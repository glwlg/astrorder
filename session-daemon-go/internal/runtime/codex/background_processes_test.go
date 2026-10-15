package codex

import (
	core "astrorder.dev/session-daemon/internal/runtime"
	"astrorder.dev/session-daemon/internal/transport/ssh"
	"context"
	"encoding/json"
	"testing"
	"time"
)

func TestBackgroundProcessesOnlyReturnsRunningPTYItems(t *testing.T) {
	fake := newFakeSSHSession()
	go func() {
		decoder, encoder := json.NewDecoder(fake.stdinR), json.NewEncoder(fake.stdoutW)
		for {
			var req map[string]any
			if decoder.Decode(&req) != nil {
				return
			}
			method := req["method"]
			if method == "initialized" {
				continue
			}
			result := map[string]any{}
			switch method {
			case "initialize":
			case "thread/resume":
				result["thread"] = map[string]any{"id": "s"}
			case "thread/goal/get":
				result["goal"] = nil
			case "thread/read":
				if req["params"].(map[string]any)["threadId"] != "s" {
					t.Error("foreign thread")
				}
				result["thread"] = map[string]any{"id": "s", "turns": []any{map[string]any{"items": []any{
					map[string]any{"id": "active", "type": "commandExecution", "status": "inProgress", "processId": "pty-live", "command": "serve", "aggregatedOutput": "ready"},
					map[string]any{"id": "done", "type": "commandExecution", "status": "completed", "processId": "pty-old"},
					map[string]any{"id": "tool", "type": "mcpToolCall", "status": "inProgress"},
					map[string]any{"id": "unbound", "type": "commandExecution", "status": "inProgress"},
				}}}}
			default:
				t.Error("unexpected mutation", method)
			}
			if encoder.Encode(map[string]any{"id": req["id"], "result": result}) != nil {
				return
			}
		}
	}()
	a := NewSSHAdapter(Config{Executable: "/fixture/codex", Workspace: "/work", Allowed: []string{"/work"}, AgentID: "a"}, nil, func(context.Context, ssh.CommandSpec) (SSHSession, error) { return fake, nil })
	defer a.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	if _, err := a.Spawn(ctx, core.Request{SessionID: "s"}); err != nil {
		t.Fatal(err)
	}
	result, err := a.Command(ctx, core.Request{SessionID: "s", Action: "session.processes"})
	if err != nil {
		t.Fatal(err)
	}
	rows := result.Payload["processes"].([]any)
	if len(rows) != 1 || rows[0].(map[string]any)["session_id"] != "pty-live" || rows[0].(map[string]any)["output_tail"] != "ready" {
		t.Fatal(rows)
	}
}
