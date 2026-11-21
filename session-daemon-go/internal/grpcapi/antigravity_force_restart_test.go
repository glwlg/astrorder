package grpcapi_test

import (
	"context"
	"testing"

	pb "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
	core "astrorder.dev/session-daemon/internal/runtime"
)

type forceRestartFixture struct {
	controlRuntimeFixture
	sessionID string
	stopped   chan struct{}
}

func (a *forceRestartFixture) ForceShutdown(context.Context) ([]string, error) {
	close(a.stopped)
	return []string{a.sessionID}, nil
}

func TestForceRestartAntigravityOnlyStopsSelectedConnection(t *testing.T) {
	f := startFixture(t)
	targets := map[string]*forceRestartFixture{
		"wsl":    {sessionID: "wsl-conversation", stopped: make(chan struct{})},
		"remote": {sessionID: "remote-conversation", stopped: make(chan struct{})},
	}
	f.daemon.RegisterRuntime("antigravity-ssh", core.NewMultiplexRegistry(func(connectionID string, _ map[string]any) (core.Adapter, error) {
		return targets[connectionID], nil
	}))
	for name, fixture := range targets {
		spawn := f.daemon.ExecuteSessionControl(f.ctx, map[string]any{
			"action": "session.spawn", "agent_type": "antigravity-ssh", "session_id": fixture.sessionID,
			"params": map[string]any{"connection_id": name, "ssh_settings": map[string]any{"host": "fixture.invalid"}},
		})
		if spawn["action"] != "session.spawn.result" {
			t.Fatalf("spawn failed: %v", spawn)
		}
		send := f.daemon.ExecuteSessionControl(f.ctx, map[string]any{"action": "session.send", "session_id": fixture.sessionID, "text": "fixture"})
		if send["action"] != "session.send.result" {
			t.Fatalf("send failed: %v", send)
		}
	}
	client := pb.NewRuntimeQueryClient(f.conn)
	conn := "wsl"
	request := &pb.DisconnectRuntimeRequest{
		Meta: controlMeta("ordinary-restart"), Target: &pb.RuntimeTarget{
			Runtime: pb.RuntimeKind_RUNTIME_KIND_ANTIGRAVITY_SSH, ConnectionId: &conn, Ssh: &pb.SshConnectConfig{Host: "fixture.invalid"},
		},
	}
	refusal, err := client.DisconnectRuntime(f.ctx, request)
	if err != nil || refusal.GetDisconnected() || refusal.GetError() == nil {
		t.Fatalf("ordinary restart did not refuse the active conversation: %v, %v", refusal, err)
	}
	request.Meta = controlMeta("confirmed-force-restart")
	request.ConfirmActive = true
	for i := 0; i < 2; i++ {
		receipt, err := client.DisconnectRuntime(f.ctx, request)
		if err != nil || !receipt.GetDisconnected() || len(receipt.GetReleasedSessionIds()) != 1 || receipt.GetReleasedSessionIds()[0] != targets[conn].sessionID {
			t.Fatalf("unexpected force restart receipt: %v, %v", receipt, err)
		}
	}
	select {
	case <-targets["wsl"].stopped:
	default:
		t.Fatal("selected worker was not stopped")
	}
	select {
	case <-targets["remote"].stopped:
		t.Fatal("an unrelated worker was stopped")
	default:
	}
}
