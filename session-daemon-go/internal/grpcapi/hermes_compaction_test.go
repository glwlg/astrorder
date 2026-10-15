package grpcapi_test

import (
	"context"
	"encoding/json"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	pb "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
	"astrorder.dev/session-daemon/internal/runtime/hermes"
	"astrorder.dev/session-daemon/internal/transport/ssh"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestHermesCompactionCompletesAfterCallerStopsWaiting(t *testing.T) {
	exerciseHermesCompaction(t, 0)
}

func TestHermesCompactionWaitsBeyondOrdinaryDeadline(t *testing.T) {
	if os.Getenv("ASTRORDER_TEST_LONG_COMPACTION") != "1" {
		t.Skip("set ASTRORDER_TEST_LONG_COMPACTION=1 for the 35-second native RPC fixture")
	}
	exerciseHermesCompaction(t, 35*time.Second)
}

func exerciseHermesCompaction(t *testing.T, wait time.Duration) {
	t.Helper()
	f := startFixture(t)
	started, release := make(chan struct{}, 1), make(chan struct{})
	var released sync.Once
	t.Cleanup(func() { released.Do(func() { close(release) }) })
	var calls atomic.Int32
	transport := newGRPCControlTransport()
	go func() {
		decoder, encoder := json.NewDecoder(transport.stdinR), json.NewEncoder(transport.stdoutW)
		_ = encoder.Encode(map[string]any{"method": "event", "params": map[string]any{"type": "gateway.ready"}})
		for {
			var request map[string]any
			if decoder.Decode(&request) != nil {
				return
			}
			result := map[string]any{}
			switch request["method"] {
			case "session.resume":
				result = map[string]any{"session_id": "handle", "info": map[string]any{"cwd": "/work"}}
			case "command.dispatch":
				calls.Add(1)
				started <- struct{}{}
				<-release
				result = map[string]any{"type": "output", "output": "native compression complete"}
			case "session.control.read":
			default:
				t.Errorf("unexpected native method: %v", request["method"])
			}
			if encoder.Encode(map[string]any{"id": request["id"], "result": result}) != nil {
				return
			}
		}
	}()
	a := hermes.NewWithSSHStarter(hermes.Config{Executable: "/fixture/hermes", Workspace: "/work", Allowed: []string{"/work"}, AgentID: "fixture", Emit: f.daemon.Emit}, func(context.Context, ssh.CommandSpec) (hermes.SSHSession, error) { return transport, nil })
	t.Cleanup(func() { _ = a.Close() })
	f.daemon.RegisterRuntime("hermes", a)
	bound := f.daemon.ExecuteSessionControl(context.Background(), map[string]any{"action": "session.spawn", "agent_type": "hermes", "session_id": "compression-session"})
	if bound["action"] == "error" {
		t.Fatal(bound)
	}
	client := pb.NewSessionControlClient(f.conn)
	ref := &pb.SessionRef{SessionId: "compression-session", AgentId: ptr("fixture")}
	request := &pb.SendRequest{Meta: controlMeta("compression-command"), Session: ref, Input: []*pb.InputPart{{Part: &pb.InputPart_Text{Text: &pb.TextInput{Text: "/compress"}}}}}
	ctx, cancel := context.WithCancel(f.ctx)
	defer cancel()
	returned := make(chan error, 1)
	go func() { _, err := client.Send(ctx, request); returned <- err }()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("native compression never started")
	}
	cancel()
	if err := <-returned; status.Code(err) != codes.Canceled {
		t.Fatalf("caller cancellation: %v", err)
	}
	key := operationKey("compression-command", "session.send")
	key.SessionId = operationNullableValue(ref.SessionId)
	query := pb.NewOperationQueryClient(f.conn)
	pending, err := query.GetOperation(f.ctx, &pb.GetOperationRequest{Key: key})
	if err != nil || pending.State != pb.OperationState_OPERATION_STATE_UNCERTAIN {
		t.Fatalf("pending ledger: %v %v", pending, err)
	}
	duplicate, err := client.Send(f.ctx, request)
	if err != nil || duplicate.OperationState != pb.OperationState_OPERATION_STATE_UNCERTAIN {
		t.Fatalf("in-flight duplicate: %v %v", duplicate, err)
	}
	if wait > 0 {
		time.Sleep(wait)
	}
	released.Do(func() { close(release) })
	deadline := time.Now().Add(time.Second)
	for {
		receipt, err := query.GetOperation(f.ctx, &pb.GetOperationRequest{Key: key})
		if err != nil {
			t.Fatal(err)
		}
		if receipt.State == pb.OperationState_OPERATION_STATE_COMPLETED {
			if receipt.TypedResponse == nil || !receipt.TypedResponse.Accepted {
				t.Fatalf("completion is not recoverable: %v", receipt)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("caller cancellation discarded native completion: %v", receipt)
		}
		time.Sleep(5 * time.Millisecond)
	}
	completed, err := client.Send(f.ctx, request)
	if err != nil || completed.OperationState != pb.OperationState_OPERATION_STATE_COMPLETED || calls.Load() != 1 {
		t.Fatalf("duplicate redispatched native compression: %v %v, calls=%d", completed, err, calls.Load())
	}
}
