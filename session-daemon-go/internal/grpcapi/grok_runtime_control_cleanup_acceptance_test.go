package grpcapi_test

import (
	"context"
	"fmt"
	"testing"

	pb "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
	"astrorder.dev/session-daemon/internal/runtime/grok"
	"astrorder.dev/session-daemon/internal/transport/ssh"
)

type grokUnconfirmedExitTransport struct{ *grokControlTransport }

func (t *grokUnconfirmedExitTransport) Wait() (ssh.ExitStatus, error) {
	<-t.done
	return ssh.ExitStatus{}, fmt.Errorf("fixture remote exit could not be confirmed")
}

// Shutdown must not acknowledge a released connection after its transport
// explicitly reports that remote process cleanup was not confirmed.
func TestAcceptanceGrokDisconnectRejectsUnconfirmedNativeExit(t *testing.T) {
	fixture := &grokControlFixture{}
	starter := fixture.starter()
	adapter := grok.NewWithSSHStarter(grok.Config{Executable: "/usr/bin/grok", Workspace: "/control", Allowed: []string{"/control"}, AgentID: "grok-private-fixture"}, func(ctx context.Context, spec ssh.CommandSpec) (grok.SSHSession, error) {
		transport, err := starter(ctx, spec)
		if err != nil {
			return nil, err
		}
		return &grokUnconfirmedExitTransport{transport.(*grokControlTransport)}, nil
	})
	defer adapter.Close()
	f := startFixture(t)
	f.daemon.RegisterRuntime("grok", adapter)
	ref := &pb.SessionRef{SessionId: "probe-grok-unconfirmed"}
	target := &pb.RuntimeTarget{Runtime: pb.RuntimeKind_RUNTIME_KIND_GROK}
	if _, err := pb.NewSessionControlClient(f.conn).AttachSession(f.ctx, &pb.AttachSessionRequest{Session: ref, Target: target, RuntimeControl: true}); err != nil {
		t.Fatal(err)
	}
	result, err := pb.NewRuntimeQueryClient(f.conn).DisconnectRuntime(f.ctx, &pb.DisconnectRuntimeRequest{Meta: controlMeta("grok-unconfirmed-exit"), Target: target})
	if err == nil && result.GetDisconnected() {
		t.Fatalf("unconfirmed native exit reported successful disconnect: %v", result)
	}
	if err == nil && result.GetOperationState() == pb.OperationState_OPERATION_STATE_COMPLETED {
		t.Fatalf("unconfirmed native exit completed the disconnect operation: %v", result)
	}
	// The failed cleanup must keep the binding visibly error and refuse a
	// silent replacement: a second control attach must not start another
	// transport over the possibly live native process.
	if entry, ok := adapter.Snapshot()["probe-grok-unconfirmed"]; !ok || entry.Status != "error" {
		t.Fatalf("unconfirmed cleanup did not keep an error binding: %+v", adapter.Snapshot())
	}
	if receipt, err := pb.NewSessionControlClient(f.conn).AttachSession(f.ctx, &pb.AttachSessionRequest{Session: ref, Target: target, RuntimeControl: true}); err == nil {
		t.Fatalf("control reattach replaced a binding with unconfirmed native cleanup: %+v", receipt)
	}
	if fixture.count("initialize") != 1 {
		t.Fatalf("refused reattach started another native transport: %v", fixture.snapshot())
	}
}

// grokNilErrorUnconfirmedExitTransport reports no Wait error while explicitly
// not confirming the remote exit; a nil error is not confirmation.
type grokNilErrorUnconfirmedExitTransport struct{ *grokControlTransport }

func (t *grokNilErrorUnconfirmedExitTransport) Wait() (ssh.ExitStatus, error) {
	<-t.done
	return ssh.ExitStatus{ConfirmedRemoteExit: false, LocalKilled: true}, nil
}

// TestAcceptanceGrokDisconnectRejectsNilErrorUnconfirmedExit covers the same
// boundary as the error case: a transport that returns no error but does not
// confirm the remote exit must refuse the release and refuse a replacement
// control attach over the possibly live native process.
func TestAcceptanceGrokDisconnectRejectsNilErrorUnconfirmedExit(t *testing.T) {
	fixture := &grokControlFixture{}
	starter := fixture.starter()
	adapter := grok.NewWithSSHStarter(grok.Config{Executable: "/usr/bin/grok", Workspace: "/control", Allowed: []string{"/control"}, AgentID: "grok-private-fixture"}, func(ctx context.Context, spec ssh.CommandSpec) (grok.SSHSession, error) {
		transport, err := starter(ctx, spec)
		if err != nil {
			return nil, err
		}
		return &grokNilErrorUnconfirmedExitTransport{transport.(*grokControlTransport)}, nil
	})
	defer adapter.Close()
	f := startFixture(t)
	f.daemon.RegisterRuntime("grok", adapter)
	ref := &pb.SessionRef{SessionId: "probe-grok-nil-unconfirmed"}
	target := &pb.RuntimeTarget{Runtime: pb.RuntimeKind_RUNTIME_KIND_GROK}
	if _, err := pb.NewSessionControlClient(f.conn).AttachSession(f.ctx, &pb.AttachSessionRequest{Session: ref, Target: target, RuntimeControl: true}); err != nil {
		t.Fatal(err)
	}
	result, err := pb.NewRuntimeQueryClient(f.conn).DisconnectRuntime(f.ctx, &pb.DisconnectRuntimeRequest{Meta: controlMeta("grok-nil-unconfirmed-exit"), Target: target})
	if err == nil && result.GetDisconnected() {
		t.Fatalf("unconfirmed (nil error) native exit reported successful disconnect: %v", result)
	}
	if err == nil && result.GetOperationState() == pb.OperationState_OPERATION_STATE_COMPLETED {
		t.Fatalf("unconfirmed (nil error) native exit completed the disconnect operation: %v", result)
	}
	if entry, ok := adapter.Snapshot()["probe-grok-nil-unconfirmed"]; !ok || entry.Status != "error" {
		t.Fatalf("unconfirmed cleanup did not keep an error binding: %+v", adapter.Snapshot())
	}
	if receipt, err := pb.NewSessionControlClient(f.conn).AttachSession(f.ctx, &pb.AttachSessionRequest{Session: ref, Target: target, RuntimeControl: true}); err == nil {
		t.Fatalf("control reattach replaced a binding with unconfirmed native cleanup: %+v", receipt)
	}
	if fixture.count("initialize") != 1 {
		t.Fatalf("refused reattach started another native transport: %v", fixture.snapshot())
	}
}
