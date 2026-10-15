package codex

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	core "astrorder.dev/session-daemon/internal/runtime"
	"astrorder.dev/session-daemon/internal/transport/ssh"
)

func TestResumeRecoversNativeGoalWithoutStartingTurn(t *testing.T) {
	for _, scenario := range []string{"active", "cleared", "unsupported", "missing", "foreign"} {
		t.Run(scenario, func(t *testing.T) {
			fake := newFakeSSHSession()
			events := make(chan map[string]any, 4)
			queried := make(chan struct{}, 1)
			go func() {
				decoder, encoder := json.NewDecoder(fake.stdinR), json.NewEncoder(fake.stdoutW)
				for {
					var request map[string]any
					if decoder.Decode(&request) != nil {
						return
					}
					method := request["method"]
					if method == "initialized" {
						continue
					}
					response := map[string]any{"id": request["id"], "result": map[string]any{}}
					switch method {
					case "initialize":
					case "thread/resume":
						response["result"] = map[string]any{"thread": map[string]any{"id": "s"}}
					case "thread/goal/get":
						queried <- struct{}{}
						if request["params"].(map[string]any)["threadId"] != "s" {
							t.Error("queried foreign thread")
						}
						switch scenario {
						case "active", "foreign":
							id := "s"
							if scenario == "foreign" {
								id = "other"
							}
							response["result"] = map[string]any{"goal": map[string]any{"threadId": id, "objective": "Build", "status": "active"}}
						case "cleared":
							response["result"] = map[string]any{"goal": nil}
						case "unsupported":
							delete(response, "result")
							response["error"] = map[string]any{"code": -32601, "message": "unsupported"}
						}
					default:
						t.Errorf("unexpected native mutation: %v", method)
					}
					if encoder.Encode(response) != nil {
						return
					}
				}
			}()
			a := NewSSHAdapter(Config{Executable: "/fixture/codex", Workspace: "/work", Allowed: []string{"/work"}, AgentID: "a"}, func(sid, kind string, payload map[string]any, status string) error {
				if kind == "codex.notification" {
					events <- payload
				}
				return nil
			}, func(context.Context, ssh.CommandSpec) (SSHSession, error) { return fake, nil })
			defer a.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			if _, err := a.Spawn(ctx, core.Request{SessionID: "s"}); err != nil {
				t.Fatal(err)
			}
			select {
			case <-queried:
			default:
				t.Fatal("resume did not query goal")
			}
			if scenario == "active" || scenario == "cleared" {
				select {
				case event := <-events:
					want := "thread/goal/updated"
					if scenario == "cleared" {
						want = "thread/goal/cleared"
					}
					if event["frame"].(map[string]any)["method"] != want {
						t.Fatalf("wrong goal event: %+v", event)
					}
				case <-ctx.Done():
					t.Fatal("goal recovery event missing")
				}
			} else {
				select {
				case event := <-events:
					t.Fatalf("failed read fabricated goal: %+v", event)
				default:
				}
			}
		})
	}
}
