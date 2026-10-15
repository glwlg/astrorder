package codex

import (
	"context"
	"testing"
	"time"

	core "astrorder.dev/session-daemon/internal/runtime"
)

func TestActiveOwnedSendUsesSteerWithoutReplacingTurnCommand(t *testing.T) {
	for _, state := range []string{"running", "waiting_approval"} {
		t.Run(state, func(t *testing.T) {
			rt := New(Config{})
			defer rt.Close()
			rt.setSession("thread", state)
			rt.activeTurnID = "original-turn"
			rt.turnCommands["original-turn"] = "original-command"
			frames := make(chan map[string]any, 2)
			rt.stdin = captureWriter{frames}
			delivery := make(chan map[string]any, 1)
			a := NewAdapter(Config{AgentID: "a"}, func(sid, event string, payload map[string]any, current string) error {
				if sid != "thread" || event != "codex.command_complete" || current != state {
					t.Errorf("unexpected delivery event: %s %s %s", sid, event, current)
				}
				delivery <- payload
				return nil
			})
			a.sessions["thread"] = &adapterSession{runtime: rt}
			defer a.Close()
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			go func() {
				select {
				case request := <-frames:
					params := request["params"].(map[string]any)
					if request["method"] != "turn/steer" || params["threadId"] != "thread" || params["expectedTurnId"] != "original-turn" {
						t.Errorf("active send used wrong native operation: %+v", request)
					}
					inputs := params["input"].([]any)
					if inputs[0].(map[string]any)["text"] != "follow-up" {
						t.Errorf("lost guidance input: %+v", inputs)
					}
					id := int(request["id"].(float64))
					rt.mu.Lock()
					waiter := rt.pending[id]
					delete(rt.pending, id)
					rt.mu.Unlock()
					waiter.response <- map[string]any{"turnId": "original-turn"}
				case <-ctx.Done():
				}
			}()
			result, err := a.Command(ctx, core.Request{Action: "session.send", SessionID: "thread", Fields: map[string]any{"prompt": "follow-up", "command_id": "guide-command"}})
			if err != nil || result.Payload["accepted"] != true || result.Payload["completed"] != true || result.Payload["turn_id"] != "original-turn" {
				t.Fatalf("owned active session refused guidance: result=%+v err=%v", result, err)
			}
			rt.mu.Lock()
			active, original, current := rt.activeTurnID, rt.turnCommands["original-turn"], rt.status
			rt.mu.Unlock()
			if active != "original-turn" || original != "original-command" || current != state {
				t.Fatalf("guidance replaced active command: turn=%s command=%s status=%s", active, original, current)
			}
			if len(frames) != 0 {
				t.Fatal("guidance sent an additional native request")
			}
			select {
			case payload := <-delivery:
				if payload["command_id"] != "guide-command" || payload["agent_id"] != "a" {
					t.Fatalf("delivery lost command identity: %+v", payload)
				}
			default:
				t.Fatal("guidance acknowledgement missing durable event")
			}
		})
	}
}
