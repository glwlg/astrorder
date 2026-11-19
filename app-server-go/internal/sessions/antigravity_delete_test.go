package sessions

import (
	pb "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
	"context"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"net/http/httptest"
	"testing"
)

func TestAntigravityDeleteRequiresConfirmedNativeReceipt(t *testing.T) {
	for _, confirmed := range []bool{false, true} {
		t.Run(map[bool]string{false: "unconfirmed", true: "confirmed"}[confirmed], func(t *testing.T) {
			called := false
			srv := &mockDaemonServer{deleteCb: func(r *pb.DeleteSessionRequest) (*pb.CommandReceipt, error) {
				called = true
				if r.Session.SessionId != "s" || r.Session.GetAgentId() != "a" {
					t.Fatal("wrong identity")
				}
				if !confirmed {
					return nil, status.Error(codes.NotFound, "session is not daemon-owned")
				}
				return &pb.CommandReceipt{OperationState: pb.OperationState_OPERATION_STATE_COMPLETED, Outcome: &pb.CommandReceipt_Completed{Completed: &pb.CommandResult{Accepted: true}}}, nil
			}}
			h, st, _, cleanup := setupTestServer(t, srv)
			defer cleanup()
			ctx := context.Background()
			st.UpsertAgent(ctx, map[string]any{"id": "a", "kind": "antigravity", "name": "fixture", "status": "ready"})
			st.UpsertSession(ctx, map[string]any{"id": "s", "agent_id": "a", "status": "idle", "control_state": "owned", "updated_at": "2026-10-18T00:00:00Z"})
			req := httptest.NewRequest("DELETE", "/api/v1/sessions/s?agent_id=a", nil)
			req.Header.Set("Authorization", "Bearer browser-sec")
			w := httptest.NewRecorder()
			h.ServeHTTP(w, req)
			row, _ := st.GetSession(ctx, "a", "s")
			if !called || (w.Code == 200) != confirmed || (row == nil) != confirmed {
				t.Fatalf("delete code=%d called=%v row=%v", w.Code, called, row)
			}
		})
	}
}

func TestAntigravityUnboundDeleteAttachesDeleteOnly(t *testing.T) {
	var sawDeleteOnly bool
	srv := &mockDaemonServer{
		readModelCb: func(*pb.ReadModelRequest) (*pb.ModelState, error) {
			return nil, status.Error(codes.NotFound, "session is not daemon-owned")
		},
		attachCb: func(r *pb.AttachSessionRequest) (*pb.SessionReceipt, error) {
			var opts map[string]any
			if r.NativeOptions != nil && parseNativeJSON(r.NativeOptions, &opts) == nil && opts["delete_only"] == true {
				sawDeleteOnly = true
			}
			return &pb.SessionReceipt{Attached: true, Session: r.Session, Status: pb.SessionStatus_SESSION_STATUS_IDLE}, nil
		},
		deleteCb: func(*pb.DeleteSessionRequest) (*pb.CommandReceipt, error) {
			return &pb.CommandReceipt{
				OperationState: pb.OperationState_OPERATION_STATE_COMPLETED,
				Outcome:        &pb.CommandReceipt_Completed{Completed: &pb.CommandResult{Accepted: true}},
			}, nil
		},
	}
	h, st, _, cleanup := setupTestServer(t, srv)
	defer cleanup()
	ctx := context.Background()
	_, _ = st.UpsertAgent(ctx, map[string]any{"id": "a", "kind": "antigravity", "name": "fixture", "status": "ready"})
	_, _ = st.UpsertSession(ctx, map[string]any{"id": "11111111-1111-4111-8111-111111111111", "agent_id": "a", "status": "idle", "control_state": "unbound", "updated_at": "2026-10-18T00:00:00Z"})

	req := httptest.NewRequest("DELETE", "/api/v1/sessions/11111111-1111-4111-8111-111111111111?agent_id=a", nil)
	req.Header.Set("Authorization", "Bearer browser-sec")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != 200 || !sawDeleteOnly {
		t.Fatalf("expected 200 with delete_only attach, got code=%d sawDeleteOnly=%v body=%s", w.Code, sawDeleteOnly, w.Body.String())
	}
}
