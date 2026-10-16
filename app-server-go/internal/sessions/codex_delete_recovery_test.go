package sessions

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	pb "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestCodexDeleteReattachesExitedNativeOwner(t *testing.T) {
	attached, deleted := false, false
	srv := &mockDaemonServer{
		readModelCb: func(*pb.ReadModelRequest) (*pb.ModelState, error) {
			if !attached {
				return nil, status.Error(codes.NotFound, "confirmed exited owner")
			}
			return &pb.ModelState{}, nil
		},
		attachCb: func(r *pb.AttachSessionRequest) (*pb.SessionReceipt, error) {
			if r.Session.SessionId != "s" || r.Session.GetAgentId() != "a" || r.GetCwd() != "C:/work" || r.RuntimeControl {
				t.Errorf("wrong recovery identity: %+v", r)
			}
			attached = true
			return &pb.SessionReceipt{Session: r.Session, Attached: true, Status: pb.SessionStatus_SESSION_STATUS_IDLE}, nil
		},
		deleteCb: func(r *pb.DeleteSessionRequest) (*pb.CommandReceipt, error) {
			if !attached {
				return nil, status.Error(codes.Unavailable, "codex runtime is closed")
			}
			deleted = true
			return &pb.CommandReceipt{OperationState: pb.OperationState_OPERATION_STATE_COMPLETED, Outcome: &pb.CommandReceipt_Completed{Completed: &pb.CommandResult{Accepted: true}}}, nil
		},
	}
	h, st, _, cleanup := setupTestServer(t, srv)
	defer cleanup()
	ctx := context.Background()
	_, err := st.UpsertAgent(ctx, map[string]any{"id": "a", "kind": "codex", "name": "fixture", "status": "ready"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = st.UpsertSession(ctx, map[string]any{"id": "s", "agent_id": "a", "status": "running", "workspace": "C:/work", "updated_at": "2026-10-15T00:00:00Z"})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/sessions/s?agent_id=a", nil)
	req.Header.Set("Authorization", "Bearer browser-sec")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	row, err := st.GetSession(ctx, "a", "s")
	if w.Code != 200 || !attached || !deleted || row != nil || err != nil {
		t.Fatalf("recovery/delete failed: %d %s attached=%v deleted=%v row=%v err=%v", w.Code, w.Body.String(), attached, deleted, row, err)
	}
}
