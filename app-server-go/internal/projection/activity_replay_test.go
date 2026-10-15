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

func TestCodexReplayUsesOriginalActivityTime(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	const aid, sid = "codex", "historical"
	initial := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	if _, err := st.UpsertSession(ctx, map[string]any{"agent_id": aid, "id": sid, "status": "idle", "updated_at": initial.Format(time.RFC3339)}); err != nil {
		t.Fatal(err)
	}
	p := &Projection{}
	agentID := aid
	apply := func(method string, at time.Time) {
		t.Helper()
		raw, err := json.Marshal(map[string]any{"agent_id": aid, "session_id": sid, "frame": map[string]any{"method": method, "params": map[string]any{"threadId": sid, "turn": map[string]any{"id": "old-turn", "status": "completed"}}}})
		if err != nil {
			t.Fatal(err)
		}
		env := &pb.EventEnvelope{Session: &pb.SessionRef{AgentId: &agentID, SessionId: sid}, EventType: "codex.notification", TimestampUnixMicros: at.UnixMicro(), NativePayload: &pb.NativeJson{Schema: "astrorder.event.payload.v1", Utf8Json: raw}}
		if err := st.WithTx(ctx, func(tx *sql.Tx) error {
			_, err := p.projectEnvelopeTx(ctx, tx, env)
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}
	assertTime := func(want time.Time) {
		t.Helper()
		row, err := st.QueryRow(ctx, "SELECT updated_at FROM sessions WHERE agent_id=? AND id=?", aid, sid)
		if err != nil {
			t.Fatal(err)
		}
		got, err := store.ParseTime(row["updated_at"])
		if err != nil || !got.Equal(want) {
			t.Fatalf("activity = %v, want original event time %v (err=%v)", row["updated_at"], want, err)
		}
	}
	started := initial.Add(24*time.Hour + 123456*time.Microsecond)
	completed := started.Add(time.Minute)
	apply("turn/started", started)
	assertTime(started)
	apply("turn/completed", completed)
	assertTime(completed)
	// Reprojection of older activity cannot replace a newer native activity.
	apply("turn/started", initial)
	assertTime(completed)
	// An envelope without an original timestamp cannot invent activity now.
	apply("turn/completed", time.UnixMicro(0))
	assertTime(completed)
}
