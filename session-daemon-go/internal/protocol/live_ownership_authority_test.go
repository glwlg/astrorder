package protocol

import (
	"context"
	"path/filepath"
	"sync"
	"testing"

	core "astrorder.dev/session-daemon/internal/runtime"
)

// authorityOwnerAdapter is a synthetic multiplexed runtime used to prove the
// authority snapshot distinguishes live ownership from durable journal rows.
type authorityOwnerAdapter struct {
	mu       sync.Mutex
	sessions map[string]string // sessionID -> connectionID
}

func (a *authorityOwnerAdapter) Create(context.Context, core.Request) (core.Result, error) {
	return core.Result{Status: "idle", SessionID: "created"}, nil
}

func (a *authorityOwnerAdapter) Spawn(_ context.Context, request core.Request) (core.Result, error) {
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
	return core.Result{SessionID: request.SessionID, Status: "idle", Payload: map[string]any{"agent_id": request.SessionID}}, nil
}

func (a *authorityOwnerAdapter) SessionsForConnection(connectionID string) []string {
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

func (a *authorityOwnerAdapter) DisconnectConnection(_ context.Context, connectionID string) ([]string, error) {
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

// A released connection keeps its durable journal row but must stop being
// reported as live-owned; the other connection's binding stays live with its
// own kind and identity.
func TestAuthorityStatusDistinguishesLiveOwnershipFromJournal(t *testing.T) {
	daemon, err := Open("fixture", filepath.Join(t.TempDir(), "authority.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer daemon.Close()
	daemon.RegisterRuntime("codex-ssh", &authorityOwnerAdapter{})
	ctx := context.Background()
	for _, session := range []struct{ id, conn string }{
		{"probe-codex-saved", "saved"},
		{"probe-codex-other", "other"},
	} {
		response := daemon.control(ctx, map[string]any{
			"action": "session.spawn", "agent_type": "codex-ssh", "session_id": session.id,
			"params": map[string]any{"connection_id": session.conn, "ssh_settings": map[string]any{"host": "synthetic.invalid"}},
		})
		if response["action"] != "session.spawn.result" {
			t.Fatalf("spawn %s failed: %#v", session.id, response)
		}
	}
	status, err := daemon.AuthorityStatus()
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]SessionAuthority{}
	for _, session := range status.Sessions {
		byID[session.SessionID] = session
	}
	for _, want := range []struct{ id, conn string }{{"probe-codex-saved", "saved"}, {"probe-codex-other", "other"}} {
		session, exists := byID[want.id]
		if !exists {
			t.Fatalf("live session %s missing from authority: %+v", want.id, status.Sessions)
		}
		if !session.RuntimeOwned || session.RuntimeKind != "codex-ssh" || session.Status != "idle" {
			t.Fatalf("live session %s authority = %+v", want.id, session)
		}
		if session.Identity.ConnectionID == nil || *session.Identity.ConnectionID != want.conn || session.Identity.AgentID == nil || *session.Identity.AgentID != want.id {
			t.Fatalf("live session %s identity = %+v, want connection %s", want.id, session.Identity, want.conn)
		}
	}

	// Release only the saved connection's runtime, as a scoped disconnect does.
	response := daemon.control(ctx, map[string]any{"action": "runtime.disconnect", "agent_type": "codex-ssh", "connection_id": "saved"})
	if response["action"] != "runtime.disconnect.result" {
		t.Fatalf("disconnect failed: %#v", response)
	}
	status, err = daemon.AuthorityStatus()
	if err != nil {
		t.Fatal(err)
	}
	byID = map[string]SessionAuthority{}
	for _, session := range status.Sessions {
		byID[session.SessionID] = session
	}
	released, exists := byID["probe-codex-saved"]
	if !exists {
		t.Fatalf("released session lost its durable journal row: %+v", status.Sessions)
	}
	if released.RuntimeOwned || released.RuntimeKind != "" {
		t.Fatalf("released session still claims live ownership: %+v", released)
	}
	if released.Identity.AgentID != nil || released.Identity.ConnectionID != nil {
		t.Fatalf("released session borrowed an identity: %+v", released.Identity)
	}
	live, exists := byID["probe-codex-other"]
	if !exists || !live.RuntimeOwned || live.RuntimeKind != "codex-ssh" {
		t.Fatalf("other connection's live binding was lost: %+v", status.Sessions)
	}
	if live.Identity.ConnectionID == nil || *live.Identity.ConnectionID != "other" {
		t.Fatalf("other connection's identity changed: %+v", live.Identity)
	}
}
