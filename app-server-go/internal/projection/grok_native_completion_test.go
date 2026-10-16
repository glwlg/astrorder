package projection

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"astrorder.dev/app-server/internal/store"
)

func TestGrokNativeCompletionProjectsUsage(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "native.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	_, err = st.UpsertSession(ctx, map[string]any{"id": "s", "agent_id": "g", "title": "Session", "status": "running", "updated_at": "2026-10-16T00:00:00Z"})
	if err != nil {
		t.Fatal(err)
	}
	err = st.WithTx(ctx, func(tx *sql.Tx) error {
		events, e := projectGrokCompletedTx(ctx, tx, "s", map[string]any{"agent_id": "g", "result": map[string]any{"_meta": map[string]any{"usage": map[string]any{"inputTokens": float64(120), "outputTokens": float64(30), "totalTokens": float64(150), "modelUsage": map[string]any{"native-model": map[string]any{}}}}}})
		if e != nil {
			return e
		}
		for _, event := range events {
			if event.Type == "token.usage" && event.Data["total_tokens"] == int64(150) {
				return nil
			}
		}
		t.Fatal("native completion usage was not projected")
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	rows, err := st.QueryRows(ctx, "SELECT model,total_tokens FROM token_metrics WHERE session_id='s'")
	if err != nil || len(rows) != 1 || rows[0]["model"] != "native-model" {
		t.Fatalf("native usage missing: %v %v", rows, err)
	}
}

func TestGrokNativeNestedToolContent(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "tool.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	_, err = st.UpsertSession(ctx, map[string]any{"id": "s", "agent_id": "g", "title": "Session", "status": "running", "updated_at": "2026-10-16T00:00:00Z"})
	if err != nil {
		t.Fatal(err)
	}
	err = st.WithTx(ctx, func(tx *sql.Tx) error {
		events, e := projectGrokToolCallTx(ctx, tx, "s", "g", nil, map[string]any{"_meta": map[string]any{"astrorderItemId": "tool-id"}}, map[string]any{"toolCallId": "native-call", "title": "read_file", "status": "Completed", "content": []any{map[string]any{"type": "content", "content": map[string]any{"type": "text", "text": "native output"}}}})
		if e != nil {
			return e
		}
		for _, event := range events {
			if event.Type == "message.upsert" && event.Data["text"] == "native output" {
				return nil
			}
		}
		t.Fatal("nested native tool output missing")
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	rows, err := st.QueryRows(ctx, "SELECT status FROM tasks WHERE session_id='s'")
	if err != nil || len(rows) != 1 || rows[0]["status"] != "completed" {
		t.Fatalf("native status missing: %v %v", rows, err)
	}
}
