package sessions

import (
	pb "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
	"context"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAntigravityDeadOwnerReattachesExactUUIDBeforeHistory(t *testing.T) {
	const sid = "11111111-1111-4111-8111-111111111111"
	attached := false
	attaches := 0
	sends := 0
	srv := &mockDaemonServer{
		readModelCb: func(*pb.ReadModelRequest) (*pb.ModelState, error) {
			if !attached {
				return nil, status.Error(codes.NotFound, "transport exited")
			}
			return &pb.ModelState{}, nil
		},
		attachCb: func(r *pb.AttachSessionRequest) (*pb.SessionReceipt, error) {
			attaches++
			if r.Session.SessionId != sid || r.Target.GetRuntime() != pb.RuntimeKind_RUNTIME_KIND_ANTIGRAVITY || r.Target.GetAgentId() != "agy" || r.GetCwd() != "P:/work" || r.RuntimeControl {
				t.Errorf("incorrect reattachment: %+v", r)
			}
			attached = true
			return &pb.SessionReceipt{Session: r.Session, Attached: true, Status: pb.SessionStatus_SESSION_STATUS_IDLE}, nil
		},
		sendCb: func(*pb.SendRequest) (*pb.CommandReceipt, error) {
			sends++
			return nil, status.Error(codes.Internal, "must not send during recovery")
		},
	}
	h, st, _, cleanup := setupTestServer(t, srv)
	defer cleanup()
	ctx := context.Background()
	if _, err := st.UpsertAgent(ctx, map[string]any{"id": "agy", "kind": "antigravity", "name": "Antigravity", "status": "ready"}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.UpsertSession(ctx, map[string]any{"id": sid, "agent_id": "agy", "workspace": "P:/work", "status": "error", "control_state": "owned", "history_state": "available", "updated_at": "2026-10-18T00:00:00Z"}); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/sessions/"+sid+"/messages?agent_id=agy", nil)
	req.Header.Set("Authorization", "Bearer browser-sec")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != 200 || attaches != 1 || sends != 0 {
		t.Fatalf("recovery: code=%d attaches=%d sends=%d body=%s", w.Code, attaches, sends, w.Body.String())
	}
}
