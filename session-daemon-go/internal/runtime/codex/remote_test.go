package codex

import (
	core "astrorder.dev/session-daemon/internal/runtime"
	"astrorder.dev/session-daemon/internal/transport/ssh"
	"context"
	"encoding/json"
	"io"
	"testing"
	"time"
)

type fakeSSHSession struct {
	stdinR  *io.PipeReader
	stdinW  *io.PipeWriter
	stdoutR *io.PipeReader
	stdoutW *io.PipeWriter
	done    chan struct{}
}

func newFakeSSHSession() *fakeSSHSession {
	sinR, sinW := io.Pipe()
	soutR, soutW := io.Pipe()
	return &fakeSSHSession{
		stdinR:  sinR,
		stdinW:  sinW,
		stdoutR: soutR,
		stdoutW: soutW,
		done:    make(chan struct{}),
	}
}

func (f *fakeSSHSession) Stdin() io.WriteCloser {
	return f.stdinW
}

func (f *fakeSSHSession) Stdout() io.ReadCloser {
	return f.stdoutR
}

func (f *fakeSSHSession) Stderr() []byte {
	return nil
}

func (f *fakeSSHSession) Wait() (ssh.ExitStatus, error) {
	<-f.done
	return ssh.ExitStatus{ConfirmedRemoteExit: true, ExitCode: 0}, nil
}

func (f *fakeSSHSession) Close() error {
	_ = f.stdinR.Close()
	_ = f.stdinW.Close()
	_ = f.stdoutR.Close()
	_ = f.stdoutW.Close()
	select {
	case <-f.done:
	default:
		close(f.done)
	}
	return nil
}

func TestRemoteCodexAdapterCreatesAndCommandsOverSSH(t *testing.T) {
	fake := newFakeSSHSession()
	defer fake.Close()

	go func() {
		dec := json.NewDecoder(fake.stdinR)
		enc := json.NewEncoder(fake.stdoutW)

		var req map[string]any
		if err := dec.Decode(&req); err != nil {
			return
		}
		id := req["id"]
		method, _ := req["method"].(string)
		if method == "initialize" {
			_ = enc.Encode(map[string]any{
				"jsonrpc": "2.0",
				"id":      id,
				"result":  map[string]any{"protocolVersion": "1.0"},
			})
		}

		var notif map[string]any
		_ = dec.Decode(&notif) // expect initialized notification

		for {
			var cmd map[string]any
			if err := dec.Decode(&cmd); err != nil {
				return
			}
			cmdID := cmd["id"]
			_ = enc.Encode(map[string]any{
				"jsonrpc": "2.0",
				"id":      cmdID,
				"result":  map[string]any{"thread": map[string]any{"id": "remote-thread-1"}},
			})
		}
	}()

	var transportContext context.Context
	starter := func(ctx context.Context, spec ssh.CommandSpec) (SSHSession, error) {
		transportContext = ctx
		if spec.Executable != "/usr/bin/codex" {
			t.Fatalf("expected remote executable /usr/bin/codex, got %s", spec.Executable)
		}
		return fake, nil
	}

	cfg := Config{
		Executable: "/usr/bin/codex",
		Workspace:  "/remote/workspace",
		Allowed:    []string{"/remote/workspace"},
		AgentID:    "ssh-codex-debian",
	}

	adapter := NewSSHAdapter(cfg, nil, starter)
	defer adapter.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	res, err := adapter.Create(ctx, core.Request{
		AgentType: "codex",
		SessionID: "sess-ssh-1",
		Fields: map[string]any{
			"cwd": "/remote/workspace",
		},
	})
	if err != nil {
		t.Fatalf("failed adapter.Create over ssh: %v", err)
	}
	cancel()
	if transportContext.Err() != nil {
		t.Fatal("create request cancellation reached the owned SSH transport")
	}
	if res.SessionID == "" {
		t.Fatal("expected session id in result")
	}
}
