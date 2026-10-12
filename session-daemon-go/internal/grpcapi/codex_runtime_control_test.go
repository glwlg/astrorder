package grpcapi_test

import (
	"context"
	"encoding/json"
	"io"
	"sync"
	"testing"
	"time"

	pb "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
	"astrorder.dev/session-daemon/internal/runtime/codex"
	"astrorder.dev/session-daemon/internal/transport/ssh"
)

// grpcControlTransport is a synthetic codex app-server stdio transport for the
// official gRPC control path. It records every native method and answers only
// initialize/initialized plus read-only catalog calls; a control-only attach
// that reaches thread/start, thread/resume, session/new, session/load, or
// turn/start is recorded and fails fast with a protocol error.
type grpcControlTransport struct {
	stdinR  *io.PipeReader
	stdinW  *io.PipeWriter
	stdoutR *io.PipeReader
	stdoutW *io.PipeWriter
	done    chan struct{}
	once    sync.Once
}

func newGRPCControlTransport() *grpcControlTransport {
	sinR, sinW := io.Pipe()
	soutR, soutW := io.Pipe()
	return &grpcControlTransport{stdinR: sinR, stdinW: sinW, stdoutR: soutR, stdoutW: soutW, done: make(chan struct{})}
}

func (f *grpcControlTransport) Stdin() io.WriteCloser { return f.stdinW }
func (f *grpcControlTransport) Stdout() io.ReadCloser { return f.stdoutR }
func (f *grpcControlTransport) Stderr() []byte        { return nil }
func (f *grpcControlTransport) Wait() (ssh.ExitStatus, error) {
	<-f.done
	return ssh.ExitStatus{ConfirmedRemoteExit: true, ExitCode: 0}, nil
}
func (f *grpcControlTransport) Close() error {
	_ = f.stdinR.Close()
	_ = f.stdinW.Close()
	_ = f.stdoutR.Close()
	_ = f.stdoutW.Close()
	f.once.Do(func() { close(f.done) })
	return nil
}

type grpcControlFixture struct {
	mu         sync.Mutex
	methods    []string
	transports []*grpcControlTransport
}

func (f *grpcControlFixture) record(method string) {
	f.mu.Lock()
	f.methods = append(f.methods, method)
	f.mu.Unlock()
}

func (f *grpcControlFixture) count(method string) int {
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

func (f *grpcControlFixture) snapshot() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.methods...)
}

func (f *grpcControlFixture) starter() codex.SSHStarter {
	return func(ctx context.Context, spec ssh.CommandSpec) (codex.SSHSession, error) {
		transport := newGRPCControlTransport()
		f.mu.Lock()
		f.transports = append(f.transports, transport)
		f.mu.Unlock()
		go func() {
			decoder := json.NewDecoder(transport.stdinR)
			encoder := json.NewEncoder(transport.stdoutW)
			for {
				var request map[string]any
				if decoder.Decode(&request) != nil {
					return
				}
				method, _ := request["method"].(string)
				if method == "" {
					continue
				}
				f.record(method)
				switch method {
				case "initialized":
					continue
				case "initialize":
					_ = encoder.Encode(map[string]any{"jsonrpc": "2.0", "id": request["id"], "result": map[string]any{"protocolVersion": "1.0", "codexHome": "/control-home"}})
				case "model/list":
					_ = encoder.Encode(map[string]any{"jsonrpc": "2.0", "id": request["id"], "result": map[string]any{"data": []any{map[string]any{"id": "gpt-5.2-codex", "displayName": "GPT-5.2 Codex", "provider": "openai", "supportedReasoningEfforts": []any{map[string]any{"reasoningEffort": "high"}}}}}})
				case "thread/list":
					_ = encoder.Encode(map[string]any{"jsonrpc": "2.0", "id": request["id"], "result": map[string]any{"data": []any{}}})
				default:
					_ = encoder.Encode(map[string]any{"jsonrpc": "2.0", "id": request["id"], "error": map[string]any{"code": -32601, "message": "control-only transport rejects " + method}})
				}
			}
		}()
		return transport, nil
	}
}

// closeTransports ends every native transport started so far, simulating a
// native app-server EOF for the bound control session.
func (f *grpcControlFixture) closeTransports() {
	f.mu.Lock()
	transports := append([]*grpcControlTransport(nil), f.transports...)
	f.mu.Unlock()
	for _, transport := range transports {
		_ = transport.Close()
	}
}

