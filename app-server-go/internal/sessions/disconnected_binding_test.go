package sessions

import (
	pb "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
	"context"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"testing"
)

func TestExplicitDisconnectPreventsNativeBindingUntilReenabled(t *testing.T) {
	calls := 0
	srv := &mockDaemonServer{
		readModelCb: func(*pb.ReadModelRequest) (*pb.ModelState, error) {
			return nil, status.Error(codes.NotFound, "owner missing")
		},
		attachCb: func(r *pb.AttachSessionRequest) (*pb.SessionReceipt, error) {
			calls++
			return &pb.SessionReceipt{Session: r.Session, Attached: true, Status: pb.SessionStatus_SESSION_STATUS_IDLE}, nil
		},
	}
	h, st, _, cleanup := setupTestServer(t, srv)
	defer cleanup()
	ctx := context.Background()
	if _, err := st.UpsertAgent(ctx, map[string]any{"id": "a", "kind": "hermes", "name": "test", "status": "ready"}); err != nil {
		t.Fatal(err)
	}
	if err := st.SetAgentConnectionChoice(ctx, "local:hermes", false); err != nil {
		t.Fatal(err)
	}
	row := map[string]any{"id": "native", "agent_id": "a", "workspace": "C:/work"}
	if err := h.bindNativeSession(ctx, row); err == nil {
		t.Error("explicitly disconnected Agent binding accepted")
	}
	if calls != 0 {
		t.Fatalf("opening a historical session reconnected the Agent: %d", calls)
	}
	if _, err := h.resolveRuntimeTarget(ctx, map[string]any{"id": "a", "kind": "hermes"}); err == nil {
		t.Error("runtime target allowed a disabled Agent")
	}
	if err := st.SetAgentConnectionChoice(ctx, "local:hermes", true); err != nil {
		t.Fatal(err)
	}
	_ = h.bindNativeSession(ctx, row)
	if calls != 1 {
		t.Fatal("explicit reconnect did not restore binding")
	}
}
