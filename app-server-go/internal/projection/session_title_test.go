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
