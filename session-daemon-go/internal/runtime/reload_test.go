package runtime

import (
	"context"
	"errors"
	"testing"
	"time"
)

type otherReloadFixture struct{}

func (*otherReloadFixture) Create(context.Context, Request) (Result, error) {
	return Result{SessionID: "other-owned", Status: "idle"}, nil
}

type reloadFixture struct {
	status           string
	calls            int
	entered, release chan struct{}
}

func (a *reloadFixture) Create(context.Context, Request) (Result, error) {
	return Result{SessionID: "reload-owned", Status: a.status}, nil
}
func (a *reloadFixture) Spawn(_ context.Context, r Request) (Result, error) {
	return Result{SessionID: r.SessionID, Status: a.status}, nil
}
func (a *reloadFixture) Command(_ context.Context, r Request) (Result, error) {
	return Result{SessionID: r.SessionID, Status: a.status}, nil
}
func (a *reloadFixture) Snapshot() map[string]Result {
	return map[string]Result{"reload-owned": {SessionID: "reload-owned", Status: a.status}}
}
func (a *reloadFixture) ReloadConfig(ctx context.Context) error {
	a.calls++
	if a.entered != nil {
		close(a.entered)
		select {
		case <-a.release:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}
func TestRegistryReloadRequiresAuthoritativeIdle(t *testing.T) {
	for _, status := range []string{"running", "waiting_approval", "error"} {
		r := NewRegistry()
		a := &reloadFixture{status: status}
		r.Register("codex", a)
		if _, err := r.Create(context.Background(), Request{AgentType: "codex"}); err != nil {
			t.Fatal(err)
		}
		if err := r.ReloadConfig(context.Background(), "codex"); !errors.Is(err, ErrRuntimeBusy) || a.calls != 0 {
			t.Fatal("reload ignored native activity", status, err, a.calls)
		}
		a.status = "idle"
		if err := r.ReloadConfig(context.Background(), "codex"); err != nil || a.calls != 1 {
			t.Fatal("confirmed idle reload failed", err, a.calls)
		}
	}
}
func TestRegistryReloadLeaseIsPerRuntime(t *testing.T) {
	r := NewRegistry()
	a := &reloadFixture{status: "idle", entered: make(chan struct{}), release: make(chan struct{})}
	r.Register("codex", a)
	r.Register("other", &otherReloadFixture{})
	r.Create(context.Background(), Request{AgentType: "codex"})
	done := make(chan error, 1)
	go func() { done <- r.ReloadConfig(context.Background(), "codex") }()
	select {
	case <-a.entered:
	case <-time.After(time.Second):
		t.Fatal("reload blocked")
	}
	defer close(a.release)
	if _, err := r.Create(context.Background(), Request{AgentType: "codex"}); !errors.Is(err, ErrRuntimeBusy) {
		t.Fatal("create raced reload", err)
	}
	if _, err := r.Command(context.Background(), Request{SessionID: "reload-owned", Action: "session.send"}); !errors.Is(err, ErrRuntimeBusy) {
		t.Fatal("command raced reload", err)
	}
	if _, err := r.Spawn(context.Background(), Request{AgentType: "codex", SessionID: "reload-owned"}); !errors.Is(err, ErrRuntimeBusy) {
		t.Fatal("spawn raced reload", err)
	}
	if _, err := r.Create(context.Background(), Request{AgentType: "other"}); err != nil {
		t.Fatal("reload held global registry lock", err)
	}
	// The test joins the goroutine after releasing the lease.
	a.release <- struct{}{}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
