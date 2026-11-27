package projection

import (
	"astrorder.dev/app-server/internal/store"
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"testing"
)

func TestCodexNativeTokenUsageIsProjected(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	_, err = st.UpsertSession(ctx, map[string]any{"id": "s", "agent_id": "a", "title": "test", "status": "idle", "updated_at": "2026-10-12T00:00:00Z"})
	if err != nil {
		t.Fatal(err)
	}
	err = st.WithTx(ctx, func(tx *sql.Tx) error {
		_, err := projectCodexNotificationTx(ctx, tx, "s", "a", map[string]any{"method": "thread/tokenUsage/updated", "params": map[string]any{"threadId": "s", "tokenUsage": map[string]any{"modelContextWindow": json.Number("200000"), "last": map[string]any{"inputTokens": json.Number("12345")}, "total": map[string]any{"inputTokens": json.Number("24690"), "totalTokens": json.Number("25000")}}}})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	rows, err := st.ListTokenMetrics(ctx, "a", "s")
	if err != nil || len(rows) != 1 {
		t.Fatalf("usage missing: %+v %v", rows, err)
	}
	if rows[0]["last_input_tokens"] != int64(12345) {
		t.Fatalf("context missing: %+v", rows)
	}
}

func TestCodexEphemeralHandoffSummarySkipsTokenMetricsPersistence(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	_, err = st.UpsertSession(ctx, map[string]any{
		"id":         "fork-1",
		"agent_id":   "a",
		"title":      "转交摘要",
		"ephemeral":  true,
		"status":     "idle",
		"updated_at": "2026-10-12T00:00:00Z",
	})
	if err != nil {
		t.Fatal(err)
	}
	var events []store.Event
	err = st.WithTx(ctx, func(tx *sql.Tx) error {
		var projErr error
		events, projErr = projectCodexNotificationTx(ctx, tx, "fork-1", "a", map[string]any{
			"method": "thread/tokenUsage/updated",
			"params": map[string]any{
				"threadId": "fork-1",
				"tokenUsage": map[string]any{
					"modelContextWindow": json.Number("200000"),
					"last":               map[string]any{"inputTokens": json.Number("150000")},
					"total": map[string]any{
						"inputTokens":  json.Number("185000000"),
						"outputTokens": json.Number("1000000"),
						"totalTokens":  json.Number("186000000"),
					},
				},
			},
		})
		return projErr
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].Type != "token.usage" {
		t.Fatalf("expected live token.usage event, got %+v", events)
	}
	rows, err := st.ListTokenMetrics(ctx, "a", "fork-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 0 {
		t.Fatalf("expected ephemeral handoff summary fork to skip token_metrics persistence, got %+v", rows)
	}
}
