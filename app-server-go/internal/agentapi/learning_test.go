package agentapi

import (
	"context"
	"testing"
)

func TestLearningCapabilitiesDispatch(t *testing.T) {
	h, st, _ := setupDelegationTestEnv(t)
	defer st.Close()
	ctx := context.Background()

	var gotStatus, gotIngest, gotUpdate, gotScan map[string]any
	h.SetLearningCallbacks(
		func(_ context.Context, payload map[string]any) (any, error) {
			gotStatus = payload
			return map[string]any{"topics": []any{}}, nil
		},
		func(_ context.Context, payload map[string]any) (any, error) {
			gotIngest = payload
			return map[string]any{"ok": true, "new_topics_count": 1}, nil
		},
		func(_ context.Context, payload map[string]any) (any, error) {
			gotUpdate = payload
			return map[string]any{"ok": true}, nil
		},
		func(_ context.Context, payload map[string]any) (any, error) {
			gotScan = payload
			return map[string]any{"ok": true}, nil
		},
	)

	if _, err := h.Invoke(ctx, "learning.status", map[string]any{"status": "open"}); err != nil {
		t.Fatalf("learning.status failed: %v", err)
	}
	if gotStatus["status"] != "open" {
		t.Fatalf("unexpected status payload: %#v", gotStatus)
	}

	if _, err := h.Invoke(ctx, "learning.ingest", map[string]any{"run_id": "scan-1", "topics": []any{map[string]any{"title": "T1"}}}); err != nil {
		t.Fatalf("learning.ingest failed: %v", err)
	}
	if gotIngest["run_id"] != "scan-1" {
		t.Fatalf("unexpected ingest payload: %#v", gotIngest)
	}

	if _, err := h.Invoke(ctx, "learning.update", map[string]any{"topic_id": "lt-1", "status": "mastered"}); err != nil {
		t.Fatalf("learning.update failed: %v", err)
	}
	if gotUpdate["topic_id"] != "lt-1" || gotUpdate["status"] != "mastered" {
		t.Fatalf("unexpected update payload: %#v", gotUpdate)
	}

	if _, err := h.Invoke(ctx, "learning.scan", map[string]any{"lookback_days": 14}); err != nil {
		t.Fatalf("learning.scan failed: %v", err)
	}
	if gotScan["lookback_days"] != 14 {
		t.Fatalf("unexpected scan payload: %#v", gotScan)
	}
}
