package projection

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"testing"

	"astrorder.dev/app-server/internal/store"
	pb "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
)

func TestCodexGuidanceDeliveryReplayCompletesUnknownCommandOnly(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if _, err := st.UpsertSession(ctx, map[string]any{"id": "s", "agent_id": "a", "status": "running", "updated_at": "2026-10-14T00:00:00Z"}); err != nil {
		t.Fatal(err)
	}
	for _, command := range []struct{ id, state string }{{"original", "running"}, {"guidance", "unknown"}} {
		if _, _, err := st.CreateCommand(ctx, map[string]any{"id": command.id, "agent_id": "a", "session_id": "s", "action": "send", "text": command.id}, command.state); err != nil {
			t.Fatal(err)
		}
	}
	payload, _ := json.Marshal(map[string]any{"agent_id": "a", "session_id": "s", "command_id": "guidance"})
	p := &Projection{}
	for i := 0; i < 2; i++ {
		if err := st.WithTx(ctx, func(tx *sql.Tx) error {
			_, err := p.projectEnvelopeTx(ctx, tx, &pb.EventEnvelope{EventType: "codex.command_complete", Session: &pb.SessionRef{SessionId: "s"}, Status: pb.SessionStatus_SESSION_STATUS_RUNNING, NativePayload: &pb.NativeJson{Utf8Json: payload}})
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}
	row, err := st.GetCommand(ctx, "a", "s", "guidance")
	if err != nil || row["state"] != "completed" {
		t.Fatalf("lost HTTP receipt not recovered from WAL: %+v %v", row, err)
	}
	original, err := st.GetCommand(ctx, "a", "s", "original")
	if err != nil || original["state"] != "running" {
		t.Fatalf("delivery event completed original command: %+v %v", original, err)
	}
	session, err := st.GetSession(ctx, "a", "s")
	if err != nil || session["status"] != "running" {
		t.Fatalf("delivery event ended original session: %+v %v", session, err)
	}
}
