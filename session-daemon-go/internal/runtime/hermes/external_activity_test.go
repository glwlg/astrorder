package hermes

import "testing"

func TestCanonicalExternalTurnStartUpdatesActivity(t *testing.T) {
	var events []string
	a := New(Config{Emit: func(_ string, event string, _ map[string]any, _ string) error {
		events = append(events, event)
		return nil
	}})
	a.canonical = true
	a.sessions["s"] = &SessionState{SessionID: "s", Handle: "s", Status: "idle", Metadata: map[string]any{}}
	a.handleGatewayEvent(map[string]any{"type": "message.start", "session_id": "s", "payload": map[string]any{}})
	if a.sessions["s"].Status != "running" || len(events) != 1 || events[0] != "session.active" {
		t.Fatalf("external turn was invisible: status=%s events=%v", a.sessions["s"].Status, events)
	}
}
