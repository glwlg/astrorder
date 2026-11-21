package grpcapi_test

import (
	"context"
	"strings"
	"testing"

	pb "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
)

type disconnectGuardFixture struct {
	controlRuntimeFixture
	shutdownCalls chan struct{}
}

func (a *disconnectGuardFixture) Shutdown(context.Context) ([]string, error) {
	a.shutdownCalls <- struct{}{}
	return []string{"control-owned"}, nil
}

func TestDisconnectRuntimeReportsActiveSessionWithoutDispatching(t *testing.T) {
	f := startFixture(t)
	adapter := &disconnectGuardFixture{shutdownCalls: make(chan struct{}, 3)}
	f.daemon.RegisterRuntime("hermes", adapter)
	f.daemon.ExecuteSessionControl(f.ctx, map[string]any{"action": "session.create", "agent_type": "hermes"})
	f.daemon.ExecuteSessionControl(f.ctx, map[string]any{"action": "session.send", "session_id": "control-owned", "text": "fixture"})

	client := pb.NewRuntimeQueryClient(f.conn)
	request := &pb.DisconnectRuntimeRequest{
		Meta:   controlMeta("disconnect-while-active"),
		Target: &pb.RuntimeTarget{Runtime: pb.RuntimeKind_RUNTIME_KIND_HERMES},
	}
	assertRefused := func() {
		t.Helper()
		response, err := client.DisconnectRuntime(f.ctx, request)
		if err != nil || response.GetDisconnected() ||
			response.GetOperationState() != pb.OperationState_OPERATION_STATE_COMPLETED ||
			response.GetError().GetKind() != pb.ErrorKind_ERROR_KIND_FAILED_PRECONDITION ||
			!strings.Contains(response.GetError().GetMessage(), "1 non-idle") ||
			response.GetError().GetRetryable() {
			t.Fatalf("expected a definite refusal mentioning the active session, got %v, %v", response, err)
		}
	}
	assertRefused()

	// A replay must keep the recorded refusal even after the native session
	// becomes idle. A new request can then disconnect normally.
	idle := f.daemon.ExecuteSessionControl(f.ctx, map[string]any{"action": "session.interrupt", "session_id": "control-owned"})
	if idle["action"] != "session.interrupt.result" {
		t.Fatalf("fixture did not settle the session: %v", idle)
	}
	assertRefused()
	if len(adapter.shutdownCalls) != 0 {
		t.Fatal("refused request reached the adapter")
	}
	request.Meta = controlMeta("disconnect-after-idle")
	response, err := client.DisconnectRuntime(f.ctx, request)
	if err != nil || !response.GetDisconnected() || len(adapter.shutdownCalls) != 1 {
		t.Fatalf("idle session could not be disconnected: %v, %v", response, err)
	}
}
