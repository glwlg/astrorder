package httpapi

import (
	"context"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"astrorder.dev/app-server/internal/daemonclient"
	"astrorder.dev/app-server/internal/store"
	pb "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
	"google.golang.org/grpc"
)

type fakeRemoteExecDaemon struct {
	pb.UnimplementedDaemonControlServer
	pb.UnimplementedSessionControlServer
	pb.UnimplementedRuntimeQueryServer

	mu                sync.Mutex
	seenAntigravityEx []string
}

func (d *fakeRemoteExecDaemon) GetCapabilities(context.Context, *pb.GetCapabilitiesRequest) (*pb.GetCapabilitiesResponse, error) {
	return &pb.GetCapabilitiesResponse{
		ProtocolMajor:            1,
		ProtocolMinor:            0,
		DaemonId:                 "daemon-httpapi-test",
		Services:                 []string{"astrorder.sessiond.v1.DaemonControl", "astrorder.sessiond.v1.SessionControl", "astrorder.sessiond.v1.RuntimeQuery", "astrorder.sessiond.v1.OperationQuery", "astrorder.sessiond.v1.ModelConfig", "astrorder.sessiond.v1.EventStream", "astrorder.sessiond.v1.TerminalStream", "astrorder.sessiond.v1.ConnectorIngress"},
		CanonicalizationVersions: []pb.CanonicalizationVersion{pb.CanonicalizationVersion_CANONICALIZATION_VERSION_LEGACY_JSON_V1},
		MaxBusinessBytes:         pb.ContractLimit_CONTRACT_LIMIT_MAX_COMPAT_BUSINESS_BYTES,
		MaxWireBytes:             pb.ContractLimit_CONTRACT_LIMIT_MAX_GRPC_WIRE_BYTES,
		EventStreamVersion:       pb.EventStreamVersion_EVENT_STREAM_VERSION_V1,
		OperationLookup:          true,
	}, nil
}

func (d *fakeRemoteExecDaemon) GetStatus(context.Context, *pb.GetStatusRequest) (*pb.GetStatusResponse, error) {
	return &pb.GetStatusResponse{DaemonId: "daemon-httpapi-test"}, nil
}

func (d *fakeRemoteExecDaemon) QueryNative(_ context.Context, req *pb.NativeQueryRequest) (*pb.NativeQueryResponse, error) {
	if req.GetTarget() != nil && req.GetTarget().GetSsh() != nil {
		d.mu.Lock()
		d.seenAntigravityEx = append(d.seenAntigravityEx, req.GetTarget().GetSsh().GetAntigravityExecutable())
		d.mu.Unlock()
	}
	return &pb.NativeQueryResponse{
		Target: req.GetTarget(),
		Result: &pb.NativeJson{Utf8Json: []byte(`{"items":[]}`)},
	}, nil
}

func (d *fakeRemoteExecDaemon) CreateSession(_ context.Context, req *pb.CreateSessionRequest) (*pb.CommandReceipt, error) {
	d.mu.Lock()
	d.seenAntigravityEx = append(d.seenAntigravityEx, req.GetTarget().GetSsh().GetAntigravityExecutable())
	n := len(d.seenAntigravityEx)
	d.mu.Unlock()

	aid := "ssh-antigravity-wsl-dev"
	cid := req.GetTarget().GetConnectionId()
	sid := "00000000-0000-4000-8000-00000000000" + string(rune('0'+n))
	return &pb.CommandReceipt{
		Accepted:       true,
		OperationState: pb.OperationState_OPERATION_STATE_COMPLETED,
		Session:        &pb.SessionRef{SessionId: sid, AgentId: &aid, ConnectionId: &cid},
		Status:         pb.SessionStatus_SESSION_STATUS_IDLE,
		Outcome:        &pb.CommandReceipt_Completed{Completed: &pb.CommandResult{Accepted: true}},
	}, nil
}

type stubRemoteExecutableResolver struct {
	path  string
	calls int
	mu    sync.Mutex
}

func (s *stubRemoteExecutableResolver) Resolve(_ context.Context, kind string, _ map[string]any) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	if kind == "antigravity" {
		return s.path, nil
	}
	return "", nil
}

