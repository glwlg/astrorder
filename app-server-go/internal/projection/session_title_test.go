package projection

import (
	"astrorder.dev/app-server/internal/store"
	"context"
	"database/sql"
	"path/filepath"
	"testing"
)

func TestRuntimePlaceholderCannotEraseGeneratedSessionTitle(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "title.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	st.UpsertSession(ctx, map[string]any{"id": "s", "agent_id": "a", "title": "已有标题", "status": "running", "updated_at": "2026-10-09T00:00:00Z"})
	err = st.WithTx(ctx, func(tx *sql.Tx) error {
		_, err := upsertSessionTx(ctx, tx, map[string]any{"id": "s", "agent_id": "a", "title": "New Session", "status": "idle", "updated_at": "2026-10-10T00:00:00Z"})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	row, _ := st.GetSession(ctx, "a", "s")
	if row["title"] != "已有标题" || row["status"] != "idle" {
		t.Fatalf("projection=%v", row)
	}
}

func TestNativeCatalogUpsertPreservesManualRenameButUpgradesAutoTitle(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "rename.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	// 1. Manually renamed session must NOT be overwritten by a non-placeholder native catalog title.
	if _, err := st.UpsertSession(ctx, map[string]any{"id": "s-manual", "agent_id": "agy", "title": "UI/UX Review Of Astrorder", "status": "idle", "updated_at": "2026-10-09T00:00:00Z"}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.UpdateSession(ctx, "agy", "s-manual", map[string]any{"title": "星序界面优化"}); err != nil {
		t.Fatal(err)
	}
	err = st.WithTx(ctx, func(tx *sql.Tx) error {
		_, err := upsertSessionTx(ctx, tx, map[string]any{"id": "s-manual", "agent_id": "agy", "title": "UI/UX Review Of Astrorder", "status": "running", "updated_at": "2026-10-10T00:00:00Z"})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	manualRow, _ := st.GetSession(ctx, "agy", "s-manual")
	if manualRow["title"] != "星序界面优化" || manualRow["status"] != "running" {
		t.Fatalf("expected manual rename preserved, got %v", manualRow)
	}

	// 2. Auto-generated fallback/llm title MAY still be upgraded by a native title.
	if _, err := st.UpsertSession(ctx, map[string]any{"id": "s-auto", "agent_id": "agy", "title": "New Session", "status": "idle", "updated_at": "2026-10-09T00:00:00Z"}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.CompareSessionTitle(ctx, "agy", "s-auto", "New Session", "临时标题", "fallback"); err != nil {
		t.Fatal(err)
	}
	err = st.WithTx(ctx, func(tx *sql.Tx) error {
		_, err := upsertSessionTx(ctx, tx, map[string]any{"id": "s-auto", "agent_id": "agy", "title": "原生升级标题", "status": "idle", "updated_at": "2026-10-10T00:00:00Z"})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	autoRow, _ := st.GetSession(ctx, "agy", "s-auto")
	if autoRow["title"] != "原生升级标题" {
		t.Fatalf("expected auto fallback title upgraded by native title, got %v", autoRow)
	}
}
