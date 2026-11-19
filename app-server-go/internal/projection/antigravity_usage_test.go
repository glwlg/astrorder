package projection

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"astrorder.dev/app-server/internal/store"
)

func TestAntigravityTokenUsageProjectionPreservesCumulativeAndResumeContext(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "proj-agy.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	sid := "52794208-5f2e-4120-a6b1-99e5ee29c7f1"
	aid := "local-antigravity"
	if _, err := st.UpsertSession(ctx, map[string]any{
		"id":         sid,
		"agent_id":   aid,
		"title":      "Session",
		"status":     "idle",
		"updated_at": "2026-11-18T00:00:00Z",
	}); err != nil {
		t.Fatal(err)
	}

	// 1. Seed full cumulative usage on initial model.
	if err := st.WithTx(ctx, func(tx *sql.Tx) error {
		_, e := projectTokenMetricsTx(ctx, tx, sid, aid, map[string]any{
			"agent_id":          aid,
			"session_id":        sid,
			"provider":          "antigravity",
			"model":             "gemini-4-carbon",
			"date":              "2026-11-18",
			"input_tokens":      int64(173519606),
			"cached_tokens":     int64(163943397),
			"output_tokens":     int64(674472),
			"reasoning_tokens":  int64(374415),
			"total_tokens":      int64(174194078),
			"context_window":    int64(1048576),
			"last_input_tokens": int64(81208),
		})
		return e
	}); err != nil {
		t.Fatal(err)
	}

	// 2. Partial ACP usage_update on a switched model must carry forward cumulative counters.
	if err := st.WithTx(ctx, func(tx *sql.Tx) error {
		evs, e := projectTokenMetricsTx(ctx, tx, sid, aid, map[string]any{
			"agent_id":          aid,
			"session_id":        sid,
			"provider":          "antigravity",
			"model":             "gemini-4-carbon-high",
			"date":              "2026-11-18",
			"source":            "acp_usage_update",
			"context_window":    int64(1048576),
			"last_input_tokens": int64(94000),
		})
		if e != nil {
			return e
		}
		if len(evs) != 1 || evs[0].Data["total_tokens"] != int64(174194078) {
			t.Fatalf("expected projected event to retain cumulative total_tokens, got %+v", evs)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	// 3. Resume used=0 notification must not zero out last_input_tokens.
	if err := st.WithTx(ctx, func(tx *sql.Tx) error {
		_, e := projectTokenMetricsTx(ctx, tx, sid, aid, map[string]any{
			"agent_id":          aid,
			"session_id":        sid,
			"provider":          "antigravity",
			"model":             "gemini-4-carbon-high",
			"date":              "2026-11-18",
			"source":            "acp_usage_update",
			"context_window":    int64(1048576),
			"last_input_tokens": int64(0),
		})
		return e
	}); err != nil {
		t.Fatal(err)
	}

	metrics, err := st.ListTokenMetrics(ctx, aid, sid)
	if err != nil || len(metrics) == 0 {
		t.Fatalf("ListTokenMetrics: %v %v", metrics, err)
	}
	latest := metrics[0]
	if latest["model"] != "gemini-4-carbon-high" || latest["last_input_tokens"] != int64(94000) || latest["total_tokens"] != int64(174194078) {
		t.Fatalf("unexpected persisted metrics: %+v", latest)
	}
}
