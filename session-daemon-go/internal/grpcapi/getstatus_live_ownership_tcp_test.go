package grpcapi_test

import (
	"context"
	"sync"
	"testing"

	pb "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
	core "astrorder.dev/session-daemon/internal/runtime"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// liveOwnershipTCPAdapter is a synthetic multiplexed runtime: Spawn binds a
// session to a connection and DisconnectConnection releases one connection's
// sessions, so the real gRPC surface can be exercised without any native
// process.
type liveOwnershipTCPAdapter struct {
	mu       sync.Mutex
	sessions map[string]string // sessionID -> connectionID
}

func (a *liveOwnershipTCPAdapter) Create(context.Context, core.Request) (core.Result, error) {
	return core.Result{Status: "idle", SessionID: "created"}, nil
}

func (a *liveOwnershipTCPAdapter) Spawn(_ context.Context, request core.Request) (core.Result, error) {
	connID := ""
	if params, ok := request.Fields["params"].(map[string]any); ok {
		connID, _ = params["connection_id"].(string)
	}
	a.mu.Lock()
	if a.sessions == nil {
		a.sessions = map[string]string{}
	}
	a.sessions[request.SessionID] = connID
	a.mu.Unlock()
	return core.Result{SessionID: request.SessionID, Status: "idle", Payload: map[string]any{"agent_id": request.SessionID}}, nil
}

func (a *liveOwnershipTCPAdapter) SessionsForConnection(connectionID string) []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	var out []string
	for id, conn := range a.sessions {
		if conn == connectionID {
			out = append(out, id)
		}
	}
	return out
}

func (a *liveOwnershipTCPAdapter) DisconnectConnection(_ context.Context, connectionID string) ([]string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	var out []string
	for id, conn := range a.sessions {
		if conn == connectionID {
			out = append(out, id)
			delete(a.sessions, id)
		}
	}
	return out, nil
}

func (a *liveOwnershipTCPAdapter) spawn(t *testing.T, fixture *grpcFixture, sessionID, connID string) {
	t.Helper()
	response := fixture.daemon.ExecuteSessionControl(context.Background(), map[string]any{
		"action": "session.spawn", "agent_type": "codex-ssh", "session_id": sessionID,
		"params": map[string]any{"connection_id": connID, "ssh_settings": map[string]any{"host": "synthetic.invalid"}, "runtime_control": true},
	})
	if response["action"] != "session.spawn.result" {
		t.Fatalf("spawn %s failed: %#v", sessionID, response)
	}
}

func sessionByID(response *pb.GetStatusResponse, id string) *pb.SessionSnapshot {
	for _, session := range response.Sessions {
		if session.GetSession().GetSessionId() == id {
			return session
		}
	}
	return nil
}

// GetStatus over a real loopback TCP gRPC connection must expose the live
// ownership verdict, typed runtime, and immutable identity for exactly the
// owning connection; after a scoped disconnect the durable journal row stays
// visible but explicitly stops claiming live ownership, and the other
// connection's binding is untouched.
func TestGetStatusReportsLiveOwnershipOverRealTCP(t *testing.T) {
	f := startFixture(t)
	adapter := &liveOwnershipTCPAdapter{}
	f.daemon.RegisterRuntime("codex-ssh", adapter)
	adapter.spawn(t, f, "probe-codex-saved", "saved")
	adapter.spawn(t, f, "probe-codex-other", "other")

	client := pb.NewDaemonControlClient(f.conn)
	statusResponse, err := client.GetStatus(f.ctx, &pb.GetStatusRequest{})
	if err != nil {
		t.Fatalf("GetStatus: %v", err)
	}
	for _, want := range []struct{ id, conn string }{{"probe-codex-saved", "saved"}, {"probe-codex-other", "other"}} {
		session := sessionByID(statusResponse, want.id)
		if session == nil {
			t.Fatalf("live session %s missing from GetStatus: %+v", want.id, statusResponse.Sessions)
		}
		if session.RuntimeOwned == nil || !session.GetRuntimeOwned() {
			t.Fatalf("live session %s not marked live-owned over the wire: %+v", want.id, session)
		}
		if session.GetRuntime() != pb.RuntimeKind_RUNTIME_KIND_CODEX_SSH {
			t.Fatalf("live session %s runtime kind = %v, want CODEX_SSH", want.id, session.GetRuntime())
		}
		if session.GetSession().GetAgentId() != want.id || session.GetSession().GetConnectionId() != want.conn {
			t.Fatalf("live session %s identity = %+v, want agent %s connection %s", want.id, session.GetSession(), want.id, want.conn)
		}
	}

	// Release only the saved connection through the real RPC surface.
	saved := "saved"
	disconnect, err := pb.NewRuntimeQueryClient(f.conn).DisconnectRuntime(f.ctx, &pb.DisconnectRuntimeRequest{
		Meta: controlMeta("tcp-disconnect-saved"),
		Target: &pb.RuntimeTarget{
			Runtime:      pb.RuntimeKind_RUNTIME_KIND_CODEX_SSH,
			ConnectionId: &saved,
			Ssh:          &pb.SshConnectConfig{Host: "synthetic.invalid"},
		},
	})
	if err != nil || !disconnect.GetDisconnected() {
		t.Fatalf("scoped disconnect: %+v %v", disconnect, err)
	}

	statusResponse, err = client.GetStatus(f.ctx, &pb.GetStatusRequest{})
	if err != nil {
		t.Fatalf("GetStatus after disconnect: %v", err)
	}
	released := sessionByID(statusResponse, "probe-codex-saved")
	if released == nil {
		t.Fatalf("released session lost its durable journal row: %+v", statusResponse.Sessions)
	}
	if released.RuntimeOwned == nil || released.GetRuntimeOwned() {
		t.Fatalf("released session still claims live ownership over the wire: %+v", released)
	}
	if released.Runtime != nil {
		t.Fatalf("released session carries a defaulted runtime kind: %+v", released)
	}
	live := sessionByID(statusResponse, "probe-codex-other")
	if live == nil || live.RuntimeOwned == nil || !live.GetRuntimeOwned() {
		t.Fatalf("other connection's live binding was lost: %+v", statusResponse.Sessions)
	}
	if live.GetSession().GetConnectionId() != "other" {
		t.Fatalf("other connection's identity changed: %+v", live.GetSession())
	}
}

// A live-owned session whose runtime kind has no contract mapping must fail
// the whole GetStatus call closed rather than being reported under a default
// kind.
func TestGetStatusFailsClosedOnUnknownLiveRuntimeKind(t *testing.T) {
	f := startFixture(t)
	adapter := &liveOwnershipTCPAdapter{}
	f.daemon.RegisterRuntime("mystery", adapter)
	response := f.daemon.ExecuteSessionControl(context.Background(), map[string]any{
		"action": "session.spawn", "agent_type": "mystery", "session_id": "probe-mystery-local",
		"params": map[string]any{},
	})
	if response["action"] != "session.spawn.result" {
		t.Fatalf("spawn failed: %#v", response)
	}
	_, err := pb.NewDaemonControlClient(f.conn).GetStatus(f.ctx, &pb.GetStatusRequest{})
	if status.Code(err) != codes.Unavailable {
		t.Fatalf("GetStatus with unmappable live runtime kind = %v, want Unavailable: %v", status.Code(err), err)
	}
}
