package hermes

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	core "astrorder.dev/session-daemon/internal/runtime"
	"astrorder.dev/session-daemon/internal/transport/ssh"
)

func TestResumeRecoversNativeControlGoal(t *testing.T) {
	for _, scenario := range []string{"active", "cleared", "unsupported", "missing"} {
		t.Run(scenario, func(t *testing.T) {
			fake := newMockRemoteSession()
			events := make(chan map[string]any, 4)
			queried := make(chan struct{}, 1)
			go func() {
				decoder, encoder := json.NewDecoder(fake.stdinR), json.NewEncoder(fake.stdoutW)
				if encoder.Encode(map[string]any{"method": "event", "params": map[string]any{"type": "gateway.ready"}}) != nil {
					return
				}
				for {
					var request map[string]any
					if decoder.Decode(&request) != nil {
						return
					}
					response := map[string]any{"id": request["id"], "result": map[string]any{}}
					switch request["method"] {
					case "session.resume":
						response["result"] = map[string]any{"session_id": "handle", "info": map[string]any{"cwd": "/work"}}
					case "session.control.read":
						queried <- struct{}{}
						if request["params"].(map[string]any)["session_id"] != "handle" {
							t.Error("control read did not use live handle")
						}
						switch scenario {
						case "active":
							response["result"] = map[string]any{"control": map[string]any{"goal": map[string]any{"title": "Build", "status": "active", "turns_used": 2, "max_turns": 20}}}
						case "cleared":
							response["result"] = map[string]any{"control": map[string]any{"goal": nil}}
						case "unsupported":
							delete(response, "result")
							response["error"] = map[string]any{"code": -32601, "message": "unsupported"}
						}
					default:
						t.Errorf("unexpected mutation: %v", request["method"])
					}
					if encoder.Encode(response) != nil {
						return
					}
				}
			}()
			a := NewWithSSHStarter(Config{Executable: "/fixture/hermes", Workspace: "/work", Allowed: []string{"/work"}, AgentID: "a", Emit: func(sid, kind string, payload map[string]any, status string) error {
				if kind == "hermes.notification" {
					events <- payload
				}
				return nil
			}}, func(context.Context, ssh.CommandSpec) (SSHSession, error) { return fake, nil })
			defer a.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			if _, err := a.Spawn(ctx, core.Request{SessionID: "s"}); err != nil {
				t.Fatal(err)
			}
			select {
			case <-queried:
			default:
				t.Fatal("resume did not read native goal")
			}
			if scenario == "active" || scenario == "cleared" {
				select {
				case event := <-events:
					if event["session_id"] != "s" || event["frame"].(map[string]any)["type"] != "session.control.update" {
						t.Fatalf("wrong recovery event: %+v", event)
					}
				case <-ctx.Done():
					t.Fatal("native goal not forwarded")
				}
			} else {
				select {
				case event := <-events:
					t.Fatalf("failed read fabricated clear: %+v", event)
				default:
				}
			}
		})
	}
}
