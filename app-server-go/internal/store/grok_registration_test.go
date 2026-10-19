package store

import (
	"context"
	"path/filepath"
	"testing"
)

func TestRepairGrokRegistrationsPreservesIdentityAndSessions(t *testing.T) {
	ctx := context.Background()
	st, err := Open(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if _, err := st.SaveSSHConnection(ctx, map[string]any{"id": "saved", "display_name": "Debian", "host": "example.invalid", "user": "tester"}); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"local-grok", "ssh-grok-saved", "ssh-grok-missing", "ssh-grok-saved-extra"} {
		if _, err := st.UpsertAgent(ctx, map[string]any{"id": id, "kind": "grok", "name": "Grok Build", "status": "ready"}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := st.UpsertSession(ctx, map[string]any{"id": "native-id", "agent_id": "ssh-grok-saved", "connection_id": "saved", "title": "existing", "status": "idle", "updated_at": "2026-10-16T00:00:00Z"}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := st.RepairGrokRegistrations(ctx); err != nil {
			t.Fatal(err)
		}
	}
	agents, err := st.ListAgents(ctx)
	if err != nil || len(agents) != 4 {
		t.Fatalf("catalog changed: %v %v", agents, err)
	}
	for _, agent := range agents {
		if agent["id"] == "ssh-grok-saved" {
			if agent["connection_id"] != "saved" || agent["name"] != "Debian · Grok" {
				t.Fatalf("not repaired: %v", agent)
			}
		} else if agent["connection_id"] != nil {
			t.Fatalf("unproven registration changed: %v", agent)
		}
	}
	session, err := st.GetSession(ctx, "ssh-grok-saved", "native-id")
	if err != nil || session == nil || session["connection_id"] != "saved" {
		t.Fatalf("session changed: %v %v", session, err)
	}
}
