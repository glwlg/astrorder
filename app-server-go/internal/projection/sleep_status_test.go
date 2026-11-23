package projection

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"astrorder.dev/app-server/internal/store"
)

func TestSleptSessionIgnoresErrorProjectionAndHealsHistoricalRows(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "sleep.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()

	_, _ = st.UpsertAgent(ctx, map[string]any{"id": "ssh-codex", "kind": "codex", "name": "Codex", "status": "ready", "capabilities": []string{"chat"}})
	_, _ = st.UpsertSession(ctx, map[string]any{"id": "slept-1", "agent_id": "ssh-codex", "title": "休眠会话", "status": "idle", "updated_at": "2026-11-23T10:00:00Z"})
	_, _ = st.UpsertSession(ctx, map[string]any{"id": "active-1", "agent_id": "ssh-codex", "title": "活跃会话", "status": "idle", "updated_at": "2026-11-23T10:00:00Z"})
	_, _ = st.UpsertSession(ctx, map[string]any{"id": "legacy-slept", "agent_id": "ssh-codex", "title": "历史休眠残留", "status": "error", "updated_at": "2026-11-20T10:00:00Z"})

	if err := st.SetPreference(ctx, `session_sleep:["ssh-codex","slept-1"]`, true, true); err != nil {
		t.Fatal(err)
	}
	if err := st.SetPreference(ctx, `session_sleep:["ssh-codex","legacy-slept"]`, true, true); err != nil {
		t.Fatal(err)
	}

	// Project session.error on slept-1 -> must stay idle!
	if err := st.WithTx(ctx, func(tx *sql.Tx) error {
		_, projErr := projectSimpleSessionStatusTx(ctx, tx, "slept-1", "ssh-codex", "error", "session.error", map[string]any{"agent_id": "ssh-codex", "session_id": "slept-1"})
		return projErr
	}); err != nil {
		t.Fatal(err)
	}
	gotSlept, _ := st.GetSession(ctx, "ssh-codex", "slept-1")
	if gotSlept["status"] != "idle" {
		t.Fatalf("expected slept session to remain idle, got %v", gotSlept["status"])
	}

	// Project session.error on active-1 (not slept) -> must still transition to error!
	if err := st.WithTx(ctx, func(tx *sql.Tx) error {
		_, projErr := projectSimpleSessionStatusTx(ctx, tx, "active-1", "ssh-codex", "error", "session.error", map[string]any{"agent_id": "ssh-codex", "session_id": "active-1"})
		return projErr
	}); err != nil {
		t.Fatal(err)
	}
	gotActive, _ := st.GetSession(ctx, "ssh-codex", "active-1")
	if gotActive["status"] != "error" {
		t.Fatalf("expected non-slept session to become error, got %v", gotActive["status"])
	}

	// Startup heal should fix legacy-slept without touching active-1!
	healed, err := st.HealSleptErrorSessions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if healed != 1 {
		t.Fatalf("expected 1 healed slept session, got %d", healed)
	}
	gotLegacy, _ := st.GetSession(ctx, "ssh-codex", "legacy-slept")
	gotActiveAfter, _ := st.GetSession(ctx, "ssh-codex", "active-1")
	if gotLegacy["status"] != "idle" || gotActiveAfter["status"] != "error" {
		t.Fatalf("unexpected statuses after heal: legacy=%v active=%v", gotLegacy["status"], gotActiveAfter["status"])
	}
}
