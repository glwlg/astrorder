package runtime

import (
	"context"
	"strings"
	"testing"
)

type dummyDisconnectable struct {
	sessions []string
	stopped  bool
}

func (d *dummyDisconnectable) Create(ctx context.Context, r Request) (Result, error) {
	return Result{SessionID: r.SessionID, Status: "idle"}, nil
}

func (d *dummyDisconnectable) Close() error {
	d.stopped = true
	return nil
}

func TestDisconnectRuntimeBlocksActiveSessionsAndReleasesIdle(t *testing.T) {
	r := NewRegistry()
	ad := &dummyDisconnectable{}
	r.Register("codex", ad)

	ctx := context.Background()
	_, err := r.Create(ctx, Request{AgentType: "codex", SessionID: "sess-1"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = r.Create(ctx, Request{AgentType: "codex", SessionID: "sess-2"})
	if err != nil {
		t.Fatal(err)
	}

	// Fake sess-2 to running
	r.mu.Lock()
	r.states["sess-2"] = Result{SessionID: "sess-2", Status: "running"}
	r.mu.Unlock()

	// Should reject disconnect
	_, err = r.DisconnectRuntime(ctx, "codex", "")
	if err == nil || !strings.Contains(err.Error(), "non-idle, running, or unconfirmed") {
		t.Fatalf("expected active sessions rejection, got: %v", err)
	}

	// Change sess-2 to idle
	r.mu.Lock()
	r.states["sess-2"] = Result{SessionID: "sess-2", Status: "idle"}
	r.mu.Unlock()

	released, err := r.DisconnectRuntime(ctx, "codex", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(released) != 2 {
		t.Fatalf("expected 2 released sessions, got %v", released)
	}
	if !ad.stopped {
		t.Fatalf("expected adapter to be closed")
	}

	// Verify registry no longer owns sess-1 or sess-2
	if len(r.Sessions()) != 0 {
		t.Fatalf("expected 0 sessions after disconnect, got %v", r.Sessions())
	}
}
