package store

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

func TestSearchMessagesMultiTermAndPerSessionCap(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "search.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()

	if _, err := st.UpsertAgent(ctx, map[string]any{
		"id":           "agent-1",
		"kind":         "antigravity",
		"name":         "Antigravity",
		"status":       "ready",
		"capabilities": []string{"chat"},
	}); err != nil {
		t.Fatal(err)
	}
	for _, s := range []map[string]any{
		{"id": "sess-1", "agent_id": "agent-1", "title": "数据库迁移", "project_name": "OpsCore", "workspace": "/repo", "status": "idle", "updated_at": "2026-11-23T10:00:00Z"},
		{"id": "sess-2", "agent_id": "agent-1", "title": "临时子会话", "workspace": "/repo", "status": "idle", "ephemeral": true, "updated_at": "2026-11-23T10:05:00Z"},
	} {
		if _, err := st.UpsertSession(ctx, s); err != nil {
			t.Fatal(err)
		}
	}

	longPrefix := strings.Repeat("前文上下文信息 ", 12)
	msgs := []map[string]any{
		{"id": "m1", "agent_id": "agent-1", "session_id": "sess-1", "role": "assistant", "kind": "message", "text": longPrefix + "执行 alembic upgrade head 完成 100% 校验", "created_at": "2026-11-23T10:01:00Z"},
		{"id": "m2", "agent_id": "agent-1", "session_id": "sess-1", "role": "assistant", "kind": "tool", "text": "alembic upgrade tool noise", "created_at": "2026-11-23T10:02:00Z"},
		{"id": "m3", "agent_id": "agent-1", "session_id": "sess-2", "role": "assistant", "kind": "message", "text": "alembic upgrade ephemeral should be hidden", "created_at": "2026-11-23T10:03:00Z"},
	}
	for _, m := range msgs {
		if _, err := st.UpsertMessage(ctx, m); err != nil {
			t.Fatal(err)
		}
	}

	hits, err := st.SearchMessages(ctx, "ALEMBIC 100%", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 {
		t.Fatalf("expected 1 hit (skipping tool & ephemeral, escaping %%), got %+v", hits)
	}
	if hits[0].SessionID != "sess-1" || hits[0].SessionTitle != "数据库迁移" || !strings.Contains(hits[0].Snippet, "alembic upgrade head") {
		t.Fatalf("unexpected hit: %+v", hits[0])
	}
}
