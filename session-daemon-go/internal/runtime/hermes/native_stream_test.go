package hermes

import "testing"

func TestNativeToolLifecyclePreservesIdentity(t *testing.T) {
	var events []map[string]any
	a := New(Config{AgentID: "a", Emit: func(sid, kind string, data map[string]any, status string) error {
		if kind != "hermes.notification" {
			t.Fatalf("unexpected event %s", kind)
		}
		events = append(events, data)
		return nil
	}})
	a.sessions["s"] = &SessionState{SessionID: "s", CommandID: "exact", Status: "running"}
	for _, kind := range []string{"tool.start", "tool.complete"} {
		a.handleGatewayEvent(map[string]any{"type": kind, "session_id": "s", "payload": map[string]any{"tool_id": "call-1", "name": "terminal", "args": map[string]any{"command": "test"}}})
	}
	if len(events) != 2 {
		t.Fatalf("tool lifecycle lost: %d events", len(events))
	}
	for _, event := range events {
		if event["command_id"] != "exact" || event["session_id"] != "s" {
			t.Fatalf("lost binding: %+v", event)
		}
	}
	if a.sessions["s"].Status != "running" {
		t.Fatal("tool completion ended the turn")
	}
}

func TestNativeMessageDeltaCarriesSubmittedCommand(t *testing.T) {
	var event string
	var payload map[string]any
	a := New(Config{AgentID: "a", Emit: func(sid, kind string, data map[string]any, status string) error {
		event, payload = kind, data
		return nil
	}})
	a.sessions["s"] = &SessionState{SessionID: "s", CommandID: "exact", Status: "running", Metadata: map[string]any{}}
	a.handleGatewayEvent(map[string]any{"type": "message.delta", "session_id": "s", "payload": map[string]any{"text": "native delta"}})
	if event != "hermes.stream" || payload["command_id"] != "exact" || payload["delta"] != "native delta" || payload["agent_id"] != "a" || payload["session_id"] != "s" {
		t.Fatalf("native delta was lost: %s %+v", event, payload)
	}
}
