package hermes

import (
	core "astrorder.dev/session-daemon/internal/runtime"
	"astrorder.dev/session-daemon/internal/transport/ssh"
	"context"
	"encoding/json"
	"testing"
	"time"
)

func TestGoalManagementUsesLiveHandleAndNativeKickoff(t *testing.T) {
	fake := newMockRemoteSession()
	var goal any = map[string]any{"title": "old", "status": "done"}
	kickoff := make(chan struct{}, 1)
	go func() {
		decoder, encoder := json.NewDecoder(fake.stdinR), json.NewEncoder(fake.stdoutW)
		_ = encoder.Encode(map[string]any{"method": "event", "params": map[string]any{"type": "gateway.ready"}})
		for {
			var req map[string]any
			if decoder.Decode(&req) != nil {
				return
			}
			result := map[string]any{}
			params, _ := req["params"].(map[string]any)
			switch req["method"] {
			case "session.resume":
				result = map[string]any{"session_id": "handle", "info": map[string]any{"cwd": "/work"}}
			case "session.control.read":
				result["control"] = map[string]any{"goal": goal}
			case "command.dispatch":
				if params["name"] != "goal" || params["arg"] != "-- clear the backlog" {
					t.Error("unsafe goal command", params)
				}
				goal = map[string]any{"title": "clear the backlog", "status": "active", "turns_used": 0, "max_turns": 20}
				result = map[string]any{"type": "send", "message": "native kickoff"}
			case "prompt.submit":
				if params["text"] != "native kickoff" {
					t.Error("wrong kickoff", params)
				}
				kickoff <- struct{}{}
			case "session.control":
				if params["action"] != "goal.clear" {
					t.Error("wrong clear", params)
				}
				goal = nil
			default:
				t.Error("unexpected method", req["method"])
			}
			if req["method"] != "session.resume" && params["session_id"] != "handle" {
				t.Error("wrong live handle", params)
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
	if _, err := a.Spawn(ctx, core.Request{SessionID: "s"}); err != nil {
		t.Fatal(err)
	}
	result, err := a.Command(ctx, core.Request{SessionID: "s", Action: "session.goal.set", Fields: map[string]any{"objective": "clear the backlog"}})
	if err != nil || result.Status != "running" {
		t.Fatal("set", result, err)
	}
	select {
	case <-kickoff:
	default:
		t.Fatal("kickoff missing")
	}
	if _, err = a.Command(ctx, core.Request{SessionID: "s", Action: "session.goal.set", Fields: map[string]any{"objective": "other"}}); err == nil {
		t.Fatal("active reset accepted")
	}
	result, err = a.Command(ctx, core.Request{SessionID: "s", Action: "session.goal.clear"})
	if err != nil || result.Payload["goal"] != nil {
		t.Fatal("clear", result, err)
	}
}
