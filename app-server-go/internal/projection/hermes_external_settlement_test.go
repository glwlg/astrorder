package projection

import (
	"astrorder.dev/app-server/internal/store"
	pb "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
	"context"
	"database/sql"
	"path/filepath"
	"testing"
)

func TestHermesExternalSettlementDoesNotInventDecision(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if _, err = st.UpsertSession(ctx, map[string]any{"id": "s", "agent_id": "a", "status": "waiting_approval", "title": "fixture", "updated_at": "2026-10-18T00:00:00Z"}); err != nil {
		t.Fatal(err)
	}
	if _, err = st.AppendEvent(ctx, store.Event{ID: "pending", Type: "approval.upsert", AgentID: "a", SessionID: "s", Data: map[string]any{"id": "p", "state": "pending"}}); err != nil {
		t.Fatal(err)
	}
	env := &pb.EventEnvelope{Session: &pb.SessionRef{SessionId: "s"}, EventType: "hermes.approval_resolved", Status: pb.SessionStatus_SESSION_STATUS_RUNNING, NativePayload: &pb.NativeJson{Utf8Json: []byte(`{"agent_id":"a","session_id":"s","approval_id":"p","state":"resolved"}`)}}
	var events []store.Event
	if err = st.WithTx(ctx, func(tx *sql.Tx) error {
		var e error
		events, e = (&Projection{}).projectEnvelopeTx(ctx, tx, env)
		return e
	}); err != nil {
		t.Fatal(err)
	}
	for _, event := range events {
		if _, err = st.AppendEvent(ctx, event); err != nil {
			t.Fatal(err)
		}
	}
	pending, err := st.PendingApprovals(ctx)
	if err != nil || len(pending) != 0 {
		t.Fatalf("settled prompt remains: %v %v", pending, err)
	}
	approval, err := st.Approval(ctx, "a", "s", "p")
	if err != nil || approval["state"] != "resolved" {
		t.Fatalf("invented user choice: %v %v", approval, err)
	}
}
