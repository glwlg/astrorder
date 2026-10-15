package hermes

import "testing"

func TestControlUpdatesForwardGoalsWithoutEndingCommand(t *testing.T) {
	var events []map[string]any
	a := New(Config{AgentID: "a", Emit: func(sid, kind string, data map[string]any, status string) error {
		if sid != "s" || kind != "hermes.notification" || status != "running" {
			t.Errorf("wrong envelope: %s %s %s", sid, kind, status)
		}
		events = append(events, data)
		return nil
	}})
	a.sessions["s"] = &SessionState{SessionID: "s", Handle: "handle", CommandID: "exact", Status: "running"}
	a.handleToSession["handle"] = "s"
	for _, goal := range []any{map[string]any{"title": "Build", "status": "active", "turns_used": 2, "max_turns": 20}, nil} {
		a.handleGatewayEvent(map[string]any{"type": "session.control.update", "session_id": "handle", "payload": map[string]any{"control": map[string]any{"goal": goal, "revision": "r"}}})
	}
	a.handleGatewayEvent(map[string]any{"type": "session.control.update", "session_id": "foreign", "payload": map[string]any{"control": map[string]any{"goal": nil}}})
	if len(events) != 2 {
		t.Fatalf("goal updates lost or foreign session leaked: %+v", events)
	}
	for _, event := range events {
		if event["command_id"] != "exact" || event["session_id"] != "s" {
			t.Fatalf("lost identity: %+v", event)
		}
	}
	if a.sessions["s"].Status != "running" || a.sessions["s"].CommandID != "exact" {
		t.Fatal("goal update ended command")
	}
}