func (f *grpcControlFixture) transportCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.transports)
}

// TestCodexRuntimeControlAttachOverOfficialGRPC drives the real transport
// stack: official AttachSession(RuntimeControl=true) over a real loopback gRPC
// fixture into the real Codex adapter and a synthetic native transport. The
// control attach must initialize without creating a native conversation,
// report the exact control identity and health, reuse the live transport on a
// repeated attach, survive runtime disconnect, and reconnect cleanly.
func TestCodexRuntimeControlAttachOverOfficialGRPC(t *testing.T) {
	fixture := &grpcControlFixture{}
	adapter := codex.NewSSHAdapter(codex.Config{Executable: "/usr/bin/codex", Workspace: "/audit", Allowed: []string{"/audit"}, AgentID: "codex-control-agent"}, nil, fixture.starter())
	defer adapter.Close()
	f := startFixture(t)
	f.daemon.RegisterRuntime("codex", adapter)

	attach := func() *pb.SessionReceipt {
		t.Helper()
		receipt, err := pb.NewSessionControlClient(f.conn).AttachSession(f.ctx, &pb.AttachSessionRequest{
			Session:        &pb.SessionRef{SessionId: "probe-codex-local"},
			Target:         &pb.RuntimeTarget{Runtime: pb.RuntimeKind_RUNTIME_KIND_CODEX},
			RuntimeControl: true,
		})
		if err != nil {
			t.Fatalf("AttachSession: %v", err)
		}
		return receipt
	}

	receipt := attach()
	if !receipt.Attached || receipt.Session.GetSessionId() != "probe-codex-local" || receipt.Status != pb.SessionStatus_SESSION_STATUS_IDLE {
		t.Fatalf("control attach receipt: %+v", receipt)
	}
	if receipt.DaemonId != f.daemon.DaemonID() {
		t.Fatalf("control attach daemon epoch: %+v", receipt)
	}
	if fixture.count("initialize") != 1 {
		t.Fatalf("control attach did not initialize exactly once: %v", fixture.snapshot())
	}
	for _, forbidden := range []string{"thread/start", "thread/resume", "session/new", "session/load", "turn/start"} {
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
		if session.GetSession().GetSessionId() == "probe-codex-local" && session.Status == pb.SessionStatus_SESSION_STATUS_IDLE {
			found = true
		}
	}
	if !found {
		t.Fatalf("authoritative status missing control session: %+v", status.Sessions)
	}

	// A repeated attach reuses the live transport instead of starting another
	// native process.
	if repeated := attach(); repeated.Session.GetSessionId() != "probe-codex-local" {
		t.Fatalf("repeat control attach: %+v", repeated)
	}
	if fixture.count("initialize") != 1 {
		t.Fatalf("repeat control attach started another transport: %v", fixture.snapshot())
	}

	// Official runtime disconnect releases the confirmed idle control binding.
	disconnect, err := pb.NewRuntimeQueryClient(f.conn).DisconnectRuntime(f.ctx, &pb.DisconnectRuntimeRequest{
		Meta:   controlMeta("disconnect-codex-control"),
		Target: &pb.RuntimeTarget{Runtime: pb.RuntimeKind_RUNTIME_KIND_CODEX},
	})
	if err != nil || !disconnect.Disconnected {
		t.Fatalf("control disconnect: %+v %v", disconnect, err)
	}
	released := false
	for _, id := range disconnect.ReleasedSessionIds {
		if id == "probe-codex-local" {
			released = true
		}
	}
	if !released {
		t.Fatalf("control disconnect did not release the control session: %+v", disconnect)
	}

	// A fresh control attach after disconnect re-initializes cleanly without
	// ever touching a native thread.
	deadline := time.Now().Add(5 * time.Second)
	for {
		reconnected := attach()
		if reconnected.Session.GetSessionId() == "probe-codex-local" {
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
	for _, forbidden := range []string{"thread/start", "thread/resume", "session/new", "session/load", "turn/start"} {
		if fixture.count(forbidden) != 0 {
			t.Fatalf("control reconnect issued %s: %v", forbidden, fixture.snapshot())
		}
	}
}
