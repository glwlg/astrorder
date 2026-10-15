package grpcapi_test

import (
	pb "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
	"context"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"testing"
)

func TestBackgroundProcessesReadUsesExactOwnerWithoutChangingState(t *testing.T) {
	f := startFixture(t)
	adapter := &sessionReadFixture{replies: map[string]map[string]any{"session.processes": {"processes": []any{map[string]any{"session_id": "proc-owned", "status": "running", "command": "serve", "output_tail": "ready"}}}}}
	f.daemon.RegisterRuntime("hermes", adapter)
	legacyWSOperation(t, f.daemon, map[string]any{"action": "session.create", "agent_type": "hermes"})
	client := pb.NewRuntimeQueryClient(f.conn)
	req := &pb.ListBackgroundProcessesRequest{Session: &pb.SessionRef{SessionId: "read-owned", AgentId: ptr("agent-fixture")}}
	before, _ := f.daemon.AuthorityStatus()
	if _, err := client.ListBackgroundProcesses(context.Background(), req); status.Code(err) != codes.Unauthenticated {
		t.Fatal(err)
	}
	response, err := client.ListBackgroundProcesses(f.ctx, req)
	if err != nil || len(response.GetProcesses()) != 1 {
		t.Fatalf("%v %v", response, err)
	}
	req.Session.AgentId = ptr("another-agent")
	if _, err := client.ListBackgroundProcesses(f.ctx, req); status.Code(err) != codes.FailedPrecondition {
		t.Fatal("foreign owner accepted", err)
	}
	after, _ := f.daemon.AuthorityStatus()
	if after.Sessions[0].Status != before.Sessions[0].Status || after.Sessions[0].MaxSeqID != before.Sessions[0].MaxSeqID {
		t.Fatal("read changed session")
	}
	adapter.mu.Lock()
	defer adapter.mu.Unlock()
	if len(adapter.calls) != 1 || adapter.calls[0].Action != "session.processes" {
		t.Fatal(adapter.calls)
	}
}
