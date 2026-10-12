package application

import (
	"astrorder.dev/app-server/internal/store"
	"context"
	"path/filepath"
	"testing"
)

func TestCommittedSessionDeletionClosesExactBrowserResourcesOnce(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	hub := NewEventHub(st)
	calls := [][2]string{}
	hub.SetSessionCleanup(func(ctx context.Context, agentID, sessionID string) {
		calls = append(calls, [2]string{agentID, sessionID})
	})
	e, err := st.AppendEvent(ctx, store.Event{ID: "delete", Type: "session.delete", AgentID: " a ", SessionID: " s ", Data: map[string]any{"agent_id": " a ", "id": " s "}})
	if err != nil {
		t.Fatal(err)
	}
	hub.PublishCommitted([]store.Event{*e})
	hub.PublishCommitted([]store.Event{*e})
	if len(calls) != 1 || calls[0] != [2]string{" a ", " s "} {
		t.Fatalf("cleanup identity/duplicate=%v", calls)
	}
	if err := hub.Publish(ctx, "session.upsert", map[string]any{"agent_id": "a", "id": "s"}); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 1 {
		t.Fatal("nondeletion closed browser")
	}
}
