package codex

import (
	core "astrorder.dev/session-daemon/internal/runtime"
	"astrorder.dev/session-daemon/internal/transport/ssh"
	"context"
	"encoding/json"
	"testing"
	"time"
)

func TestFreshEmptyThreadReadsBeforeNativeHistoryMaterializes(t *testing.T) {
	fake := newFakeSSHSession()
	go func() {
		d, e := json.NewDecoder(fake.stdinR), json.NewEncoder(fake.stdoutW)
		for {
			var req map[string]any
			if d.Decode(&req) != nil {
				return
			}
			method := req["method"]
			if method == "initialized" {
				continue
			}
			result := map[string]any{}
			frame := map[string]any{"id": req["id"]}
			switch method {
			case "thread/start":
				result["thread"] = map[string]any{"id": "fresh", "turns": []any{}}
			case "thread/read":
				p := req["params"].(map[string]any)
				if p["includeTurns"] == true {
					frame["error"] = map[string]any{"code": -32601, "message": "list_turns is not supported yet"}
				} else {
					result["thread"] = map[string]any{"id": "fresh", "turns": []any{}, "status": map[string]any{"type": "idle"}}
				}
			case "thread/items/list":
				frame["error"] = map[string]any{"code": -32601, "message": "thread/items/list is not supported yet"}
			case "turn/start":
				frame["error"] = map[string]any{"code": -32000, "message": "reply lost after possible native commit"}
			}
			if frame["error"] == nil {
				frame["result"] = result
			}
			if e.Encode(frame) != nil {
				return
			}
		}
	}()
	a := NewSSHAdapter(Config{Executable: "/fixture/codex", Workspace: "/work", Allowed: []string{"/work"}, AgentID: "test"}, nil, func(context.Context, ssh.CommandSpec) (SSHSession, error) { return fake, nil })
	defer a.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	created, err := a.Create(ctx, core.Request{})
	if err != nil {
		t.Fatal(err)
	}
	for _, action := range []string{"session.history_page", "session.processes"} {
		got, err := a.Command(ctx, core.Request{SessionID: created.SessionID, Action: action, Fields: map[string]any{"limit": 20}})
		if err != nil {
			t.Fatalf("%s: %v", action, err)
		}
		key := "items"
		if action == "session.processes" {
			key = "processes"
		}
		items, ok := got.Payload[key].([]any)
		if !ok || len(items) != 0 {
			t.Fatal(got)
		}
	}
	// Even an ambiguous failed send retires the proof before the native write.
	s, _ := a.session(created.SessionID)
	if _, err = s.runtime.request(ctx, "turn/start", map[string]any{"threadId": created.SessionID, "input": []any{}}); err == nil {
		t.Fatal("expected ambiguous send failure")
	}
	for _, action := range []string{"session.history_page", "session.processes"} {
		if _, err := a.Command(ctx, core.Request{SessionID: created.SessionID, Action: action, Fields: map[string]any{"limit": 20}}); err == nil {
			t.Fatalf("%s reused empty proof after input", action)
		}
	}
}