func TestRemoteExecutableForwardingAndNilDefault(t *testing.T) {
	fakeDaemon := &fakeRemoteExecDaemon{}
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	grpcSrv := grpc.NewServer()
	pb.RegisterDaemonControlServer(grpcSrv, fakeDaemon)
	pb.RegisterSessionControlServer(grpcSrv, fakeDaemon)
	pb.RegisterRuntimeQueryServer(grpcSrv, fakeDaemon)
	go func() { _ = grpcSrv.Serve(lis) }()
	defer grpcSrv.Stop()

	ctx := context.Background()
	client, err := daemonclient.Dial(ctx, daemonclient.Config{
		Target:         lis.Addr().String(),
		Credential:     "cred",
		RequestTimeout: 2 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	prepareStore := func(t *testing.T) *store.Store {
		t.Helper()
		st, openErr := store.Open(filepath.Join(t.TempDir(), "app.db"))
		if openErr != nil {
			t.Fatal(openErr)
		}
		t.Cleanup(func() { _ = st.Close() })
		if _, saveErr := st.SaveSSHConnection(ctx, map[string]any{
			"id":        "wsl-dev",
			"host":      "127.0.0.1",
			"port":      22,
			"user":      "dev",
			"workspace": "/srv/ws",
			"state":     "connected",
		}); saveErr != nil {
			t.Fatal(saveErr)
		}
		if _, upErr := st.UpsertAgent(ctx, map[string]any{
			"id":            "ssh-antigravity-wsl-dev",
			"kind":          "antigravity",
			"name":          "Antigravity · WSL",
			"connection_id": "wsl-dev",
			"status":        "ready",
		}); upErr != nil {
			t.Fatal(upErr)
		}
		return st
	}

	t.Run("forwarded_to_sessions_and_agentapi", func(t *testing.T) {
		st := prepareStore(t)
		resolver := &stubRemoteExecutableResolver{path: "/opt/custom/bin/agy"}
		cfg := testConfig()
		cfg.DefaultWorkspace = "C:/work"

		handler, hErr := NewHandlerChecked(Options{
			Config:           cfg,
			Store:            st,
			DaemonClient:     client,
			RemoteExecutable: resolver,
		})
		if hErr != nil {
			t.Fatal(hErr)
		}

		fakeDaemon.mu.Lock()
		fakeDaemon.seenAntigravityEx = nil
		fakeDaemon.mu.Unlock()

		// 1. Browser sessions route uses forwarded RemoteExecutable
		rec := request(t, handler, http.MethodPost, "/api/v1/sessions",
			strings.NewReader(`{"agent_id":"ssh-antigravity-wsl-dev","title":"Custom Agy"}`),
			map[string]string{"Authorization": "Bearer browser-secret", "Origin": "http://testserver"})
		if rec.Code != http.StatusOK {
			t.Fatalf("sessions create = %d %s", rec.Code, rec.Body.String())
		}

		// 2. Agent API invoke route uses forwarded RemoteExecutable
		rec = request(t, handler, http.MethodPost, "/api/v1/agent/invoke",
			strings.NewReader(`{"capability":"sessions.create","input":{"agent_id":"ssh-antigravity-wsl-dev","title":"Agent Created"}}`),
			map[string]string{"Authorization": "Bearer connector-secret"})
		if rec.Code != http.StatusOK {
			t.Fatalf("agentapi sessions.create = %d %s", rec.Code, rec.Body.String())
		}

		fakeDaemon.mu.Lock()
		seen := append([]string(nil), fakeDaemon.seenAntigravityEx...)
		fakeDaemon.mu.Unlock()

		if len(seen) < 2 {
			t.Fatalf("expected at least 2 CreateSession/QueryNative calls, got %v", seen)
		}
		for _, exe := range seen {
			if exe != "/opt/custom/bin/agy" {
				t.Fatalf("expected forwarded /opt/custom/bin/agy, got %q (all=%v)", exe, seen)
			}
		}
	})

	t.Run("no_resolver_leaves_executable_unset", func(t *testing.T) {
		st := prepareStore(t)
		cfg := testConfig()
		cfg.DefaultWorkspace = "C:/work"

		handler := NewHandler(Options{
			Config:       cfg,
			Store:        st,
			DaemonClient: client,
		})

		fakeDaemon.mu.Lock()
		fakeDaemon.seenAntigravityEx = nil
		fakeDaemon.mu.Unlock()

		rec := request(t, handler, http.MethodPost, "/api/v1/sessions",
			strings.NewReader(`{"agent_id":"ssh-antigravity-wsl-dev","title":"Agy"}`),
			map[string]string{"Authorization": "Bearer browser-secret", "Origin": "http://testserver"})
		if rec.Code != http.StatusOK {
			t.Fatalf("sessions create with nil RemoteExecutable = %d %s", rec.Code, rec.Body.String())
		}

		fakeDaemon.mu.Lock()
		seen := append([]string(nil), fakeDaemon.seenAntigravityEx...)
		fakeDaemon.mu.Unlock()

		if len(seen) == 0 {
			t.Fatal("expected daemon call")
		}
		for _, exe := range seen {
			if exe != "" {
				t.Fatalf("expected unset executable, got %q", exe)
			}
		}
	})
}
