package runtime

import (
	"context"
	"sync"
	"testing"
	"time"
)

type blockedOwnerSnapshot struct {
	ownerSnapshotAdapter
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (a *blockedOwnerSnapshot) Spawn(ctx context.Context, request Request) (Result, error) {
	result, err := a.ownerSnapshotAdapter.Spawn(ctx, request)
	if request.Fields["fixture_running"] == true {
		result.Status = "running"
	}
	return result, err
}

func (a *blockedOwnerSnapshot) Snapshot() map[string]Result {
	a.once.Do(func() { close(a.entered); <-a.release })
	return map[string]Result{"rebound": {SessionID: "rebound", Status: "idle"}}
}

func TestAcceptanceLiveSnapshotDoesNotPairOldStateWithReboundIdentity(t *testing.T) {
	registry := NewRegistry()
	adapter := &blockedOwnerSnapshot{entered: make(chan struct{}), release: make(chan struct{})}
	registry.Register("codex-ssh", adapter)
	spawnOwned(t, registry, "rebound", "conn-old")
	done := make(chan map[string]LiveSessionBinding, 1)
	go func() { done <- registry.LiveSessionBindings() }()
	select {
	case <-adapter.entered:
	case <-time.After(3 * time.Second):
		t.Fatal("snapshot did not reach barrier")
	}
	var release sync.Once
	defer release.Do(func() { close(adapter.release) })
	if _, err := registry.Command(context.Background(), Request{SessionID: "rebound", Action: "session.close"}); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Spawn(context.Background(), Request{AgentType: "codex-ssh", SessionID: "rebound", Fields: map[string]any{"fixture_running": true, "params": map[string]any{"connection_id": "conn-new"}}}); err != nil {
		t.Fatal(err)
	}
	release.Do(func() { close(adapter.release) })
	var bindings map[string]LiveSessionBinding
	select {
	case bindings = <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("snapshot did not return")
	}
	if binding, exists := bindings["rebound"]; exists {
		if binding.Identity.ConnectionID == nil || *binding.Identity.ConnectionID != "conn-new" || binding.State.Status != "running" {
			t.Fatalf("authority paired stale state with a rebound identity: binding=%+v identity=%+v", binding, binding.Identity)
		}
	}
}

// A close+rebind that reuses the exact same identity values still starts a
// new binding generation. The stale adapter row from the previous generation
// must not be reported as this binding's state just because the identity
// values compare equal; the row must either be dropped or carry the fresh
// generation's state.
func TestAcceptanceLiveSnapshotDoesNotPairOldStateWithSameValueRebind(t *testing.T) {
	registry := NewRegistry()
	adapter := &blockedOwnerSnapshot{entered: make(chan struct{}), release: make(chan struct{})}
	registry.Register("codex-ssh", adapter)
	spawnOwned(t, registry, "rebound", "conn-old")
	done := make(chan map[string]LiveSessionBinding, 1)
	go func() { done <- registry.LiveSessionBindings() }()
	select {
	case <-adapter.entered:
	case <-time.After(3 * time.Second):
		t.Fatal("snapshot did not reach barrier")
	}
	var release sync.Once
	defer release.Do(func() { close(adapter.release) })
	if _, err := registry.Command(context.Background(), Request{SessionID: "rebound", Action: "session.close"}); err != nil {
		t.Fatal(err)
	}
	// Rebind with identical identity values (same connection): only the
	// binding generation changed.
	if _, err := registry.Spawn(context.Background(), Request{AgentType: "codex-ssh", SessionID: "rebound", Fields: map[string]any{"fixture_running": true, "params": map[string]any{"connection_id": "conn-old"}}}); err != nil {
		t.Fatal(err)
	}
	release.Do(func() { close(adapter.release) })
	var bindings map[string]LiveSessionBinding
	select {
	case bindings = <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("snapshot did not return")
	}
	if binding, exists := bindings["rebound"]; exists {
		if binding.Identity.ConnectionID == nil || *binding.Identity.ConnectionID != "conn-old" || binding.State.Status != "running" {
			t.Fatalf("authority paired stale state with a same-value rebind: binding=%+v identity=%+v", binding, binding.Identity)
		}
	}
}

// noIdentityRebindAdapter models a runtime whose sessions carry no identity
// fields at all (e.g. the local pty runtime): there is no identity value or
// pointer that can prove the binding generation, so only the registry's own
// binding-generation counter can distinguish a close+rebind from the
// original binding.
type noIdentityRebindAdapter struct {
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (a *noIdentityRebindAdapter) Create(context.Context, Request) (Result, error) {
	return Result{Status: "idle", SessionID: "created"}, nil
}

func (a *noIdentityRebindAdapter) Spawn(_ context.Context, request Request) (Result, error) {
	return Result{SessionID: request.SessionID, Status: "running"}, nil
}

func (a *noIdentityRebindAdapter) Command(_ context.Context, request Request) (Result, error) {
	payload := map[string]any{}
	if request.Action == "session.close" {
		payload["closed"] = true
	}
	return Result{SessionID: request.SessionID, Status: "idle", Payload: payload}, nil
}

func (a *noIdentityRebindAdapter) Snapshot() map[string]Result {
	a.once.Do(func() { close(a.entered); <-a.release })
	return map[string]Result{"rebound": {SessionID: "rebound", Status: "idle"}}
}

// A close+rebind of a session whose identity fields are all nil cannot be
// detected by comparing identity values or pointers: the stale adapter row
// from the previous binding must still not be reported as the new binding's
// state, so the registry's binding-generation counter must catch it.
func TestAcceptanceLiveSnapshotDoesNotPairOldStateWithNoIdentityRebind(t *testing.T) {
	registry := NewRegistry()
	adapter := &noIdentityRebindAdapter{entered: make(chan struct{}), release: make(chan struct{})}
	registry.Register("pty", adapter)
	if _, err := registry.Spawn(context.Background(), Request{AgentType: "pty", SessionID: "rebound"}); err != nil {
		t.Fatal(err)
	}
	done := make(chan map[string]LiveSessionBinding, 1)
	go func() { done <- registry.LiveSessionBindings() }()
	select {
	case <-adapter.entered:
	case <-time.After(3 * time.Second):
		t.Fatal("snapshot did not reach barrier")
	}
	var release sync.Once
	defer release.Do(func() { close(adapter.release) })
	if _, err := registry.Command(context.Background(), Request{SessionID: "rebound", Action: "session.close"}); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Spawn(context.Background(), Request{AgentType: "pty", SessionID: "rebound"}); err != nil {
		t.Fatal(err)
	}
	release.Do(func() { close(adapter.release) })
	var bindings map[string]LiveSessionBinding
	select {
	case bindings = <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("snapshot did not return")
	}
	if binding, exists := bindings["rebound"]; exists {
		if binding.Identity.AgentID != nil || binding.Identity.ConnectionID != nil || binding.State.Status != "running" {
			t.Fatalf("authority paired stale state with a no-identity rebind: binding=%+v identity=%+v", binding, binding.Identity)
		}
	}
}
