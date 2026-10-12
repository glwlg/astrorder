package projection

import (
	"astrorder.dev/app-server/internal/store"
	pb "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
	"context"
	"database/sql"
	"path/filepath"
	"testing"
)

func TestHermesApprovalResolutionProjectsWithoutInterruptingOriginalCommand(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	_, err = st.UpsertSession(ctx, map[string]any{"id": "s", "agent_id": "a", "title": "fixture", "status": "waiting_approval", "updated_at": "2026-10-09T00:00:00Z"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = st.AppendEvent(ctx, store.Event{ID: "request", Type: "approval.upsert", AgentID: "a", SessionID: "s", Data: map[string]any{"id": "approval", "state": "pending"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, cid := range []string{"original", "decision"} {
		if _, _, err := st.CreateCommand(ctx, map[string]any{"id": cid, "agent_id": "a", "session_id": "s", "action": "approve", "text": "", "attachments": []any{}}, "accepted"); err != nil {
			t.Fatal(err)
		}
	}
	raw := []byte(`{"agent_id":"a","session_id":"s","approval_id":"approval","command_id":"decision","state":"rejected"}`)
	env := &pb.EventEnvelope{Session: &pb.SessionRef{SessionId: "s"}, EventType: "hermes.approval_resolved", Status: pb.SessionStatus_SESSION_STATUS_RUNNING, NativePayload: &pb.NativeJson{Utf8Json: raw}}
	var events []store.Event
	if err := st.WithTx(ctx, func(tx *sql.Tx) error {
		var err error
		events, err = (&Projection{}).projectEnvelopeTx(ctx, tx, env)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	for _, event := range events {
		if _, err := st.AppendEvent(ctx, event); err != nil {
			t.Fatal(err)
		}
	}
	approvals, err := st.PendingApprovals(ctx)
	if err != nil || len(approvals) != 0 {
		t.Fatalf("approval stayed pending: %v %v", approvals, err)
	}
	decision, err := st.GetCommand(ctx, "a", "s", "decision")
	if err != nil || decision["state"] != "completed" {
		t.Fatalf("decision not completed: %v %v", decision, err)
	}
	original, err := st.GetCommand(ctx, "a", "s", "original")
	if err != nil || original["state"] != "accepted" {
		t.Fatalf("original command touched: %v %v", original, err)
	}
}

func TestHermesApprovalResolutionUsesAuthoritativeSessionStatus(t *testing.T) {
	for _, test := range []struct {
		status pb.SessionStatus
		want   string
	}{
		{pb.SessionStatus_SESSION_STATUS_IDLE, "idle"},
		{pb.SessionStatus_SESSION_STATUS_WAITING_APPROVAL, "waiting_approval"},
		{pb.SessionStatus_SESSION_STATUS_RUNNING, "running"},
	} {
		t.Run(test.want, func(t *testing.T) {
			ctx := context.Background()
			st, err := store.Open(filepath.Join(t.TempDir(), "app.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer st.Close()
			if _, err := st.UpsertSession(ctx, map[string]any{"id": "s", "agent_id": "a", "status": "waiting_approval", "title": "fixture", "updated_at": "2026-10-09T00:00:00Z"}); err != nil {
				t.Fatal(err)
			}
			env := &pb.EventEnvelope{Session: &pb.SessionRef{SessionId: "s"}, EventType: "hermes.approval_resolved", Status: test.status, NativePayload: &pb.NativeJson{Utf8Json: []byte(`{"agent_id":"a","session_id":"s","approval_id":"approval","state":"rejected"}`)}}
			if err := st.WithTx(ctx, func(tx *sql.Tx) error { _, err := (&Projection{}).projectEnvelopeTx(ctx, tx, env); return err }); err != nil {
				t.Fatal(err)
			}
			session, err := st.GetSession(ctx, "a", "s")
			if err != nil || session["status"] != test.want {
				t.Fatalf("native status lost: got=%v want=%s error=%v", session["status"], test.want, err)
			}
		})
	}
}
