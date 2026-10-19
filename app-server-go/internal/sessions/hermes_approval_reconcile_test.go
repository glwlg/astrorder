package sessions

import (
	"astrorder.dev/app-server/internal/application"
	"astrorder.dev/app-server/internal/store"
	"context"
	"testing"
)

func TestHermesSnapshotRetiresOnlyAbsentApproval(t *testing.T) {
	h, st, _, cleanup := setupTestServer(t, &mockDaemonServer{})
	defer cleanup()
	h.publisher = application.NewEventHub(st)
	ctx := context.Background()
	for _, id := range []string{"old", "live"} {
		if _, err := st.AppendEvent(ctx, store.Event{ID: id, Type: "approval.upsert", AgentID: "a", SessionID: "s", Data: map[string]any{"id": id, "state": "pending"}}); err != nil {
			t.Fatal(err)
		}
	}
	pending, err := st.PendingApprovals(ctx)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := map[string]any{"session_id": "s", "running": true, "execution_generation": float64(4), "revision": float64(3), "prompts": []any{map[string]any{"kind": "approval", "prompt_id": "live"}}}
	if err := h.reconcileHermesApprovals(ctx, "a", "s", "s", pending, snapshot); err != nil {
		t.Fatal(err)
	}
	remaining, err := st.PendingApprovals(ctx)
	if err != nil || len(remaining) != 1 || remaining[0]["id"] != "live" {
		t.Fatalf("incorrect remaining approvals %v %v", remaining, err)
	}
	old, err := st.Approval(ctx, "a", "s", "old")
	if err != nil || old["state"] != "resolved" {
		t.Fatalf("old prompt not resolved %v %v", old, err)
	}
}

func TestHermesApprovalSnapshotMustConfirmIdentityAndPrompts(t *testing.T) {
	h, st, _, cleanup := setupTestServer(t, &mockDaemonServer{})
	defer cleanup()
	h.publisher = application.NewEventHub(st)
	ctx := context.Background()
	if _, err := st.AppendEvent(ctx, store.Event{ID: "old", Type: "approval.upsert", AgentID: "a", SessionID: "s", Data: map[string]any{"id": "old", "state": "pending"}}); err != nil {
		t.Fatal(err)
	}
	pending, _ := st.PendingApprovals(ctx)
	for _, snapshot := range []map[string]any{{"session_id": "foreign", "prompts": []any{}}, {"session_id": "s"}, {"session_id": "s", "prompts": []any{map[string]any{"kind": "approval"}}}} {
		if err := h.reconcileHermesApprovals(ctx, "a", "s", "s", pending, snapshot); err == nil {
			t.Fatal("accepted unconfirmed snapshot")
		}
	}
	remaining, _ := st.PendingApprovals(ctx)
	if len(remaining) != 1 {
		t.Fatal("unconfirmed read cleared approval")
	}
}
