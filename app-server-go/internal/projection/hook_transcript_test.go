package projection

import (
	"context"
	"testing"
	"time"
)

func TestToolHookInvalidatesTranscriptEvenWhenActivityTimeIsAlreadyNewer(t *testing.T) {
	for _, hook := range []string{"PreToolUse", "PostToolUse", "UserPromptSubmit", "SubagentStop"} {
		t.Run(hook, func(t *testing.T) {
			db := activityDB(t)
			if _, err := db.Exec(`INSERT INTO sessions(id,agent_id,status,source_session_id,updated_at) VALUES('app-1','a1','running','native-1','2026-10-15 02:00:00')`); err != nil {
				t.Fatal(err)
			}
			tx, err := db.BeginTx(context.Background(), nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			events, _, err := projectNativeActivity(context.Background(), tx, "native-1", "a1", map[string]any{"session_id": "native-1", "observed_at": float64(time.Date(2026, 10, 15, 1, 0, 0, 0, time.UTC).Unix()), "hook_event": hook})
			if err != nil {
				t.Fatal(err)
			}
			if len(events) != 1 || events[0].Type != "transcript.invalidate" || events[0].AgentID != "a1" || events[0].SessionID != "app-1" || events[0].Data["hook_event"] != hook {
				t.Fatalf("hook did not notify the exact transcript while the turn was still running: %+v", events)
			}
		})
	}
}
