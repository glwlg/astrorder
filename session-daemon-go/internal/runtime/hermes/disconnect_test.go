package hermes

import (
	"context"
	"os"
	"testing"
	"time"

	core "astrorder.dev/session-daemon/internal/runtime"
)

func TestHermesRuntimeDisconnectPreservesBusyAndCanReconnectAfterIdle(t *testing.T) {
	root := t.TempDir()
	pool := NewIsolated(Config{Executable: os.Args[0], Arguments: []string{"-test.run=TestHermesAdapterOwnsMultipleSessions"}, Environment: []string{"ASTRORDER_HERMES_HELPER=1"}, Workspace: root, Allowed: []string{root}, AgentID: "private-hermes"})
	t.Cleanup(func() { _ = pool.Close() })
	registry := core.NewRegistry()
	registry.Register("hermes", pool)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	request := core.Request{AgentType: "hermes", SessionID: "control", Fields: map[string]any{"params": map[string]any{"runtime_control": true}}}
	if _, err := registry.Spawn(ctx, request); err != nil {
		t.Fatal(err)
	}
	worker := pool.workers["control"]
	pid := worker.cmd.Process.Pid
	for _, state := range []string{"running", "waiting_approval", "error"} {
		worker.mu.Lock()
		worker.sessions["control"].Status = state
		worker.mu.Unlock()
		if _, err := registry.DisconnectRuntime(ctx, "hermes", ""); err == nil {
			t.Errorf("disconnect accepted %s", state)
		}
		worker.mu.RLock()
		closed := worker.closed
		worker.mu.RUnlock()
		if closed || len(registry.Sessions()) != 1 {
			t.Fatal("rejected maintenance closed the gateway or lost ownership")
		}
	}
	worker.mu.Lock()
	worker.sessions["control"].Status = "idle"
	worker.mu.Unlock()
	cancelled, stop := context.WithCancel(ctx)
	stop()
	if _, err := registry.DisconnectRuntime(cancelled, "hermes", ""); err == nil {
		t.Fatal("cancelled disconnect accepted")
	}
	worker.mu.RLock()
	closed := worker.closed
	worker.mu.RUnlock()
	if closed {
		t.Fatal("cancelled disconnect stopped native process")
	}
	released, err := registry.DisconnectRuntime(ctx, "hermes", "")
	if err != nil || len(released) != 1 || released[0] != "control" {
		t.Fatal("idle Hermes disconnect failed", released, err)
	}
	for _, done := range []<-chan struct{}{worker.processDone, worker.eventDone} {
		select {
		case <-done:
		default:
			t.Fatal("disconnect returned before native process and projection dispatcher exited")
		}
	}
	if len(registry.Sessions()) != 0 || len(pool.Snapshot()) != 0 {
		t.Fatal("released Hermes ownership survived")
	}
	if _, err := registry.Spawn(ctx, request); err != nil {
		t.Fatal("disconnected runtime cannot reconnect", err)
	}
	if pool.workers["control"].cmd.Process.Pid == pid {
		t.Fatal("reconnect reused retired native process")
	}
	if _, err := pool.Query(ctx, core.Request{Fields: map[string]any{"method": "models.list"}}); err != nil {
		t.Fatal("new control gateway cannot answer queries", err)
	}
}
