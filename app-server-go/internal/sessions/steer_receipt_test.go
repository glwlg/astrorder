package sessions

import (
	"context"
	"testing"

	pb "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
)

func TestConfirmedGuidanceReceiptCompletesOnlyFollowupCommand(t *testing.T) {
	srv := &mockDaemonServer{sendCb: func(r *pb.SendRequest) (*pb.CommandReceipt, error) {
		turn := "current-turn"
		return &pb.CommandReceipt{OperationState: pb.OperationState_OPERATION_STATE_COMPLETED, Accepted: true, TurnId: &turn, Session: r.Session, Status: pb.SessionStatus_SESSION_STATUS_RUNNING, Outcome: &pb.CommandReceipt_Completed{Completed: &pb.CommandResult{Accepted: true, TurnId: &turn, Native: &pb.NativeJson{Utf8Json: []byte(`{"accepted":true,"completed":true,"turn_id":"current-turn","status":"running"}`)}}}}, nil
	}}
	h, st, _, cleanup := setupTestServer(t, srv)
	defer cleanup()
	ctx := context.Background()
	if _, err := st.UpsertAgent(ctx, map[string]any{"id": "a", "kind": "codex", "name": "fixture", "status": "ready"}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.UpsertSession(ctx, map[string]any{"id": "s", "agent_id": "a", "status": "running", "control_state": "owned", "updated_at": "2026-10-14T00:00:00Z"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.CreateCommand(ctx, map[string]any{"id": "original", "session_id": "s", "agent_id": "a", "action": "send", "text": "long task"}, "running"); err != nil {
		t.Fatal(err)
	}
	result, err := h.Submit(ctx, map[string]any{"id": "guidance", "session_id": "s", "agent_id": "a", "action": "send", "text": "follow-up"})
	if err != nil || result["state"] != "completed" {
		t.Fatalf("confirmed guidance left nonterminal: %+v %v", result, err)
	}
	original, err := st.GetCommand(ctx, "a", "s", "original")
	if err != nil || original["state"] != "running" {
		t.Fatalf("guidance completed original turn: %+v %v", original, err)
	}
	current, err := st.GetSession(ctx, "a", "s")
	if err != nil || current["status"] != "running" {
		t.Fatalf("guidance changed session status: %+v %v", current, err)
	}
}
