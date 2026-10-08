package grok

import (
	"context"
	"encoding/json"
	"io"
	"testing"
	"time"

	core "astrorder.dev/session-daemon/internal/runtime"
	"astrorder.dev/session-daemon/internal/transport/ssh"
)

func runGrokACPLoop(sess *fakeGrokSSHSession) {
	defer close(sess.done)
	dec, enc := json.NewDecoder(sess.stdinR), json.NewEncoder(sess.stdoutW)
	for {
		var req map[string]any
		if err := dec.Decode(&req); err != nil {
			return
		}
		id := req["id"]
		method, _ := req["method"].(string)
		switch method {
		case "initialize":
			_ = enc.Encode(map[string]any{
				"jsonrpc": "2.0",
				"id":      id,
				"result": map[string]any{
					"protocolVersion": 1,
					"_meta": map[string]any{
						"modelState": map[string]any{
							"currentModelId": "grok-4.6",
							"availableModels": []any{
								map[string]any{
									"modelId": "grok-4.6",
									"name":    "Grok 4.6",
								},
							},
						},
					},
				},
			})
		case "session/new":
			_ = enc.Encode(map[string]any{
				"jsonrpc": "2.0",
				"id":      id,
				"result":  map[string]any{"sessionId": "grok-remote-sess-1"},
			})
		case "session/load":
			p, _ := req["params"].(map[string]any)
			sessID := "grok-remote-sess-1"
			if p != nil && p["sessionId"] != nil {
				sessID, _ = p["sessionId"].(string)
			}
			_ = enc.Encode(map[string]any{
				"jsonrpc": "2.0",
				"id":      id,
				"result": map[string]any{
					"sessionId": sessID,
					"configOptions": []any{
						map[string]any{"id": "model", "currentValue": "grok-4.6"},
					},
				},
			})
		default:
			if id != nil {
				_ = enc.Encode(map[string]any{
					"jsonrpc": "2.0",
					"id":      id,
					"result":  map[string]any{},
				})
			}
		}
	}
}

type fakeGrokSSHSession struct {
	stdinR  *io.PipeReader
	stdinW  *io.PipeWriter
	stdoutR *io.PipeReader
	stdoutW *io.PipeWriter
	done    chan struct{}
}

func newFakeGrokSSHSession() *fakeGrokSSHSession {
	sinR, sinW := io.Pipe()
	soutR, soutW := io.Pipe()
	sess := &fakeGrokSSHSession{
		stdinR:  sinR,
		stdinW:  sinW,
		stdoutR: soutR,
		stdoutW: soutW,
		done:    make(chan struct{}),
	}
	go runGrokACPLoop(sess)
	return sess
}

func (f *fakeGrokSSHSession) Stdin() io.WriteCloser {
	return f.stdinW
}

func (f *fakeGrokSSHSession) Stdout() io.ReadCloser {
	return f.stdoutR
}

func (f *fakeGrokSSHSession) Stderr() []byte {
	return nil
}

func (f *fakeGrokSSHSession) Wait() (ssh.ExitStatus, error) {
	<-f.done
	return ssh.ExitStatus{ConfirmedRemoteExit: true, ExitCode: 0}, nil
}

func (f *fakeGrokSSHSession) Close() error {
	_ = f.stdinW.Close()
	_ = f.stdoutW.Close()
	// The ACP loop exclusively owns completion; Close only retires pipes.
	_ = f.stdinR.Close()
	_ = f.stdoutR.Close()
	return nil
}

func TestRemoteGrokAdapterInitializesOverSSH(t *testing.T) {
	var transportContext context.Context
	starter := func(ctx context.Context, spec ssh.CommandSpec) (SSHSession, error) {
		transportContext = ctx
		return newFakeGrokSSHSession(), nil
	}

	cfg := Config{
		Executable: "/opt/grok/bin/grok",
		Workspace:  "/remote/grok_ws",
		Allowed:    []string{"/remote/grok_ws"},
		AgentID:    "ssh-grok-debian",
	}

	adapter := NewWithSSHStarter(cfg, starter)
	defer adapter.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	res, err := adapter.Create(ctx, core.Request{
		AgentType: "grok",
		Fields: map[string]any{
			"cwd": "/remote/grok_ws",
		},
	})
	if err != nil {
		t.Logf("Create error: %v", err)
		t.Fatalf("failed adapter.Create over ssh: %v", err)
	}
	cancel()
	if transportContext.Err() != nil {
		t.Fatal("create request cancellation reached the owned SSH transport")
	}
	if res.SessionID != "grok-remote-sess-1" {
		t.Fatalf("expected session id grok-remote-sess-1, got %s", res.SessionID)
	}
}
