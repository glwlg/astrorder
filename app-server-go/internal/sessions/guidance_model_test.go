package sessions

import (
	"context"
	"google.golang.org/grpc"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	pb "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
)

type guidanceModelServer struct {
	modelRestoreServer
	snapshot *pb.SessionSnapshot
}

func (s *guidanceModelServer) GetCapabilities(ctx context.Context, r *pb.GetCapabilitiesRequest) (*pb.GetCapabilitiesResponse, error) {
	response, err := s.mockDaemonServer.GetCapabilities(ctx, r)
	if response != nil {
		response.ProtocolMinor = 1
	}
	return response, err
}

func (s *guidanceModelServer) GetStatus(context.Context, *pb.GetStatusRequest) (*pb.GetStatusResponse, error) {
	return &pb.GetStatusResponse{DaemonId: "test-daemon", Sessions: []*pb.SessionSnapshot{s.snapshot}}, nil
}

func startGuidanceStatusTarget(t *testing.T, srv *guidanceModelServer) (string, func()) {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := grpc.NewServer()
	pb.RegisterDaemonControlServer(server, srv)
	pb.RegisterSessionControlServer(server, srv)
	go func() { _ = server.Serve(lis) }()
	return lis.Addr().String(), func() { server.Stop(); _ = lis.Close() }
}

func TestRunningGuidanceDoesNotRestoreSavedModel(t *testing.T) {
	for _, tc := range []struct {
		name      string
		projected string
		live      pb.SessionStatus
		agent     string
		owned     bool
		wantSet   int
	}{
		{"running", "running", pb.SessionStatus_SESSION_STATUS_RUNNING, "a", true, 0},
		{"stale idle projection", "idle", pb.SessionStatus_SESSION_STATUS_RUNNING, "a", true, 0},
		{"stale running projection", "running", pb.SessionStatus_SESSION_STATUS_IDLE, "a", true, 1},
		{"foreign owner", "running", pb.SessionStatus_SESSION_STATUS_RUNNING, "other", true, 1},
		{"journal only", "running", pb.SessionStatus_SESSION_STATUS_RUNNING, "a", false, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			kind := pb.RuntimeKind_RUNTIME_KIND_HERMES
			srv := &guidanceModelServer{modelRestoreServer: modelRestoreServer{provider: "custom", model: "codex/gpt-6.1-sol", setReadback: "codex/gpt-6.1-sol"}, snapshot: &pb.SessionSnapshot{Session: &pb.SessionRef{SessionId: "s", AgentId: &tc.agent}, Status: tc.live, RuntimeOwned: &tc.owned, Runtime: &kind}}
			h, st, _, cleanup := setupTestServer(t, &srv.mockDaemonServer)
			defer cleanup()
			srv.sendCb = func(request *pb.SendRequest) (*pb.CommandReceipt, error) {
				commandSendCalls++
				if tc.wantSet == 0 && string(request.GetPolicy().GetNativeOptions().GetUtf8Json()) != `{"guidance_only":true}` {
					t.Error("guidance lost strict running-only policy")
				}
				return &pb.CommandReceipt{OperationState: pb.OperationState_OPERATION_STATE_COMPLETED, Accepted: true, Outcome: &pb.CommandReceipt_Completed{Completed: &pb.CommandResult{Accepted: true}}}, nil
			}
			addr, stop := startModelRestoreTarget(t, &srv.modelRestoreServer)
			defer stop()
			client := dialModelRestoreTarget(t, addr)
			defer client.Close()
			h.client.Runtime = client.Runtime
			// The status fixture must expose the exact same composite owner independently of the App projection.
			statusAddr, statusStop := startGuidanceStatusTarget(t, srv)
			defer statusStop()
			statusClient := dialModelRestoreTarget(t, statusAddr)
			defer statusClient.Close()
			h.client.Daemon = statusClient.Daemon
			ctx := context.Background()
			if _, err := st.UpsertAgent(ctx, map[string]any{"id": "a", "kind": "hermes", "name": "fixture", "status": "ready"}); err != nil {
				t.Fatal(err)
			}
			if _, err := st.UpsertSession(ctx, map[string]any{"id": "s", "agent_id": "a", "title": "fixture", "status": tc.projected, "control_state": "owned", "updated_at": "2026-10-15T00:00:00Z"}); err != nil {
				t.Fatal(err)
			}
			if _, err := st.SetSessionModelBinding(ctx, "a", "s", "magpie", "codex/gpt-6.1-sol"); err != nil {
				t.Fatal(err)
			}
			if tc.wantSet == 0 {
				if _, err := st.SetSessionReasoningBinding(ctx, "a", "s", "high"); err != nil {
					t.Fatal(err)
				}
			}
			commandSendCalls = 0
			w := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodPost, "/api/v1/commands", strings.NewReader(`{"id":"guidance","agent_id":"a","session_id":"s","action":"send","text":"followup"}`))
			r.Header.Set("Authorization", "Bearer browser-sec")
			h.ServeHTTP(w, r)
			if w.Code != 200 || srv.setCalls != tc.wantSet || commandSendCalls != 1 {
				t.Fatalf("status=%d sets=%d want=%d sends=%d body=%s", w.Code, srv.setCalls, tc.wantSet, commandSendCalls, w.Body.String())
			}
			binding, err := st.GetSessionModelBinding(ctx, "a", "s")
			if err != nil || binding["provider"] != "magpie" {
				t.Fatalf("saved binding changed: %v %v", binding, err)
			}
		})
	}
}
