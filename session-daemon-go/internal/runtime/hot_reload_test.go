package runtime

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
)

type hotSwapAdapter struct {
	version  string
	mu       sync.Mutex
	nextID   int
	sessions map[string]string
	closed   atomic.Bool
	commands atomic.Int32
}

func newHotSwapAdapter(version string) *hotSwapAdapter {
	return &hotSwapAdapter{
		version:  version,
		sessions: map[string]string{},
	}
}

func (a *hotSwapAdapter) Create(_ context.Context, req Request) (Result, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed.Load() {
		return Result{}, fmt.Errorf("adapter %s is closed", a.version)
	}
	a.nextID++
	id := fmt.Sprintf("%s-session-%d", a.version, a.nextID)
	status := "running"
	if s, ok := req.Fields["status"].(string); ok && s != "" {
		status = s
	}
	a.sessions[id] = status
	return Result{
		SessionID: id,
		Status:    status,
		Payload:   map[string]any{"adapter_version": a.version, "agent_id": "local-codex"},
	}, nil
}

func (a *hotSwapAdapter) Command(_ context.Context, req Request) (Result, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed.Load() {
		return Result{}, fmt.Errorf("adapter %s is closed", a.version)
	}
	st, ok := a.sessions[req.SessionID]
	if !ok {
		return Result{}, fmt.Errorf("session %s not owned by adapter %s", req.SessionID, a.version)
	}
	a.commands.Add(1)
	if req.Action == "session.close" {
		delete(a.sessions, req.SessionID)
		return Result{
			SessionID: req.SessionID,
			Status:    "idle",
			Payload:   map[string]any{"closed": true, "adapter_version": a.version},
		}, nil
	}
	if req.Action == "session.models" {
		return Result{
			SessionID: req.SessionID,
			Status:    st,
			Payload:   map[string]any{"adapter_version": a.version},
		}, nil
	}
	return Result{
		SessionID: req.SessionID,
		Status:    st,
		Payload:   map[string]any{"accepted": true, "adapter_version": a.version},
	}, nil
}

func (a *hotSwapAdapter) Snapshot() map[string]Result {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make(map[string]Result, len(a.sessions))
	for id, st := range a.sessions {
		out[id] = Result{
			SessionID: id,
			Status:    st,
			Payload:   map[string]any{"adapter_version": a.version},
		}
	}
	return out
}

func (a *hotSwapAdapter) OwnsSession(id string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	_, ok := a.sessions[id]
	return ok && !a.closed.Load()
}

func (a *hotSwapAdapter) Close() error {
	a.closed.Store(true)
	return nil
}

