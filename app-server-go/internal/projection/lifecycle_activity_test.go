package projection

import (
	"context"
	"testing"
)

func TestLifecycleHooksDoNotAdvanceConversationActivity(t *testing.T) {
	for _, hook := range []string{"SessionStart", "SessionEnd"} {
		t.Run(hook, func(t *testing.T) {
			db := activityDB(t)
			const original = "2026-10-16 08:35:45.048000"
			if _, err := db.Exec(`INSERT INTO sessions(id,agent_id,status,source_session_id,updated_at) VALUES('app-1','a1','idle','native-1',?)`, original); err != nil {
				t.Fatal(err)
			}
			// A process ends the following day, then its journal is replayed on restart.
			for attempt := 0; attempt < 2; attempt++ {
				tx, err := db.BeginTx(context.Background(), nil)
				if err != nil {
					t.Fatal(err)
				}
				events, unknown, err := projectNativeActivity(context.Background(), tx, "native-1", "a1", map[string]any{
					"session_id": "native-1", "hook_event": hook, "observed_at": "2026-10-17T04:07:05.615846Z",
				})
				if err != nil {
					tx.Rollback()
					t.Fatal(err)
				}
				if err := tx.Commit(); err != nil {
					t.Fatal(err)
				}
				for _, event := range events {
					if event.Type == "session.upsert" {
						t.Fatalf("%s changed conversation activity during lifecycle/replay", hook)
					}
				}
				if unknown != nil {
					t.Fatal("known lifecycle session became unknown")
				}
				var stamp, status string
				if err := db.QueryRow(`SELECT updated_at,status FROM sessions WHERE id='app-1'`).Scan(&stamp, &status); err != nil {
					t.Fatal(err)
				}
				if stamp != original || status != "idle" {
					t.Fatalf("lifecycle changed activity/status: %s / %s", stamp, status)
				}
			}
		})
	}
}

func TestConversationHookStillAdvancesActivity(t *testing.T) {
	db := activityDB(t)
	if _, err := db.Exec(`INSERT INTO sessions(id,agent_id,status,updated_at) VALUES('s1','a1','running','2026-10-16 08:34:34')`); err != nil {
		t.Fatal(err)
	}
	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	events, _, err := projectNativeActivity(context.Background(), tx, "s1", "a1", map[string]any{
		"hook_event": "UserPromptSubmit", "observed_at": "2026-10-16T08:35:00Z",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 || events[0].Type != "transcript.invalidate" || events[1].Type != "session.upsert" || events[1].Data["updated_at"] != "2026-10-16T08:35:00.000Z" {
		t.Fatalf("conversation activity did not advance: %+v", events)
	}
}
