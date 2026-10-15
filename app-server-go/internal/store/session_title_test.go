package store

import (
	"context"
	"path/filepath"
	"testing"
)

func TestSessionTitleComparePreservesActivityAndPlaceholderRefresh(t *testing.T) {
	ctx := context.Background()
	st, err := Open(filepath.Join(t.TempDir(), "title.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	row, err := st.UpsertSession(ctx, map[string]any{"id": "s", "agent_id": "a", "title": "New Session", "status": "running", "updated_at": "2026-10-09T00:00:00Z"})
	if err != nil {
		t.Fatal(err)
	}
	named, err := st.CompareSessionTitle(ctx, "a", "s", "New Session", "生成标题", "llm")
	if err != nil || named == nil {
		t.Fatal(err)
	}
	if named["updated_at"] != row["updated_at"] || named["status"] != "running" {
		t.Fatalf("activity changed: %v", named)
	}
	st.UpsertSession(ctx, map[string]any{"id": "s", "agent_id": "a", "title": "New Session", "status": "idle", "updated_at": "2026-10-10T00:00:00Z"})
	named, _ = st.GetSession(ctx, "a", "s")
	if named["title"] != "生成标题" {
		t.Fatalf("placeholder erased title: %v", named)
	}
	changed, err := st.CompareSessionTitle(ctx, "a", "s", "New Session", "过期结果", "llm")
	if err != nil || changed != nil {
		t.Fatalf("stale result overwrote title: %v %v", changed, err)
	}
}
