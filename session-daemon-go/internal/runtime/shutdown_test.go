package runtime

import (
	"context"
	"testing"
)

type shutdownAdapter struct{ calls int }

func (a *shutdownAdapter) Create(context.Context, Request) (Result, error) {
	a.calls++
	return Result{SessionID: "new", Status: "idle"}, nil
}

func TestShutdownAdmissionRejectsNewWorkAndCanBeReleased(t *testing.T) {
	registry := NewRegistry()
	adapter := &shutdownAdapter{}
	registry.Register("fixture", adapter)
	if err := registry.BeginShutdown(false); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Create(context.Background(), Request{AgentType: "fixture"}); err == nil || adapter.calls != 0 {
		t.Fatal("draining registry admitted new work")
	}
	registry.CancelShutdown()
	if _, err := registry.Create(context.Background(), Request{AgentType: "fixture"}); err != nil || adapter.calls != 1 {
		t.Fatalf("cancelled drain remained locked: %v calls=%d", err, adapter.calls)
	}
}
