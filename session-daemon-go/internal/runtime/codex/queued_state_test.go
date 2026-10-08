package codex

import (
	"context"
	"testing"
	"time"

	core "astrorder.dev/session-daemon/internal/runtime"
	"os"
)

func TestQueuedNotificationCannotRewindNativeState(t *testing.T) {
	root := t.TempDir()
	entered, release, projected := make(chan struct{}), make(chan struct{}), make(chan struct{})
	a := NewAdapter(Config{Executable: os.Args[0], Arguments: []string{"-test.run=TestAdapterOwnsMultipleNativeSessions"}, Environment: []string{"ASTRORDER_ADAPTER_HELPER=1"}, Workspace: root, Allowed: []string{root}}, func(_, event string, payload map[string]any, _ string) error {
		if event != "codex.notification" {
			return nil
		}
		frame := payload["frame"].(map[string]any)
		if frame["method"] == "item/started" {
			close(entered)
			<-release
		}
		if frame["method"] == "item/completed" {
			close(projected)
		}
		return nil
	})
	defer a.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	created, err := a.Create(ctx, core.Request{Fields: map[string]any{"cwd": root}})
	if err != nil {
		t.Fatal(err)
	}
	s, err := a.session(created.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	r := s.runtime
	notification := func(method string) {
		r.handleNotification(method, map[string]any{"method": method, "params": map[string]any{"threadId": created.SessionID}})
	}
	notification("item/started")
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	notification("item/completed")
	// A transport failure occurs while older output is waiting for projection.
	r.mu.Lock()
	r.transportFailed, r.status = true, "error"
	r.mu.Unlock()
	close(release)
	select {
	case <-projected:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if got := r.Status(); got != "error" {
		t.Fatalf("queued output rewound failed transport to %s", got)
	}
}

func TestResumeIdentityDoesNotClearEarlierApproval(t *testing.T) {
	r := New(Config{})
	defer r.Close()
	r.handleNotification("item/commandExecution/requestApproval", map[string]any{"id": float64(91), "params": map[string]any{"threadId": "thread"}})
	r.setSession("thread", "idle")
	if got := r.Status(); got != "waiting_approval" {
		t.Fatalf("resume overwrote pending approval: %s", got)
	}
}
