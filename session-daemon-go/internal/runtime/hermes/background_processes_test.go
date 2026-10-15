package hermes

import (
	core "astrorder.dev/session-daemon/internal/runtime"
	"astrorder.dev/session-daemon/internal/transport/ssh"
	"context"
	"encoding/json"
	"testing"
	"time"
)

func TestBackgroundProcessesReadUsesNativeHandle(t *testing.T) {
	fake := newMockRemoteSession()
	calls := make(chan string, 8)
	go func() {
		decoder, encoder := json.NewDecoder(fake.stdinR), json.NewEncoder(fake.stdoutW)
		_ = encoder.Encode(map[string]any{"method": "event", "params": map[string]any{"type": "gateway.ready"}})
		for {
			var req map[string]any
			if decoder.Decode(&req) != nil {
				return
			}
			method, _ := req["method"].(string)
			calls <- method
			result := map[string]any{}
			switch method {
			case "session.resume":
				result = map[string]any{"session_id": "live-handle", "info": map[string]any{"cwd": "/work"}}
			case "session.control.read":
				result["control"] = map[string]any{}
			case "process.list":
				p, _ := req["params"].(map[string]any)
				if p["session_id"] != "live-handle" {
					t.Error("wrong session", p)
				}
				result["processes"] = []any{map[string]any{"session_id": "proc-a", "status": "running", "output_tail": "ready"}}
			default:
				t.Error("unexpected call", method)
			}
			if encoder.Encode(map[string]any{"id": req["id"], "result": result}) != nil {
				return
			}
		}
	}()
	a := NewWithSSHStarter(Config{Executable: "/fixture/hermes", Workspace: "/work", Allowed: []string{"/work"}, AgentID: "a"}, func(context.Context, ssh.CommandSpec) (SSHSession, error) { return fake, nil })
	defer a.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	if _, err := a.Spawn(ctx, core.Request{SessionID: "durable"}); err != nil {
		t.Fatal(err)
	}
	result, err := a.Command(ctx, core.Request{SessionID: "durable", Action: "session.processes"})
	if err != nil || result.SessionID != "durable" || len(result.Payload["processes"].([]any)) != 1 {
		t.Fatal(result, err)
	}
	if <-calls != "session.resume" || <-calls != "session.control.read" || <-calls != "process.list" {
		t.Fatal("unexpected routing")
	}
}
