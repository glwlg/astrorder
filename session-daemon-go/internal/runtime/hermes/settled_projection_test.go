package hermes

import "testing"

func TestCanonicalSettledApprovalPublishesResolution(t *testing.T) {
	var events []string
	var resolved map[string]any
	a := New(Config{AgentID: "a", Emit: func(sid, event string, payload map[string]any, status string) error {
		events = append(events, event)
		if event == "hermes.approval_resolved" {
			resolved = payload
			if sid != "s" || status != "running" {
				t.Errorf("resolution status %s/%s", sid, status)
			}
		}
		return nil
	}})
	a.canonical = true
	a.sessions["s"] = &SessionState{SessionID: "s", Status: "running", Metadata: map[string]any{}}
	a.handleGatewayEvent(map[string]any{"type": "approval.request", "session_id": "s", "payload": map[string]any{"prompt_id": "p"}})
	a.handleGatewayEvent(map[string]any{"type": "approval.settled", "session_id": "s", "payload": map[string]any{"prompt_id": "p"}})
	if resolved == nil || resolved["approval_id"] != "p" || resolved["state"] != "resolved" {
		t.Fatalf("native settlement never projected: %v %v", events, resolved)
	}
	if a.sessions["s"].Status != "running" {
		t.Fatalf("settled approval left session waiting: %s", a.sessions["s"].Status)
	}
}
