package hermes

import (
	core "astrorder.dev/session-daemon/internal/runtime"
	"context"
	"os"
	"sync"
	"testing"
	"time"
)

func TestHermesDeleteRejectsInFlightReadWithoutClosingHandle(t *testing.T) {
	root := t.TempDir()
	pool := NewIsolated(Config{Executable: os.Args[0], Arguments: []string{"-test.run=TestHermesAdapterOwnsMultipleSessions"}, Environment: []string{"ASTRORDER_HERMES_HELPER=1", "ASTRORDER_HERMES_SLOW_MODELS=1"}, Workspace: root, Allowed: []string{root}})
	defer pool.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := pool.Spawn(ctx, core.Request{SessionID: "delete-read-race"}); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := pool.Command(ctx, core.Request{SessionID: "delete-read-race", Action: "session.models"})
		done <- err
	}()
	worker := pool.workers["delete-read-race"]
	for {
		worker.mu.RLock()
		pending := len(worker.pending) > 0
		worker.mu.RUnlock()
		if pending {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("native read did not start")
		case <-time.After(time.Millisecond):
		}
	}
	_, err := pool.Command(ctx, core.Request{SessionID: "delete-read-race", Action: "session.delete"})
	if err == nil {
		t.Fatal("delete raced an in-flight owned command")
	}
	if err := <-done; err != nil {
		t.Fatal("read failed after rejected deletion", err)
	}
}

func TestHermesOverflowIsConfinedToOneNativeSession(t *testing.T) {
	root := t.TempDir()
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	pool := NewIsolated(Config{Executable: os.Args[0], Arguments: []string{"-test.run=TestHermesAdapterOwnsMultipleSessions"}, Environment: []string{"ASTRORDER_HERMES_HELPER=1"}, Workspace: root, Allowed: []string{root}, Emit: func(id, event string, payload map[string]any, status string) error {
		if event == "runtime.owner" {
			return nil
		}
		if id == "flood-a" {
			once.Do(func() { close(entered) })
			<-release
		}
		return nil
	}})
	defer pool.Close()
	defer close(release)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for _, id := range []string{"flood-a", "healthy-b"} {
		if _, err := pool.Spawn(ctx, core.Request{SessionID: id}); err != nil {
			t.Fatal(err)
		}
	}
	first := pool.workers["flood-a"]
	frame := &rpcMessage{Method: "event", Params: map[string]any{"type": "hermes.stream", "session_id": "flood-a"}}
	first.queueEvent(frame)
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("projection did not block")
	}
	for range cap(first.eventQueue) {
		if !first.queueEvent(frame) {
			t.Fatal("queue failed before its bound")
		}
	}
	if first.queueEvent(frame) {
		t.Fatal("overflow silently accepted")
	}
	if pool.Snapshot()["flood-a"].Status != "error" {
		t.Fatal("overflow was not visible")
	}
	if _, err := pool.Command(ctx, core.Request{SessionID: "healthy-b", Action: "session.models"}); err != nil {
		t.Fatal("another session lost its native RPC", err)
	}
	if pool.Snapshot()["healthy-b"].Status != "idle" {
		t.Fatal("another session state was corrupted")
	}
}

func TestHermesSessionGatewaysHaveDistinctProcesses(t *testing.T) {
	root := t.TempDir()
	pool := NewIsolated(Config{Executable: os.Args[0], Arguments: []string{"-test.run=TestHermesAdapterOwnsMultipleSessions"}, Environment: []string{"ASTRORDER_HERMES_HELPER=1"}, Workspace: root, Allowed: []string{root}})
	defer pool.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for _, id := range []string{"isolated-a", "isolated-b"} {
		if _, err := pool.Spawn(ctx, core.Request{SessionID: id}); err != nil {
			t.Fatal(err)
		}
	}
	first, second := pool.workers["isolated-a"].cmd.Process.Pid, pool.workers["isolated-b"].cmd.Process.Pid
	if first == second {
		t.Fatal("Hermes sessions share a process failure domain")
	}
}
