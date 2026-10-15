package projection

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"path/filepath"
	"testing"

	"astrorder.dev/app-server/internal/store"
	sessiondv1 "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
)

func TestNativeGoalsProjectThroughEnvelopeAndReplay(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	p := &Projection{}
	for _, source := range []string{"codex", "hermes"} {
		if _, err := st.UpsertSession(ctx, map[string]any{"id": "s", "agent_id": source, "status": "running", "updated_at": "2026-10-12T00:00:00Z"}); err != nil {
			t.Fatal(err)
		}
		payload := map[string]any{"agent_id": source, "session_id": "s"}
		if source == "codex" {
			payload["frame"] = map[string]any{"method": "thread/goal/updated", "params": map[string]any{"threadId": "s", "goal": map[string]any{"threadId": "s", "objective": "Build", "status": "active"}}}
		} else {
			payload["frame"] = map[string]any{"type": "session.control.update", "payload": map[string]any{"control": map[string]any{"goal": map[string]any{"title": "Build", "status": "active", "turns_used": 0, "max_turns": 20}}}}
		}
		raw, err := json.Marshal(payload)
		if err != nil {
			t.Fatal(err)
		}
		env := &sessiondv1.EventEnvelope{EventType: source + ".notification", Session: &sessiondv1.SessionRef{AgentId: &source, SessionId: "s"}, NativePayload: &sessiondv1.NativeJson{Schema: "astrorder.event.payload.v1", Utf8Json: raw}}
		for replay := 0; replay < 2; replay++ {
			if err := st.WithTx(ctx, func(tx *sql.Tx) error {
				events, err := p.projectEnvelopeTx(ctx, tx, env)
				if err != nil {
					return err
				}
				if len(events) != 1 || events[0].Type != "task.upsert" || events[0].AgentID != source {
					return fmt.Errorf("invalid goal envelope projection: %+v", events)
				}
				progress, ok := events[0].Data["progress"].(map[string]any)
				if !ok || progress["type"] != "goal" {
					return fmt.Errorf("event progress not decoded: %+v", events[0].Data)
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
		}
		rows, err := st.ListTasks(ctx, source, "s")
		if err != nil || len(rows) != 1 {
			t.Fatalf("replay duplicated goal: %+v %v", rows, err)
		}
	}
}
