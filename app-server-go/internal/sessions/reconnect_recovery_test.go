package sessions

import (
	"context"
	"testing"

	pb "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestReconnectRecoversOnlyFailedSessionsOnExactAgentConnection(t *testing.T) {
	ctx := context.Background()
	attached := false
	calls := 0
	srv := &mockDaemonServer{
		readModelCb: func(req *pb.ReadModelRequest) (*pb.ModelState, error) {
			if req.Session.GetAgentId() != "a" || req.Session.SessionId != "native" || req.Session.GetConnectionId() != "wsl" {
				t.Errorf("wrong model identity: %v", req.Session)
			}
			if !attached {
				return nil, status.Error(codes.Unavailable, "transport exited")
			}
			return &pb.ModelState{}, nil
		},
		attachCb: func(req *pb.AttachSessionRequest) (*pb.SessionReceipt, error) {
			calls++
			if req.Session.SessionId != "native" || req.GetCwd() != "/work" || req.RuntimeControl {
				t.Errorf("wrong reattach: %v", req)
			}
			attached = true
			return &pb.SessionReceipt{DaemonId: "test-daemon", Attached: true, Session: req.Session, Status: pb.SessionStatus_SESSION_STATUS_IDLE}, nil
		},
		statusCb: func() (*pb.GetStatusResponse, error) {
			snapshots := []*pb.SessionSnapshot{}
			if attached {
				snapshots = append(snapshots, refreshOwnedSnapshot("native", "a", refreshStringPtr("wsl"), pb.RuntimeKind_RUNTIME_KIND_CODEX_SSH, pb.SessionStatus_SESSION_STATUS_IDLE))
			}
			return &pb.GetStatusResponse{DaemonId: "test-daemon", Sessions: snapshots}, nil
		},
	}
	h, st, _, cleanup := setupTestServer(t, srv)
	defer cleanup()
	if _, err := st.SaveSSHConnection(ctx, map[string]any{"id": "wsl", "host": "fixture.invalid", "workspace": "/work", "codex_executable": "/fixture/codex"}); err != nil {
		t.Fatal(err)
	}
	for _, a := range []struct{ id, kind, conn string }{{"a", "codex", "wsl"}, {"other", "codex", "debian"}, {"sibling", "hermes", "wsl"}} {
		seedRefreshAgent(t, st, a.id, a.kind, a.conn)
	}
	seedRefreshSession(t, st, "a", "display", map[string]any{"source_session_id": "native", "connection_id": "wsl", "status": "error", "workspace": "/work"})
	for _, state := range []string{"running", "waiting_approval", "idle"} {
		seedRefreshSession(t, st, "a", state, map[string]any{"connection_id": "wsl", "status": state})
	}
	seedRefreshSession(t, st, "a", "readonly", map[string]any{"connection_id": "wsl", "status": "error", "control_state": "readonly"})
	seedRefreshSession(t, st, "other", "foreign", map[string]any{"connection_id": "debian", "status": "error"})
	seedRefreshSession(t, st, "sibling", "sibling", map[string]any{"connection_id": "wsl", "status": "error"})
	before, _ := st.GetSession(ctx, "a", "display")
	if err := h.RecoverDisconnectedSessions(ctx, "wsl", "codex"); err != nil {
		t.Fatal(err)
	}
	after, _ := st.GetSession(ctx, "a", "display")
	if calls != 1 || after["status"] != "idle" || after["updated_at"] != before["updated_at"] {
		t.Fatalf("recovery did not refresh exact session without changing activity: calls=%d before=%v after=%v", calls, before, after)
	}
}
