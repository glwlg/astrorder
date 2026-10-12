package runtime

import (
	"context"
	"sync"
	"testing"
)

// ownerSnapshotAdapter is a synthetic multiplexed live runtime: Spawn binds a
// session to a connection, and it can release one connection's sessions, so
// tests exercise real registry ownership without starting any native process.
type ownerSnapshotAdapter struct {
	mu       sync.Mutex
	sessions map[string]string // sessionID -> connectionID
}

func (a *ownerSnapshotAdapter) Create(context.Context, Request) (Result, error) {
	return Result{Status: "idle", SessionID: "created"}, nil
}

func (a *ownerSnapshotAdapter) Spawn(_ context.Context, request Request) (Result, error) {
	connID := ""
	if params, ok := request.Fields["params"].(map[string]any); ok {
		connID, _ = params["connection_id"].(string)
	}
	a.mu.Lock()
	if a.sessions == nil {
		a.sessions = map[string]string{}
	}
	a.sessions[request.SessionID] = connID
	a.mu.Unlock()
	return Result{SessionID: request.SessionID, Status: "idle", Payload: map[string]any{"agent_id": request.SessionID}}, nil
}

func (a *ownerSnapshotAdapter) Command(_ context.Context, request Request) (Result, error) {
	payload := map[string]any{}
	if request.Action == "session.close" {
		payload["closed"] = true
	}
	return Result{SessionID: request.SessionID, Status: "idle", Payload: payload}, nil
}

func (a *ownerSnapshotAdapter) SessionsForConnection(connectionID string) []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	var out []string
	for id, conn := range a.sessions {
		if conn == connectionID {
			out = append(out, id)
		}
	}
	return out
}

func (a *ownerSnapshotAdapter) DisconnectConnection(_ context.Context, connectionID string) ([]string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	var out []string
	for id, conn := range a.sessions {
		if conn == connectionID {
			out = append(out, id)
			delete(a.sessions, id)
		}
	}
	return out, nil
}

func spawnOwned(t *testing.T, registry *Registry, sessionID, connID string) {
	t.Helper()
	request := Request{
		AgentType: "codex-ssh",
		SessionID: sessionID,
		Fields:    map[string]any{"params": map[string]any{"connection_id": connID, "ssh_settings": map[string]any{"host": "synthetic.invalid"}}},
	}
	if _, err := registry.Spawn(context.Background(), request); err != nil {
		t.Fatal(err)
	}
}

// Two connections own one session each; releasing one must drop exactly that
// session from the live snapshot while the other connection's binding stays
// visible with its own kind and identity.
func TestLiveSessionBindingsSeparateConnectionsAndRelease(t *testing.T) {
	registry := NewRegistry()
	registry.Register("codex-ssh", &ownerSnapshotAdapter{})
	spawnOwned(t, registry, "owned-a", "conn-a")
	spawnOwned(t, registry, "owned-b", "conn-b")

	bindings := registry.LiveSessionBindings()
	if len(bindings) != 2 {
		t.Fatalf("live bindings = %d, want 2: %+v", len(bindings), bindings)
	}
	for _, session := range []struct{ id, conn string }{{"owned-a", "conn-a"}, {"owned-b", "conn-b"}} {
		binding, exists := bindings[session.id]
		if !exists || binding.Kind != "codex-ssh" || binding.State.Status != "idle" {
			t.Fatalf("binding %s = %+v exists=%v", session.id, binding, exists)
		}
		if binding.Identity.ConnectionID == nil || *binding.Identity.ConnectionID != session.conn {
			t.Fatalf("binding %s identity = %+v, want connection %s", session.id, binding.Identity, session.conn)
		}
	}

	// Release only connection A's runtime, as a scoped disconnect would.
	if _, err := registry.DisconnectRuntime(context.Background(), "codex-ssh", "conn-a"); err != nil {
		t.Fatal(err)
	}
	bindings = registry.LiveSessionBindings()
	if _, exists := bindings["owned-a"]; exists {
		t.Fatalf("released session still live: %+v", bindings["owned-a"])
	}
	binding, exists := bindings["owned-b"]
	if !exists || binding.Kind != "codex-ssh" {
		t.Fatalf("other connection's binding was lost: %+v", bindings)
	}
	if binding.Identity.ConnectionID == nil || *binding.Identity.ConnectionID != "conn-b" {
		t.Fatalf("other connection's identity changed: %+v", binding.Identity)
	}
}

