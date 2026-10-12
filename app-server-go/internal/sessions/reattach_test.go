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

func TestHistoryReattachesMissingHermesOwnerAfterDaemonRestart(t *testing.T) {
	attached := false
	calls := 0
	srv := &mockDaemonServer{
		readModelCb: func(r *pb.ReadModelRequest) (*pb.ModelState, error) {
			if !attached {
				return nil, status.Error(codes.NotFound, "owner lost")
			}
			return &pb.ModelState{}, nil
		},
		attachCb: func(r *pb.AttachSessionRequest) (*pb.SessionReceipt, error) {
			calls++
			if r.Session.SessionId != "native" || r.Target.GetAgentId() != "a" || r.GetCwd() != "C:/work" || r.RuntimeControl {
				t.Errorf("incorrect resume: %+v", r)
			}
			attached = true
			return &pb.SessionReceipt{Session: r.Session, Attached: true, Status: pb.SessionStatus_SESSION_STATUS_IDLE}, nil
		},
	}
	h, st, _, cleanup := setupTestServer(t, srv)
	defer cleanup()
	ctx := context.Background()
	if _, err := st.UpsertAgent(ctx, map[string]any{"id": "a", "kind": "hermes", "name": "test", "status": "ready"}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.UpsertSession(ctx, map[string]any{"id": "native", "agent_id": "a", "title": "resume", "workspace": "C:/work", "status": "idle", "control_state": "owned", "history_state": "available", "updated_at": "2026-10-09T00:00:00Z"}); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/sessions/native/messages?agent_id=a", nil)
	req.Header.Set("Authorization", "Bearer browser-sec")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != 200 || !attached || calls != 1 {
		t.Fatalf("history failed to resume: %d %s attach=%d", w.Code, w.Body.String(), calls)
	}
}
