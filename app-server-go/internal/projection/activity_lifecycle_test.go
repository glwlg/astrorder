package projection

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"astrorder.dev/app-server/internal/store"
	pb "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
)

func TestSharedStatusReplayPreservesActivityTime(t *testing.T) {
	for _, eventType := range []string{"session.active", "hermes.command_complete", "grok.completed", "runtime.ownership_lost"} {
		t.Run(eventType, func(t *testing.T) {
			ctx := context.Background()
			st, err := store.Open(filepath.Join(t.TempDir(), "app.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer st.Close()
			agent := "a"
			initial := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
			if _, err := st.UpsertSession(ctx, map[string]any{"agent_id": agent, "id": "s", "status": "running", "control_state": "owned", "updated_at": initial.Format(time.RFC3339)}); err != nil {
				t.Fatal(err)
			}
			observed := initial.Add(time.Hour)
			want := observed
			if eventType == "runtime.ownership_lost" {
				want = initial
			}
			raw, err := json.Marshal(map[string]any{"agent_id": agent, "session_id": "s"})
			if err != nil {
				t.Fatal(err)
			}
			env := &pb.EventEnvelope{Session: &pb.SessionRef{AgentId: &agent, SessionId: "s"}, EventType: eventType, TimestampUnixMicros: observed.UnixMicro(), NativePayload: &pb.NativeJson{Schema: "astrorder.event.payload.v1", Utf8Json: raw}}
			p := &Projection{}
			if err := st.WithTx(ctx, func(tx *sql.Tx) error {
				_, err := p.projectEnvelopeTx(ctx, tx, env)
				return err
			}); err != nil {
				t.Fatal(err)
			}
			row, err := st.GetSession(ctx, agent, "s")
			if err != nil {
				t.Fatal(err)
			}
			at, err := store.ParseTime(row["updated_at"])
			if err != nil || !at.Equal(want) {
				t.Fatalf("activity = %v, want %v (err=%v)", row["updated_at"], want, err)
			}
		})
	}
}
