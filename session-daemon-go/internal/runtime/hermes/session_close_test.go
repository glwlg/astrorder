package hermes

import (
	core "astrorder.dev/session-daemon/internal/runtime"
	"context"
	"os"
	"testing"
	"time"
)

func TestSessionClosePreservesOtherWorkersAndNativeIdentity(t *testing.T) {
	root := t.TempDir()
	pool := NewIsolated(Config{Executable: os.Args[0], Arguments: []string{"-test.run=TestHermesAdapterOwnsMultipleSessions"}, Environment: []string{"ASTRORDER_HERMES_HELPER=1"}, Workspace: root, Allowed: []string{root}})
	defer pool.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for _, id := range []string{"summary", "neighbor"} {
		if _, err := pool.Spawn(ctx, core.Request{SessionID: id}); err != nil {
			t.Fatal(err)
		}
	}
	worker := pool.workers["summary"]
	worker.mu.Lock()
	worker.sessions["summary"].Status = "running"
	worker.mu.Unlock()
	if _, err := pool.Command(ctx, core.Request{SessionID: "summary", Action: "session.close"}); err == nil {
		t.Fatal("closed active summary")
	}
	worker.mu.Lock()
	worker.sessions["summary"].Status = "idle"
	worker.mu.Unlock()
	closed, err := pool.Command(ctx, core.Request{SessionID: "summary", Action: "session.close"})
	if err != nil || closed.Payload["closed"] != true {
		t.Fatalf("close: %v %v", closed, err)
	}
	if _, exists := pool.Snapshot()["summary"]; exists {
		t.Fatal("closed binding retained")
	}
	if _, err := pool.Command(ctx, core.Request{SessionID: "neighbor", Action: "session.models"}); err != nil {
		t.Fatal("neighbor was interrupted", err)
	}
	if _, err := pool.Spawn(ctx, core.Request{SessionID: "summary"}); err != nil {
		t.Fatal("same native identity could not resume", err)
	}
}
