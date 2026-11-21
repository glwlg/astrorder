package antigravity

import (
	"context"
	"testing"
	"time"

	"astrorder.dev/session-daemon/internal/transport/ssh"
)

func TestForceShutdownRequiresRemoteExitAndReleasesNonIdleConversation(t *testing.T) {
	for _, confirmed := range []bool{true, false} {
		name := "uncertain"
		if confirmed {
			name = "confirmed"
		}
		t.Run(name, func(t *testing.T) {
			starts := 0
			adapter := NewWithSSH(Config{Executable: "/fixture/agy", ConnectionID: "wsl"}, func(context.Context, ssh.CommandSpec) (SSHSession, error) {
				starts++
				transport := newFakeAgySSHSession()
				go func() {
					serveMockACP(transport.stdinR, transport.stdoutW, "test-conversation", false)
					if confirmed {
						transport.finishRemote(0)
					} else {
						_ = transport.Close()
					}
				}()
				return transport, nil
			}, nil)
			t.Cleanup(func() { _ = adapter.Close() })
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			client, err := adapter.ensureClient(ctx)
			if err != nil {
				t.Fatal(err)
			}
			session := newSession("test-conversation", "/", client)
			session.status = "running" // No idle completion will arrive before EOF.
			adapter.sessions[session.id] = session
			if _, err := adapter.Shutdown(ctx); err == nil || client.exited() {
				t.Fatal("ordinary shutdown touched the active worker")
			}
			released, err := adapter.ForceShutdown(ctx)
			if confirmed {
				if err != nil || len(released) != 1 || released[0] != session.id || !client.exited() {
					t.Fatalf("forced shutdown did not release the stopped worker: %v, %v", released, err)
				}
				next, err := adapter.ensureClient(ctx)
				if err != nil || next == client || starts != 2 {
					t.Fatalf("could not start a fresh transport: starts=%d, %v", starts, err)
				}
			} else {
				if err == nil || len(released) != 0 {
					t.Fatalf("unconfirmed exit released a transport: %v, %v", released, err)
				}
				if _, err := adapter.ensureClient(ctx); err == nil || starts != 1 {
					t.Fatal("unconfirmed exit allowed a replacement worker")
				}
			}
		})
	}
}
