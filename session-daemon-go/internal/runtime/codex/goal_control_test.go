package codex

import (
	core "astrorder.dev/session-daemon/internal/runtime"
	"astrorder.dev/session-daemon/internal/transport/ssh"
	"context"
	"encoding/json"
	"testing"
	"time"
)

func TestGoalManagementPreservesNativeStateAndVerifiesClear(t *testing.T) {
	for _, failedClear := range []bool{false, true} {
		t.Run(map[bool]string{false: "clear", true: "unconfirmed"}[failedClear], func(t *testing.T) {
			fake := newFakeSSHSession()
			var goal any = map[string]any{"threadId": "s", "objective": "old", "status": "complete", "tokenBudget": 500, "tokensUsed": 200}
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
						result["goal"] = goal
					case "thread/goal/set":
						params := req["params"].(map[string]any)
						if params["status"] != "complete" || params["tokenBudget"] != float64(500) || params["threadId"] != "s" {
							t.Error("native state/budget lost", params)
						}
						goal = map[string]any{"threadId": "s", "objective": params["objective"], "status": params["status"], "tokenBudget": params["tokenBudget"], "tokensUsed": 200}
						result["goal"] = goal
					case "thread/goal/clear":
						if !failedClear {
							goal = nil
						}
					default:
						t.Error("unexpected native request", method)
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
			result, err := a.Command(ctx, core.Request{SessionID: "s", Action: "session.goal.set", Fields: map[string]any{"objective": "new"}})
			if err != nil || result.Payload["goal"].(map[string]any)["objective"] != "new" {
				t.Fatal("set", result, err)
			}
			_, err = a.Command(ctx, core.Request{SessionID: "s", Action: "session.goal.clear"})
			if (err != nil) != failedClear {
				t.Fatal("clear verification", err)
			}
		})
	}
}
