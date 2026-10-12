package grpcapi_test

// grok_runtime_control_test.go drives the official gRPC control path for a
// Grok control-only attach: real loopback gRPC fixture -> real daemon ->
// Registry -> real Grok adapter -> synthetic ACP transport (over the SSH
// starter seam, in-process). The control attach must initialize without
// creating a native conversation, report the exact control identity and
// health, reuse the live transport on a repeated attach, refuse business
// commands with zero native requests, serve the read-only model catalog from
// the initialize metadata, survive official runtime disconnect, and
// reconnect cleanly.

import (
	"context"
	"encoding/json"
	"io"
	"sync"
	"testing"
	"time"

	pb "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
	"astrorder.dev/session-daemon/internal/runtime/grok"
	"astrorder.dev/session-daemon/internal/transport/ssh"
)

// grokControlTransport is a synthetic Grok ACP stdio transport for the
// official gRPC control path.
type grokControlTransport struct {
	stdinR  *io.PipeReader
	stdinW  *io.PipeWriter
	stdoutR *io.PipeReader
	stdoutW *io.PipeWriter
	done    chan struct{}
	once    sync.Once
}

func newGrokControlTransport() *grokControlTransport {
	sinR, sinW := io.Pipe()
	soutR, soutW := io.Pipe()
	return &grokControlTransport{stdinR: sinR, stdinW: sinW, stdoutR: soutR, stdoutW: soutW, done: make(chan struct{})}
}

func (f *grokControlTransport) Stdin() io.WriteCloser { return f.stdinW }
func (f *grokControlTransport) Stdout() io.ReadCloser { return f.stdoutR }
func (f *grokControlTransport) Stderr() []byte        { return nil }
func (f *grokControlTransport) Wait() (ssh.ExitStatus, error) {
	<-f.done
	return ssh.ExitStatus{ConfirmedRemoteExit: true, ExitCode: 0}, nil
}
func (f *grokControlTransport) Close() error {
	_ = f.stdinR.Close()
	_ = f.stdinW.Close()
	_ = f.stdoutR.Close()
	_ = f.stdoutW.Close()
	f.once.Do(func() { close(f.done) })
	return nil
}

type grokControlFixture struct {
	mu         sync.Mutex
	methods    []string
	transports []*grokControlTransport
}

func (f *grokControlFixture) record(method string) {
	f.mu.Lock()
	f.methods = append(f.methods, method)
	f.mu.Unlock()
}

func (f *grokControlFixture) transportList() []*grokControlTransport {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]*grokControlTransport(nil), f.transports...)
}

func (f *grokControlFixture) count(method string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	total := 0
	for _, m := range f.methods {
		if m == method {
			total++
		}
	}
	return total
}

func (f *grokControlFixture) snapshot() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.methods...)
}

func (f *grokControlFixture) starter() grok.SSHStarter {
	return func(ctx context.Context, spec ssh.CommandSpec) (grok.SSHSession, error) {
		transport := newGrokControlTransport()
		f.mu.Lock()
		f.transports = append(f.transports, transport)
		f.mu.Unlock()
		go func() {
			decoder := json.NewDecoder(transport.stdinR)
			encoder := json.NewEncoder(transport.stdoutW)
			for {
				var request map[string]any
				if decoder.Decode(&request) != nil {
					// Input closed: retire the transport so Wait and the
					// adapter close path observe a confirmed exit.
					_ = transport.Close()
					return
				}
				method, _ := request["method"].(string)
				if method == "" {
					continue
				}
				f.record(method)
				switch method {
				case "initialize":
					_ = encoder.Encode(map[string]any{"jsonrpc": "2.0", "id": request["id"], "result": map[string]any{
						"protocolVersion": 1,
						"_meta": map[string]any{
							"modelState": map[string]any{
								"currentModelId": "grok-4.6",
								"availableModels": []any{
									map[string]any{"modelId": "grok-4.6", "name": "Grok 4.6"},
								},
							},
						},
					}})
				default:
					// A control-only attach must never reach a conversation
					// method; record and fail fast if it does.
					_ = encoder.Encode(map[string]any{"jsonrpc": "2.0", "id": request["id"], "error": map[string]any{"code": -32601, "message": "control-only transport rejects " + method}})
				}
			}
		}()
		return transport, nil
	}
}

