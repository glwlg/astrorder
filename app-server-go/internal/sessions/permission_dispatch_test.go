package sessions

import (
	"astrorder.dev/app-server/internal/store"
	pb "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
	"context"
	"encoding/json"
	"testing"
)

var capturedApprovalRequest *pb.RespondApprovalRequest

func (m *mockDaemonServer) RespondApproval(ctx context.Context, r *pb.RespondApprovalRequest) (*pb.CommandReceipt, error) {
	capturedApprovalRequest = r
	return &pb.CommandReceipt{OperationState: pb.OperationState_OPERATION_STATE_COMPLETED, Accepted: true, Session: r.Session, Outcome: &pb.CommandReceipt_Completed{Completed: &pb.CommandResult{Accepted: true}}}, nil
}
func TestPermissionApprovalDispatchesExactRequestedGrantsOnce(t *testing.T) {
	capturedApprovalRequest = nil
	h, st, _, cleanup := setupTestServer(t, &mockDaemonServer{})
	defer cleanup()
	ctx := context.Background()
	if _, err := st.UpsertAgent(ctx, map[string]any{"id": "a", "kind": "codex", "name": "fixture", "status": "ready"}); err != nil {
		t.Fatal(err)
	}
	seedProjectSession(t, h, "a", "s", "one", "p")
	if _, err := st.AppendEvent(ctx, store.Event{ID: "permission", Type: "approval.upsert", AgentID: "a", SessionID: "s", Data: map[string]any{"id": "exact-permission", "state": "pending", "method": "item/permissions/requestApproval", "permissions": map[string]any{"network": map[string]any{"enabled": true}, "fileSystem": map[string]any{"write": []any{"C:/fixture"}}}}}); err != nil {
		t.Fatal(err)
	}
	payload := map[string]any{"id": "exact-command", "agent_id": "a", "session_id": "s", "action": "approve", "target_id": "exact-permission"}
	for i := 0; i < 2; i++ {
		if _, err := h.Submit(ctx, payload); err != nil {
			t.Fatal(err)
		}
	}
	if capturedApprovalRequest == nil || len(capturedApprovalRequest.Permissions) != 2 || capturedApprovalRequest.ApprovalId.GetText() != "exact-permission" {
		t.Fatalf("permission grants missing: %v", capturedApprovalRequest)
	}
	var filesystem map[string]any
	for _, grant := range capturedApprovalRequest.Permissions {
		if grant.Name == "fileSystem" {
			if err := json.Unmarshal(grant.Native.Utf8Json, &filesystem); err != nil {
				t.Fatal(err)
			}
		}
	}
	if filesystem["write"].([]any)[0] != "C:/fixture" {
		t.Fatal("grant value changed")
	}
}
