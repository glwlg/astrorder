package runtime_test

import (
	core "astrorder.dev/session-daemon/internal/runtime"
	"context"
	"errors"
	"sync/atomic"
	"testing"
)

type closeFailureAdapter struct {
	idleOwnerAdapter
	attempts *atomic.Int32
}

func (a closeFailureAdapter) Close() error {
	a.attempts.Add(1)
	return errors.New("native cleanup failed")
}
func TestRegistryCleanupAttemptsAllAdaptersDespiteFailures(t *testing.T) {
	registry := core.NewRegistry()
	var attempts atomic.Int32
	registry.Register("one", closeFailureAdapter{attempts: &attempts})
	registry.Register("two", closeFailureAdapter{attempts: &attempts})
	if err := registry.Close(); err == nil {
		t.Fatal("cleanup error was discarded")
	}
	if attempts.Load() != 2 {
		t.Fatal("an adapter cleanup failure skipped another owned runtime")
	}
}

type idleOwnerAdapter struct{}

func (idleOwnerAdapter) Create(context.Context, core.Request) (core.Result, error) {
	return core.Result{SessionID: "native-boundary", Status: "idle"}, nil
}

type observedStateAdapter struct {
	idleOwnerAdapter
	status string
}

func (a observedStateAdapter) Snapshot() map[string]core.Result {
	return map[string]core.Result{"native-boundary": {SessionID: "native-boundary", Status: a.status}}
}
func TestRegistryUnknownNativeStateRequiresMaintenanceConfirmation(t *testing.T) {
	for _, status := range []string{"error", "", "stopping"} {
		t.Run("status="+status, func(t *testing.T) {
			registry := core.NewRegistry()
			registry.Register("pty", observedStateAdapter{status: status})
			if _, err := registry.Create(context.Background(), core.Request{AgentType: "pty"}); err != nil {
				t.Fatal(err)
			}
			if !registry.Busy() {
				t.Fatal("uncertain native state was treated as safe for unconfirmed shutdown")
			}
		})
	}
}
func TestRegistrySnapshotCannotOverwriteAnotherRuntimeOwnership(t *testing.T) {
	registry := core.NewRegistry()
	registry.Register("owner", idleOwnerAdapter{})
	registry.Register("foreign", observedStateAdapter{status: "running"})
	if _, err := registry.Create(context.Background(), core.Request{AgentType: "owner"}); err != nil {
		t.Fatal(err)
	}
	if registry.Sessions()["native-boundary"].Status != "idle" {
		t.Fatal("foreign snapshot changed another runtime's authoritative state")
	}
	if registry.Busy() {
		t.Fatal("foreign state fabricated native activity")
	}
}
