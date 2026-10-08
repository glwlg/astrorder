package hermes

import (
	core "astrorder.dev/session-daemon/internal/runtime"
	"context"
	"os"
	"testing"
	"time"
)

func TestHermesPoolWaitsForRetiredProjectionBeforeReattach(t *testing.T) {
	root := t.TempDir()
	entered, release := make(chan struct{}), make(chan struct{})
	p := NewIsolated(Config{Executable: os.Args[0], Arguments: []string{"-test.run=TestHermesAdapterOwnsMultipleSessions"}, Environment: []string{"ASTRORDER_HERMES_HELPER=1"}, Workspace: root, Allowed: []string{root}, Emit: func(_ string, event string, _ map[string]any, _ string) error {
		if event == "runtime.owner" {
			return nil
		}
		close(entered)
		<-release
		return nil
	}})
	defer p.Close()
	defer close(release)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req := core.Request{SessionID: "blocked-projection"}
	if _, err := p.Spawn(ctx, req); err != nil {
		t.Fatal(err)
	}
	first := p.workers[req.SessionID]
	first.queueEvent(&rpcMessage{Method: "event", Params: map[string]any{"type": "hermes.stream", "session_id": req.SessionID}})
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("projection not entered")
	}
	first.failTransport(context.Canceled)
	if err := first.cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-first.processDone:
	case <-ctx.Done():
		t.Fatal("process not exited")
	}
	if _, err := p.Spawn(ctx, req); err == nil {
		t.Fatal("reattach allowed stale projection to target the new owner")
	}
}

func TestHermesPoolReattachesOnlyAfterConfirmedNativeExit(t *testing.T) {
	root := t.TempDir()
	p := NewIsolated(Config{Executable: os.Args[0], Arguments: []string{"-test.run=TestHermesAdapterOwnsMultipleSessions"}, Environment: []string{"ASTRORDER_HERMES_HELPER=1"}, Workspace: root, Allowed: []string{root}})
	defer p.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req := core.Request{SessionID: "reattach-owned"}
	if _, err := p.Spawn(ctx, req); err != nil {
		t.Fatal(err)
	}
	first := p.workers[req.SessionID]
	first.failTransport(context.Canceled)
	if _, err := p.Spawn(ctx, req); err == nil {
		t.Fatal("replaced native owner without exit confirmation")
	}
	if err := first.cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-first.processDone:
	case <-ctx.Done():
		t.Fatal("native process did not exit")
	}
	select {
	case <-first.eventDone:
	case <-ctx.Done():
		t.Fatal("retired dispatcher did not exit")
	}
	if _, err := p.Spawn(ctx, req); err != nil {
		t.Fatal("confirmed exited worker permanently locked session", err)
	}
	if p.workers[req.SessionID] == first {
		t.Fatal("stale worker reused")
	}
	if _, err := p.Command(ctx, core.Request{SessionID: req.SessionID, Action: "session.models"}); err != nil {
		t.Fatal(err)
	}
}
