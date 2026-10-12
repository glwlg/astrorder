package hermes

import "testing"

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
