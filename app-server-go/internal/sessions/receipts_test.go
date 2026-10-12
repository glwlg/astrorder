package sessions

import (
	pb "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRejectedDeletePreservesSession(t *testing.T) {
	srv := &mockDaemonServer{deleteCb: func(r *pb.DeleteSessionRequest) (*pb.CommandReceipt, error) {
		return &pb.CommandReceipt{OperationState: pb.OperationState_OPERATION_STATE_COMPLETED, Outcome: &pb.CommandReceipt_Rejected{Rejected: &pb.BusinessError{Message: "still running"}}}, nil
	}}
	h, st, _, cleanup := setupTestServer(t, srv)
	defer cleanup()
	ctx := context.Background()
	if _, err := st.UpsertSession(ctx, map[string]any{"id": "s", "agent_id": "a", "title": "keep", "status": "running", "updated_at": "2026-10-09T00:00:00Z"}); err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodDelete, "/api/v1/sessions/s?agent_id=a", nil)
	r.Header.Set("Authorization", "Bearer browser-sec")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	row, err := st.GetSession(ctx, "a", "s")
	if err != nil || row == nil || w.Code == 200 {
		t.Fatalf("rejected delete removed session: status=%d row=%+v err=%v", w.Code, row, err)
	}
}
func TestCreateRejectsForeignReceiptOwner(t *testing.T) {
	owner := "foreign"
	srv := &mockDaemonServer{createCb: func(r *pb.CreateSessionRequest) (*pb.CommandReceipt, error) {
		return &pb.CommandReceipt{Accepted: true, OperationState: pb.OperationState_OPERATION_STATE_COMPLETED, Session: &pb.SessionRef{AgentId: &owner, SessionId: "foreign-native"}, Status: pb.SessionStatus_SESSION_STATUS_IDLE, Outcome: &pb.CommandReceipt_Completed{Completed: &pb.CommandResult{Accepted: true}}}, nil
	}}
	h, st, _, cleanup := setupTestServer(t, srv)
	defer cleanup()
	ctx := context.Background()
	if _, err := st.UpsertAgent(ctx, map[string]any{"id": "a", "kind": "codex", "name": "fixture", "status": "ready"}); err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodPost, "/api/v1/sessions", strings.NewReader(`{"agent_id":"a","workspace":"C:/work"}`))
	r.Header.Set("Authorization", "Bearer browser-sec")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	rows, err := st.ListSessions(ctx, "")
	if err != nil || len(rows) != 0 || w.Code == 200 {
		t.Fatalf("foreign owner persisted: status=%d rows=%+v err=%v", w.Code, rows, err)
	}
}