func TestRegistrySwapRuntimePreservesRunningSessionsAndAutoRetires(t *testing.T) {
	ctx := context.Background()
	reg := NewRegistry()

	v1 := newHotSwapAdapter("v1")
	var v1Retired atomic.Bool
	if _, err := reg.SwapRuntime("codex", v1, func() error {
		v1Retired.Store(true)
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	s1, err := reg.Create(ctx, Request{AgentType: "codex", Fields: map[string]any{"status": "running"}})
	if err != nil {
		t.Fatalf("create on v1 failed: %v", err)
	}

	// Swap to v2 while s1 is actively running on v1.
	v2 := newHotSwapAdapter("v2")
	preserved, err := reg.SwapRuntime("codex", v2, nil)
	if err != nil {
		t.Fatalf("SwapRuntime failed: %v", err)
	}
	if preserved != 1 {
		t.Fatalf("preserved = %d, want 1", preserved)
	}
	if v1.closed.Load() || v1Retired.Load() {
		t.Fatal("old adapter with active running session was closed prematurely")
	}

	// Commands and reads for s1 must still route to v1 seamlessly.
	cmdRes, err := reg.Command(ctx, Request{SessionID: s1.SessionID, Action: "session.send"})
	if err != nil {
		t.Fatalf("command on preserved session failed: %v", err)
	}
	if cmdRes.Payload["adapter_version"] != "v1" {
		t.Fatalf("s1 routed to %v, want v1", cmdRes.Payload["adapter_version"])
	}
	readRes, kind, err := reg.ReadSession(ctx, Request{SessionID: s1.SessionID, Action: "session.models"}, SessionReadIdentity{})
	if err != nil || kind != "codex" || readRes.Payload["adapter_version"] != "v1" {
		t.Fatalf("ReadSession on preserved session = (%+v, %q, %v)", readRes, kind, err)
	}

	// New session s2 must use the newly swapped v2 adapter.
	s2, err := reg.Create(ctx, Request{AgentType: "codex", Fields: map[string]any{"status": "idle"}})
	if err != nil {
		t.Fatalf("create on v2 failed: %v", err)
	}
	if s2.Payload["adapter_version"] != "v2" {
		t.Fatalf("s2 created on %v, want v2", s2.Payload["adapter_version"])
	}

	// Both s1 (v1) and s2 (v2) must be visible in Sessions, LiveSessionBindings, and OwnsAdapterSession.
	sessions := reg.Sessions()
	if sessions[s1.SessionID].Status != "running" || sessions[s2.SessionID].Status != "idle" {
		t.Fatalf("unexpected merged sessions snapshot: %+v", sessions)
	}
	bindings := reg.LiveSessionBindings()
	if len(bindings) != 2 || bindings[s1.SessionID].State.Status != "running" || bindings[s2.SessionID].State.Status != "idle" {
		t.Fatalf("unexpected LiveSessionBindings: %+v", bindings)
	}
	if !reg.OwnsAdapterSession("codex", s1.SessionID) || !reg.OwnsAdapterSession("codex", s2.SessionID) {
		t.Fatal("OwnsAdapterSession missed active or retired session")
	}
	if !reg.Busy() {
		t.Fatal("registry failed to report Busy for running session on retired adapter")
	}

	// Closing s1 automatically retires and closes v1, while s2 on v2 stays alive.
	if _, err := reg.Command(ctx, Request{SessionID: s1.SessionID, Action: "session.close"}); err != nil {
		t.Fatalf("closing s1 failed: %v", err)
	}
	if !v1.closed.Load() || !v1Retired.Load() {
		t.Fatal("retired v1 adapter was not auto-closed after its last session closed")
	}
	if v2.closed.Load() {
		t.Fatal("active v2 adapter was closed unexpectedly")
	}
	if _, err := reg.Command(ctx, Request{SessionID: s2.SessionID, Action: "session.send"}); err != nil {
		t.Fatalf("s2 on v2 broken after v1 retired: %v", err)
	}
	if err := reg.Close(); err != nil {
		t.Fatal(err)
	}
	if !v2.closed.Load() {
		t.Fatal("v2 was not closed on Registry.Close")
	}
}

func TestRegistryUnregisterRuntimePreservesActiveSessionsAndStatuses(t *testing.T) {
	ctx := context.Background()
	reg := NewRegistry()

	v1 := newHotSwapAdapter("v1")
	reg.Register("codex", v1)
	s1, err := reg.Create(ctx, Request{AgentType: "codex", Fields: map[string]any{"status": "running"}})
	if err != nil {
		t.Fatal(err)
	}

	preserved, removed, err := reg.UnregisterRuntime("codex")
	if err != nil || !removed || preserved != 1 {
		t.Fatalf("UnregisterRuntime = (%d, %v, %v)", preserved, removed, err)
	}
	if reg.HasRuntime("codex") {
		t.Fatal("codex still reported as registered after unregister")
	}
	if v1.closed.Load() {
		t.Fatal("unregistered adapter closed while session running")
	}

	// Statuses must not panic when a removed kind still owns live sessions.
	statuses := reg.Statuses()
	entry, ok := statuses["codex"]
	if !ok || entry["registered"] != false || entry["sessions"] != 1 {
		t.Fatalf("unexpected statuses for retired kind: %+v", statuses)
	}

	// New Create fails, while existing session commands continue seamlessly.
	if _, err := reg.Create(ctx, Request{AgentType: "codex"}); err == nil {
		t.Fatal("expected Create to fail on unregistered runtime")
	}
	if _, err := reg.Command(ctx, Request{SessionID: s1.SessionID, Action: "session.send"}); err != nil {
		t.Fatalf("existing session failed after unregister: %v", err)
	}
	if _, err := reg.Command(ctx, Request{SessionID: s1.SessionID, Action: "session.close"}); err != nil {
		t.Fatalf("closing session failed: %v", err)
	}
	if !v1.closed.Load() {
		t.Fatal("retired adapter did not close after final session unbound")
	}
}

func TestRegistrySwapRuntimeClosesIdleOldAdapterImmediately(t *testing.T) {
	reg := NewRegistry()
	v1 := newHotSwapAdapter("v1")
	var retired atomic.Bool
	if _, err := reg.SwapRuntime("codex", v1, func() error {
		retired.Store(true)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	v2 := newHotSwapAdapter("v2")
	preserved, err := reg.SwapRuntime("codex", v2, nil)
	if err != nil || preserved != 0 {
		t.Fatalf("SwapRuntime = (%d, %v)", preserved, err)
	}
	if !v1.closed.Load() || !retired.Load() {
		t.Fatal("idle old adapter was not closed immediately on swap")
	}
}

func TestRegistryConcurrentSwapAndOperationsRaceFree(t *testing.T) {
	ctx := context.Background()
	reg := NewRegistry()
	reg.Register("codex", newHotSwapAdapter("v0"))

	var wg sync.WaitGroup
	// Concurrent swaps
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 1; i <= 25; i++ {
			_, _ = reg.SwapRuntime("codex", newHotSwapAdapter(fmt.Sprintf("v%d", i)), nil)
		}
	}()

	// Concurrent session lifecycles & snapshots
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 15; i++ {
				res, err := reg.Create(ctx, Request{AgentType: "codex", Fields: map[string]any{"status": "running"}})
				if err != nil {
					continue
				}
				_, _ = reg.Command(ctx, Request{SessionID: res.SessionID, Action: "session.send"})
				_ = reg.Sessions()
				_ = reg.LiveSessionBindings()
				_ = reg.Statuses()
				_ = reg.OwnsAdapterSession("codex", res.SessionID)
				_, _ = reg.Command(ctx, Request{SessionID: res.SessionID, Action: "session.close"})
			}
		}()
	}
	wg.Wait()
	if err := reg.Close(); err != nil {
		t.Fatal(err)
	}
}
