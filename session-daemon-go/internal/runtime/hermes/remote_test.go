package hermes

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	core "astrorder.dev/session-daemon/internal/runtime"
	"astrorder.dev/session-daemon/internal/transport/ssh"
)

type mockRemoteSession struct {
	stdinR  *io.PipeReader
	stdinW  *io.PipeWriter
	stdoutR *io.PipeReader
	stdoutW *io.PipeWriter
	closed  chan struct{}
	once    sync.Once
}

func newMockRemoteSession() *mockRemoteSession {
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	return &mockRemoteSession{
		stdinR:  inR,
		stdinW:  inW,
		stdoutR: outR,
		stdoutW: outW,
		closed:  make(chan struct{}),
	}
}

func (s *mockRemoteSession) Stdin() io.WriteCloser {
	return s.stdinW
}

func (s *mockRemoteSession) Stdout() io.ReadCloser {
	return s.stdoutR
}

func (s *mockRemoteSession) Stderr() []byte {
	return nil
}

func (s *mockRemoteSession) Wait() (ssh.ExitStatus, error) {
	<-s.closed
	return ssh.ExitStatus{ConfirmedRemoteExit: true, ExitCode: 0}, nil
}

func (s *mockRemoteSession) Close() error {
	s.once.Do(func() {
		close(s.closed)
		_ = s.stdinR.Close()
		_ = s.stdinW.Close()
		_ = s.stdoutR.Close()
		_ = s.stdoutW.Close()
	})
	return nil
}

// simulateHermesRemoteGateway responds to gateway.ready, session.create, and session.delete
func simulateHermesRemoteGateway(t *testing.T, s *mockRemoteSession, workspace string) {
	go func() {
		reader := bufio.NewReader(s.stdinR)
		writer := s.stdoutW

		// Send initial ready event (method is "event", params.type is "gateway.ready")
		readyEv := map[string]any{
			"jsonrpc": "2.0",
			"method":  "event",
			"params": map[string]any{
				"type":    "gateway.ready",
				"version": "1.0.0",
			},
		}
		readyBytes, _ := json.Marshal(readyEv)
		_, _ = fmt.Fprintf(writer, "%s\n", readyBytes)

		for {
			line, err := reader.ReadBytes('\n')
			if err != nil {
				return
			}
			lineStr := strings.TrimSpace(string(line))
			if lineStr == "" {
				continue
			}

			var req map[string]any
			if err := json.Unmarshal([]byte(lineStr), &req); err != nil {
				continue
			}

			id := req["id"]
			method, _ := req["method"].(string)

			resp := map[string]any{
				"jsonrpc": "2.0",
				"id":      id,
			}

			switch method {
			case "session.create":
				resp["result"] = map[string]any{
					"session_id": "hermes-remote-s1",
					"title":      "Test Remote Hermes",
				}
			case "session.resume":
				resp["result"] = map[string]any{
					"session_id": "hermes-remote-s1",
					"info": map[string]any{
						"cwd": workspace,
					},
				}
			case "session.delete":
				resp["result"] = map[string]any{
					"deleted": true,
				}
			default:
				resp["result"] = map[string]any{"ok": true}
			}

			respBytes, _ := json.Marshal(resp)
			_, _ = fmt.Fprintf(writer, "%s\n", respBytes)
		}
	}()
}

func TestHermesRemoteSSHStarterIntegration(t *testing.T) {
	workspace := "/tmp/mock-remote-workspace"
	mockSession := newMockRemoteSession()
	simulateHermesRemoteGateway(t, mockSession, workspace)

	var transportContext context.Context
	starter := func(ctx context.Context, spec ssh.CommandSpec) (SSHSession, error) {
		transportContext = ctx
		return mockSession, nil
	}

	cfg := Config{
		Executable: "python3",
		Workspace:  workspace,
		Allowed:    []string{workspace},
	}

	adapter := NewWithSSHStarter(cfg, starter)
	defer adapter.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Spawn remote session
	res, err := adapter.Spawn(ctx, core.Request{
		SessionID: "s-remote-1",
		Fields: map[string]any{
			"cwd": workspace,
		},
	})
	if err != nil {
		t.Fatalf("failed to spawn remote Hermes session: %v", err)
	}

	cancel()
	if transportContext.Err() != nil {
		t.Fatal("spawn request cancellation reached the owned SSH transport")
	}
	if res.SessionID != "s-remote-1" {
		t.Fatalf("expected session s-remote-1, got %s", res.SessionID)
	}

	// Active check via Snapshot
	snaps := adapter.Snapshot()
	snap, ok := snaps["s-remote-1"]
	if !ok {
		t.Fatalf("expected snapshot for s-remote-1")
	}
	if snap.SessionID != "s-remote-1" {
		t.Fatalf("expected snapshot session_id s-remote-1, got %s", snap.SessionID)
	}

	// Delete
	_, err = adapter.Command(context.Background(), core.Request{
		SessionID: "s-remote-1",
		Action:    "session.delete",
	})
	if err != nil {
		t.Fatalf("failed to delete remote session: %v", err)
	}

	// Close adapter
	if err := adapter.Close(); err != nil {
		t.Fatalf("failed to close adapter: %v", err)
	}
}
