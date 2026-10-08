package runtime

import (
	"context"
	"fmt"
	"testing"
)

type recoverableAdapter struct {
	status string
	spawns int
	fail   bool
}

func (a *recoverableAdapter) Create(ctx context.Context, r Request) (Result, error) {
	return a.Spawn(ctx, r)
}
func (a *recoverableAdapter) Spawn(_ context.Context, r Request) (Result, error) {
	a.spawns++
	if a.fail {
		return Result{}, fmt.Errorf("native owner not confirmed")
	}
	a.status = "idle"
	return Result{SessionID: r.SessionID, Status: "idle"}, nil
}
func (a *recoverableAdapter) Snapshot() map[string]Result {
	return map[string]Result{"owned": {SessionID: "owned", Status: a.status}}
}
func TestRegistrySpawnDoesNotReturnStaleIdleAfterNativeFailure(t *testing.T) {
	r := NewRegistry()
	a := &recoverableAdapter{}
	r.Register("test", a)
	req := Request{AgentType: "test", SessionID: "owned"}
	if _, err := r.Spawn(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	a.status = "running"
	res, err := r.Spawn(context.Background(), req)
	if err != nil || res.Status != "running" || a.spawns != 1 {
		t.Fatal("existing ownership did not read authoritative activity", res, err, a.spawns)
	}
	a.status = "error"
	a.fail = true
	if _, err := r.Spawn(context.Background(), req); err == nil {
		t.Fatal("spawn returned cached idle for failed native owner")
	}
	if a.spawns != 2 {
		t.Fatal("native recovery was not attempted")
	}
	a.fail = false
	if _, err := r.Spawn(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	if r.Busy() {
		t.Fatal("confirmed idle recovery did not reconcile registry")
	}
}
