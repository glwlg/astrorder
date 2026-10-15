package hermes

import (
	core "astrorder.dev/session-daemon/internal/runtime"
	"astrorder.dev/session-daemon/internal/transport/ssh"
	"context"
	"encoding/json"
	"testing"
	"time"
)

func TestGoalPauseResumeUsesControlAndResumesKickoff(t *testing.T) {
	fake := newMockRemoteSession()
	prompts := make(chan string, 2)
	go func() {
		decoder, encoder := json.NewDecoder(fake.stdinR), json.NewEncoder(fake.stdoutW)
		_ = encoder.Encode(map[string]any{"method": "event", "params": map[string]any{"type": "gateway.ready"}})
		state := "active"
		turns := 7
		for {
			var req map[string]any
			if decoder.Decode(&req) != nil {
				return
			}
			p, _ := req["params"].(map[string]any)
			result := map[string]any{}
			switch req["method"] {
			case "session.resume":
				result = map[string]any{"session_id": "handle", "info": map[string]any{"cwd": "/work"}}
			case "session.control.read":
				result["control"] = map[string]any{"goal": map[string]any{"title": "keep this", "status": state, "turns_used": turns, "max_turns": 20}}
			case "session.control":
				switch p["action"] {
				case "goal.pause":
					state = "paused"
				case "goal.resume":
					state = "active"
					turns = 0
					result["dispatch"] = map[string]any{"type": "send", "message": "native resume"}
				default:
					t.Error("wrong lifecycle", p)
				}
			case "prompt.submit":
				prompts <- p["text"].(string)
			default:
				t.Error("unexpected native call", req["method"])
			}
			if req["method"] != "session.resume" && p["session_id"] != "handle" {
				t.Error("wrong handle", p)
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
	for _, state := range []string{"paused", "active"} {
		result, err := a.Command(ctx, core.Request{SessionID: "s", Action: "session.goal.set", Fields: map[string]any{"status": state}})
		if err != nil {
			t.Fatal(state, err)
		}
		goal, _ := result.Payload["goal"].(map[string]any)
		expectedTurns := json.Number("7")
		if state == "active" {
			expectedTurns = json.Number("0")
		}
		if goal["status"] != state || goal["turns_used"] != expectedTurns || goal["title"] != "keep this" || goal["max_turns"] != json.Number("20") {
			t.Fatal("native state mismatch", goal)
		}
		if state == "paused" && len(prompts) != 0 {
			t.Fatal("pause submitted a prompt")
		}
	}
	select {
	case prompt := <-prompts:
		if prompt != "native resume" {
			t.Fatal(prompt)
		}
	default:
		t.Fatal("resume did not run native kickoff")
	}
}
