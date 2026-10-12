package runtime

import (
	"context"
	"errors"
	"testing"
)

type readIdentityAdapter struct{ calls int }

func (a *readIdentityAdapter) Create(context.Context, Request) (Result, error) {
	return Result{SessionID: "owned", Status: "running", Payload: map[string]any{"agent_id": "agent"}}, nil
}
func (a *readIdentityAdapter) Command(_ context.Context, r Request) (Result, error) {
	a.calls++
	payload := map[string]any{"items": []any{}}
	if r.Action == "session.close" {
		payload["closed"] = true
	}
	return Result{SessionID: r.SessionID, Status: "idle", Payload: payload}, nil
}
func TestSessionReadIdentitySurvivesCommandReceiptAndDisappearsWithOwner(t *testing.T) {
	registry := NewRegistry()
	a := &readIdentityAdapter{}
	registry.Register("grok", a)
	ctx := context.Background()
	if _, err := registry.Create(ctx, Request{AgentType: "grok"}); err != nil {
		t.Fatal(err)
	}
	agent := "agent"
	wrong := "wrong"
	request := Request{SessionID: "owned", Action: "session.models"}
	if _, _, err := registry.ReadSession(ctx, request, SessionReadIdentity{AgentID: &wrong}); !errors.Is(err, ErrSessionIdentity) {
		t.Fatal(err)
	}
	if a.calls != 0 {
		t.Fatal("mismatched read dispatched")
	}
	if _, _, err := registry.ReadSession(ctx, request, SessionReadIdentity{AgentID: &agent}); err != nil {
		t.Fatal(err)
	}
	if registry.Sessions()["owned"].Status != "running" {
		t.Fatal("read-only idle receipt overwrote activity")
	}
	if _, err := registry.Command(ctx, request); err != nil {
		t.Fatal(err)
	}
	if _, _, err := registry.ReadSession(ctx, request, SessionReadIdentity{AgentID: &agent}); err != nil {
		t.Fatalf("command receipt erased binding identity: %v", err)
	}
	request.Action = "session.close"
	if _, err := registry.Command(ctx, request); err != nil {
		t.Fatal(err)
	}
	if _, exists := registry.identities["owned"]; exists {
		t.Fatal("retired owner retained identity")
	}
	request.Action = "session.models"
	if _, _, err := registry.ReadSession(ctx, request, SessionReadIdentity{}); !errors.Is(err, ErrSessionNotOwned) {
		t.Fatalf("retired read: %v", err)
	}
}

type snapshotReadChild struct{ snapshotChild }

func (c *snapshotReadChild) Command(_ context.Context, request Request) (Result, error) {
	return Result{SessionID: request.SessionID, Status: c.status, Payload: map[string]any{"provider": "fixture", "model": "fixture"}}, nil
}

func TestSessionReadUsesRecordedSSHConnectionWithoutCreatingChild(t *testing.T) {
	registry := NewRegistry()
	child := &snapshotReadChild{snapshotChild: snapshotChild{status: "waiting_approval", dummyChild: dummyChild{connID: "debian"}}}
	starts := 0
	m := NewMultiplexRegistry(func(string, map[string]any) (Adapter, error) { starts++; return child, nil })
	registry.Register("ssh", m)
	defer registry.Close()
	ctx := context.Background()
	req := Request{AgentType: "ssh", SessionID: "owned", Fields: map[string]any{"params": map[string]any{"connection_id": "debian", "ssh_settings": map[string]any{"host": "synthetic.invalid"}}}}
	if _, err := registry.Spawn(ctx, req); err != nil {
		t.Fatal(err)
	}
	wrong, correct := "other", "debian"
	read := Request{SessionID: "owned", Action: "session.model.read"}
	if _, _, err := registry.ReadSession(ctx, read, SessionReadIdentity{ConnectionID: &wrong}); !errors.Is(err, ErrSessionIdentity) {
		t.Fatal(err)
	}
	if _, _, err := registry.ReadSession(ctx, read, SessionReadIdentity{ConnectionID: &correct}); err != nil {
		t.Fatal(err)
	}
	if starts != 1 || registry.Sessions()["owned"].Status != "waiting_approval" {
		t.Fatalf("remote read changed ownership: %d %+v", starts, registry.Sessions())
	}
}

func TestCodexSessionReadSupported(t *testing.T) {
	for _, kind := range []string{"codex", "codex-ssh"} {
		if !sessionReadSupported(kind, "session.model.read") {
			t.Fatalf("expected session.model.read to be supported for %s", kind)
		}
	}
}
