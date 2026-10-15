package agentapi

import (
	"astrorder.dev/app-server/internal/store"
	pb "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
	"context"
	"path/filepath"
	"testing"
)

func TestCreateUsesCompletedReceiptRatherThanSendAcceptance(t *testing.T) {
	for _, complete := range []bool{true, false} {
		t.Run(map[bool]string{true: "completed", false: "uncertain"}[complete], func(t *testing.T) {
			st, err := store.Open(filepath.Join(t.TempDir(), "app.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer st.Close()
			if _, err = st.UpsertAgent(context.Background(), map[string]any{"id": "a", "kind": "codex", "name": "a", "status": "ready"}); err != nil {
				t.Fatal(err)
			}
			receipt := &pb.CommandReceipt{Session: &pb.SessionRef{SessionId: "created", AgentId: strptr("a")}, Status: pb.SessionStatus_SESSION_STATUS_IDLE, OperationState: pb.OperationState_OPERATION_STATE_UNCERTAIN}
			if complete {
				receipt.OperationState = pb.OperationState_OPERATION_STATE_COMPLETED
				receipt.Outcome = &pb.CommandReceipt_Completed{Completed: &pb.CommandResult{}}
			}
			client, closeClient := setupMockGRPC(t, &mockSessionControlServer{createResp: receipt})
			defer closeClient()
			h := &Handler{options: Options{Store: st, Client: client, DefaultWorkspace: t.TempDir()}}
			_, err = h.invokeSessionsCreate(context.Background(), map[string]any{"agent_id": "a"})
			if (err == nil) != complete {
				t.Fatalf("completed=%v err=%v", complete, err)
			}
		})
	}
}
