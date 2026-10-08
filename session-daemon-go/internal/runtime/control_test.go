package runtime

import (
	"context"
	"testing"
)

type controlAdapter struct{}

func (controlAdapter) Create(_ context.Context, r Request) (Result, error) {
	return Result{SessionID: r.SessionID, Status: "idle", Payload: map[string]any{"created": true}}, nil
}
func (controlAdapter) Spawn(_ context.Context, r Request) (Result, error) {
	return Result{SessionID: r.SessionID, Status: "idle"}, nil
}
func (controlAdapter) Command(_ context.Context, r Request) (Result, error) {
	return Result{SessionID: r.SessionID, Status: "running", Payload: map[string]any{"accepted": true}}, nil
}
func TestRegistryRoutesCommandsByOwnedSession(t *testing.T) {
	r := NewRegistry()
	r.Register("test", controlAdapter{})
	if _, err := r.Spawn(context.Background(), Request{AgentType: "test", SessionID: "a"}); err != nil {
		t.Fatal(err)
	}
	result, err := r.Command(context.Background(), Request{SessionID: "a", Action: "session.send"})
	if err != nil || result.Status != "running" {
		t.Fatal(result, err)
	}
	if _, err = r.Command(context.Background(), Request{SessionID: "missing", Action: "session.send"}); err == nil {
		t.Fatal("unowned command accepted")
	}
	if _, err = r.Spawn(context.Background(), Request{AgentType: "other", SessionID: "a"}); err == nil {
		t.Fatal("foreign attach accepted")
	}
	if r.Statuses()["test"]["registered"] != true {
		t.Fatal("registration status false")
	}
}
