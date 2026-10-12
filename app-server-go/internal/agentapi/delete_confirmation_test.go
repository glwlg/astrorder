package agentapi

import (
	"astrorder.dev/app-server/internal/store"
	pb "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
	"context"
	"path/filepath"
	"testing"
)

func TestNativeDeleteRequiresCompletedOutcome(t *testing.T) {
	for _, state := range []pb.OperationState{pb.OperationState_OPERATION_STATE_PENDING, pb.OperationState_OPERATION_STATE_COMPLETED} {
		t.Run(state.String(), func(t *testing.T) {
			st, err := store.Open(filepath.Join(t.TempDir(), "app.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer st.Close()
			ctx := context.Background()
			if _, err := st.UpsertSession(ctx, map[string]any{"agent_id": "a", "id": "s", "status": "idle", "title": "fixture", "updated_at": "2026-10-09T00:00:00Z"}); err != nil {
				t.Fatal(err)
			}
			srv := &mockSessionControlServer{deleteResp: &pb.CommandReceipt{Accepted: true, OperationState: state}}
			client, cleanup := setupMockGRPC(t, srv)
			defer cleanup()
			h := &Handler{options: Options{Store: st, Client: client}}
			result, err := h.Invoke(ctx, "sessions.delete", map[string]any{"agent_id": "a", "session_id": "s"})
			if err != nil {
				t.Fatal(err)
			}
			row, err := st.GetSession(ctx, "a", "s")
			if err != nil || row == nil || result.(map[string]any)["ok"] != false {
				t.Fatalf("unconfirmed delete removed row: result=%v exists=%v error=%v", result, row != nil, err)
			}
		})
	}
}
