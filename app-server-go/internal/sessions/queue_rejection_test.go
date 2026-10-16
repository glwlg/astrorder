package sessions

import (
	"context"
	"net/http/httptest"
	"testing"
)

func TestQueueReconcilesOnlyKnownHermesAttachmentRejection(t *testing.T) {
	h, st, _, cleanup := setupTestServer(t, &mockDaemonServer{})
	defer cleanup()
	ctx := context.Background()
	if _, err := st.UpsertAgent(ctx, map[string]any{"id": "a", "kind": "hermes", "name": "fixture", "status": "ready"}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.UpsertSession(ctx, map[string]any{"id": "s", "agent_id": "a", "status": "idle", "updated_at": "2026-10-15T00:00:00Z"}); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"rejected", "lost"} {
		if _, _, err := st.CreateCommand(ctx, map[string]any{"id": id, "agent_id": "a", "session_id": "s", "action": "enqueue", "text": "keep", "attachments": []any{map[string]any{"id": "blob", "name": "photo.png"}}}, "unknown"); err != nil {
			t.Fatal(err)
		}
		detail := "response lost"
		if id == "rejected" {
			detail = "Native result is unconfirmed: native command outcome is uncertain: attachments must be queued for the next Hermes turn"
		}
		if _, err := st.SetCommandState(ctx, "a", "s", id, "unknown", detail, false); err != nil {
			t.Fatal(err)
		}
	}
	w := httptest.NewRecorder()
	h.handleListCommands(w, httptest.NewRequest("GET", "/?agent_id=a", nil), "s")
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	row, _ := st.GetCommand(ctx, "a", "s", "rejected")
	if row["state"] != "failed" {
		t.Fatalf("definitive rejection remains locked: %+v", row)
	}
	if row["text"] != "keep" || len(row["attachments"].([]any)) != 1 {
		t.Fatal("recovery lost message")
	}
	lost, _ := st.GetCommand(ctx, "a", "s", "lost")
	if lost["state"] != "unknown" {
		t.Fatal("ambiguous submission became retryable")
	}
	_, cancelled, err := st.CancelQueuedCommand(ctx, "a", "s", "rejected")
	if err != nil || !cancelled {
		t.Fatalf("failed message cannot be edited/deleted: %v", err)
	}
}