// A session whose runtime was closed through a command must disappear from
// the live snapshot entirely; a journal row is not part of this API.
func TestLiveSessionBindingsDropClosedSession(t *testing.T) {
	registry := NewRegistry()
	registry.Register("codex-ssh", &ownerSnapshotAdapter{})
	spawnOwned(t, registry, "owned", "conn-a")
	if _, err := registry.Command(context.Background(), Request{SessionID: "owned", Action: "session.close"}); err != nil {
		t.Fatal(err)
	}
	if bindings := registry.LiveSessionBindings(); len(bindings) != 0 {
		t.Fatalf("closed session still live: %+v", bindings)
	}
}

// An unregistered/unknown kind can never appear in the snapshot because only
// registered owners can bind in the first place; this guards the fail-closed
// expectation that unknown kinds are simply absent rather than fabricated.
func TestLiveSessionBindingsUnknownKindAbsent(t *testing.T) {
	registry := NewRegistry()
	if bindings := registry.LiveSessionBindings(); len(bindings) != 0 {
		t.Fatalf("empty registry returned bindings: %+v", bindings)
	}
	if _, err := registry.Spawn(context.Background(), Request{AgentType: "not-registered", SessionID: "x"}); err == nil {
		t.Fatal("unregistered kind spawned")
	}
	if bindings := registry.LiveSessionBindings(); len(bindings) != 0 {
		t.Fatalf("failed spawn leaked a binding: %+v", bindings)
	}
}

// While a spawn is still opening, the session must not be reported live even
// though the owner is recorded: the opening flag gates the snapshot.
func TestLiveSessionBindingsExcludeOpeningSession(t *testing.T) {
	registry := NewRegistry()
	started := make(chan struct{})
	release := make(chan struct{})
	registry.Register("slow", &blockingOwnerAdapter{started: started, release: release})
	done := make(chan error, 1)
	go func() {
		_, err := registry.Spawn(context.Background(), Request{AgentType: "slow", SessionID: "opening"})
		done <- err
	}()
	<-started
	registry.mu.Lock()
	registry.owners["opening"] = "slow"
	registry.opening["opening"] = true
	registry.mu.Unlock()
	if bindings := registry.LiveSessionBindings(); len(bindings) != 0 {
		t.Fatalf("opening session was reported live: %+v", bindings)
	}
	registry.mu.Lock()
	delete(registry.owners, "opening")
	delete(registry.opening, "opening")
	registry.mu.Unlock()
	close(release)
	if err := <-done; err == nil {
		// Spawn now fails after the forced cleanup; any error is fine.
		t.Log("spawn unexpectedly succeeded; snapshot assertions still hold")
	}
}

// The returned identities must be deep copies: mutating a binding returned
// to a caller must never leak into the registry's stored identity, so a
// later snapshot still reports the original owner identity.
func TestLiveSessionBindingsReturnsDeepCopiedIdentity(t *testing.T) {
	registry := NewRegistry()
	registry.Register("codex-ssh", &ownerSnapshotAdapter{})
	spawnOwned(t, registry, "owned", "conn-a")

	bindings := registry.LiveSessionBindings()
	binding, exists := bindings["owned"]
	if !exists || binding.Identity.ConnectionID == nil {
		t.Fatalf("binding missing: %+v exists=%v", binding, exists)
	}
	*binding.Identity.ConnectionID = "conn-mutated"

	again := registry.LiveSessionBindings()
	if value := again["owned"].Identity.ConnectionID; value == nil || *value != "conn-a" {
		t.Fatalf("caller mutation leaked into the registry identity: %+v", again["owned"].Identity)
	}
}

type blockingOwnerAdapter struct {
	started chan struct{}
	release chan struct{}
}

func (a *blockingOwnerAdapter) Create(context.Context, Request) (Result, error) {
	return Result{Status: "idle", SessionID: "created"}, nil
}

func (a *blockingOwnerAdapter) Spawn(_ context.Context, request Request) (Result, error) {
	close(a.started)
	<-a.release
	return Result{SessionID: request.SessionID, Status: "idle"}, nil
}