// TestGrokRuntimeControlAttachOverOfficialGRPC drives the real transport
// stack end to end for the Grok control-only attach.
func TestGrokRuntimeControlAttachOverOfficialGRPC(t *testing.T) {
	fixture := &grokControlFixture{}
	adapter := grok.NewWithSSHStarter(grok.Config{
		Executable: "/usr/bin/grok",
		Workspace:  "/control",
		Allowed:    []string{"/control"},
		AgentID:    "probe-grok-local",
	}, fixture.starter())
	defer adapter.Close()
	f := startFixture(t)
	f.daemon.RegisterRuntime("grok", adapter)

	attach := func() *pb.SessionReceipt {
		t.Helper()
		receipt, err := pb.NewSessionControlClient(f.conn).AttachSession(f.ctx, &pb.AttachSessionRequest{
			Session:        &pb.SessionRef{SessionId: "probe-grok-local"},
			Target:         &pb.RuntimeTarget{Runtime: pb.RuntimeKind_RUNTIME_KIND_GROK},
			RuntimeControl: true,
		})
		if err != nil {
			t.Fatalf("AttachSession: %v", err)
		}
		return receipt
	}

	receipt := attach()
	if !receipt.Attached || receipt.Session.GetSessionId() != "probe-grok-local" || receipt.Status != pb.SessionStatus_SESSION_STATUS_IDLE {
		t.Fatalf("control attach receipt: %+v", receipt)
	}
	if receipt.DaemonId != f.daemon.DaemonID() {
		t.Fatalf("control attach daemon epoch: %+v", receipt)
	}
	if fixture.count("initialize") != 1 {
		t.Fatalf("control attach did not initialize exactly once: %v", fixture.snapshot())
	}
	for _, forbidden := range []string{"session/new", "session/load", "session/prompt"} {
		if fixture.count(forbidden) != 0 {
			t.Fatalf("control attach issued %s: %v", forbidden, fixture.snapshot())
		}
	}

	// The authoritative daemon status must list the control session as idle.
	status, err := pb.NewDaemonControlClient(f.conn).GetStatus(f.ctx, &pb.GetStatusRequest{})
	if err != nil {
		t.Fatalf("GetStatus: %v", err)
	}
	found := false
	for _, session := range status.Sessions {
		if session.GetSession().GetSessionId() == "probe-grok-local" && session.Status == pb.SessionStatus_SESSION_STATUS_IDLE {
			found = true
		}
	}
	if !found {
		t.Fatalf("authoritative status missing control session: %+v", status.Sessions)
	}

	// A repeated attach reuses the live transport instead of starting
	// another native process.
	if repeated := attach(); repeated.Session.GetSessionId() != "probe-grok-local" {
		t.Fatalf("repeat control attach: %+v", repeated)
	}
	if fixture.count("initialize") != 1 {
		t.Fatalf("repeat control attach started another transport: %v", fixture.snapshot())
	}

	// Business commands are refused with zero native requests; the control ID
	// is never used as a native session. A refused command surfaces as a
	// non-completed receipt (the operation ledger marks it uncertain because
	// a native-side rejection cannot prove absence of side effects).
	if receipt, err := pb.NewSessionControlClient(f.conn).RenameSession(f.ctx, &pb.RenameSessionRequest{
		Meta: controlMeta("rename-grok-control"), Session: &pb.SessionRef{SessionId: "probe-grok-local"}, Title: "literal title",
	}); err == nil && receipt.GetCompleted() != nil && receipt.OperationState == pb.OperationState_OPERATION_STATE_COMPLETED {
		t.Fatalf("control session accepted rename: %+v", receipt)
	}
	if receipt, err := pb.NewSessionControlClient(f.conn).Send(f.ctx, &pb.SendRequest{
		Meta: controlMeta("send-grok-control"), Session: &pb.SessionRef{SessionId: "probe-grok-local"},
		Input: []*pb.InputPart{{Part: &pb.InputPart_Text{Text: &pb.TextInput{Text: "hello"}}}},
	}); err == nil && receipt.GetCompleted() != nil && receipt.OperationState == pb.OperationState_OPERATION_STATE_COMPLETED {
		t.Fatalf("control session accepted send: %+v", receipt)
	}
	if receipt, err := pb.NewSessionControlClient(f.conn).DeleteSession(f.ctx, &pb.DeleteSessionRequest{
		Meta: controlMeta("delete-grok-control"), Session: &pb.SessionRef{SessionId: "probe-grok-local"},
	}); err == nil && receipt.GetCompleted() != nil && receipt.OperationState == pb.OperationState_OPERATION_STATE_COMPLETED {
		t.Fatalf("control session accepted delete: %+v", receipt)
	}
	for _, forbidden := range []string{"session/new", "session/load", "session/prompt"} {
		if fixture.count(forbidden) != 0 {
			t.Fatalf("control business command issued %s: %v", forbidden, fixture.snapshot())
		}
	}

	// The read-only model catalog is served from the initialize metadata with
	// zero native requests.
	models, err := pb.NewRuntimeQueryClient(f.conn).ListModels(f.ctx, &pb.ListModelsRequest{Session: &pb.SessionRef{SessionId: "probe-grok-local"}})
	if err != nil {
		t.Fatalf("ListModels: %v", err)
	}
	if len(models.Items) != 1 || models.Items[0].Model != "grok-4.6" || models.Items[0].Provider != "grok" {
		t.Fatalf("control model catalog: %+v", models.Items)
	}
	if fixture.count("initialize") != 1 {
		t.Fatalf("model catalog read started another transport: %v", fixture.snapshot())
	}

	// Official runtime disconnect releases the confirmed idle control binding.
	disconnect, err := pb.NewRuntimeQueryClient(f.conn).DisconnectRuntime(f.ctx, &pb.DisconnectRuntimeRequest{
		Meta:   controlMeta("disconnect-grok-control"),
		Target: &pb.RuntimeTarget{Runtime: pb.RuntimeKind_RUNTIME_KIND_GROK},
	})
	if err != nil || !disconnect.Disconnected {
		t.Fatalf("control disconnect: %+v %v", disconnect, err)
	}
	released := false
	for _, id := range disconnect.ReleasedSessionIds {
		if id == "probe-grok-local" {
			released = true
		}
	}
	if !released {
		t.Fatalf("control disconnect did not release the control session: %+v", disconnect)
	}

	// A fresh control attach after disconnect re-initializes cleanly without
	// ever touching a native conversation.
	deadline := time.Now().Add(5 * time.Second)
	for {
		reconnected := attach()
		if reconnected.Session.GetSessionId() == "probe-grok-local" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("reconnect after disconnect failed: %+v", reconnected)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if fixture.count("initialize") != 2 {
		t.Fatalf("reconnect did not initialize a fresh transport: %v", fixture.snapshot())
	}
	for _, forbidden := range []string{"session/new", "session/load", "session/prompt"} {
		if fixture.count(forbidden) != 0 {
			t.Fatalf("control reconnect issued %s: %v", forbidden, fixture.snapshot())
		}
	}
}

// TestGrokRuntimeControlEOFReplaceOverOfficialGRPC proves that when a live
// control transport dies (EOF), the Registry recovers the binding: a repeated
// AttachSession(runtime_control=true) over the official gRPC path retires the
// failed transport and re-initializes cleanly, without any native conversation
// method.
func TestGrokRuntimeControlEOFReplaceOverOfficialGRPC(t *testing.T) {
	fixture := &grokControlFixture{}
	adapter := grok.NewWithSSHStarter(grok.Config{
		Executable: "/usr/bin/grok",
		Workspace:  "/control",
		Allowed:    []string{"/control"},
		AgentID:    "probe-grok-local",
	}, fixture.starter())
	defer adapter.Close()
	f := startFixture(t)
	f.daemon.RegisterRuntime("grok", adapter)

	attach := func() (*pb.SessionReceipt, error) {
		return pb.NewSessionControlClient(f.conn).AttachSession(f.ctx, &pb.AttachSessionRequest{
			Session:        &pb.SessionRef{SessionId: "probe-grok-local"},
			Target:         &pb.RuntimeTarget{Runtime: pb.RuntimeKind_RUNTIME_KIND_GROK},
			RuntimeControl: true,
		})
	}

	if receipt, err := attach(); err != nil || receipt.Status != pb.SessionStatus_SESSION_STATUS_IDLE {
		t.Fatalf("initial control attach: %+v %v", receipt, err)
	}
	transports := fixture.transportList()
	if len(transports) != 1 {
		t.Fatalf("initial control attach transports: %d", len(transports))
	}

	// Kill the live transport: EOF must surface as an error state, never as
	// a native conversation.
	if err := transports[0].Close(); err != nil {
		t.Fatalf("close control transport: %v", err)
	}

	// Recovery through the official path: the failed transport is retired and
	// replaced; the retry must not double-start while the first is retiring.
	deadline := time.Now().Add(5 * time.Second)
	for {
		receipt, err := attach()
		if err == nil && receipt.Status == pb.SessionStatus_SESSION_STATUS_IDLE {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("control recovery attach failed: %+v %v", receipt, err)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if fixture.count("initialize") != 2 {
		t.Fatalf("control recovery did not re-initialize: %v", fixture.snapshot())
	}
	for _, forbidden := range []string{"session/new", "session/load", "session/prompt"} {
		if fixture.count(forbidden) != 0 {
			t.Fatalf("control recovery issued %s: %v", forbidden, fixture.snapshot())
		}
	}
}
