package hermes

import "testing"

func TestCanonicalApprovalNotificationsStaySessionScoped(t *testing.T) {
	a := New(Config{})
	a.canonical = true
	a.sessions["owned"] = &SessionState{SessionID: "owned", Status: "running", Metadata: map[string]any{}}
	a.handleGatewayEvent(map[string]any{"type": "approval.request", "session_id": "foreign", "payload": map[string]any{"prompt_id": "bad"}})
	if len(a.pendingApprovals) != 0 {
		t.Fatal("foreign approval accepted")
	}
	a.handleGatewayEvent(map[string]any{"type": "approval.request", "session_id": "owned", "payload": map[string]any{"prompt_id": "prompt-1", "command": "echo fixture"}})
	if a.pendingApprovals["prompt-1"] == nil || a.sessions["owned"].Status != "waiting_approval" {
		t.Fatal("canonical approval lost")
	}
	a.handleGatewayEvent(map[string]any{"type": "approval.settled", "session_id": "owned", "payload": map[string]any{"prompt_id": "prompt-1"}})
	if len(a.pendingApprovals) != 0 {
		t.Fatal("settled approval retained")
	}
}
