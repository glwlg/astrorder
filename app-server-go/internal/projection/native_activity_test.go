package projection

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func activityDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := db.Exec(`CREATE TABLE sessions (
		id TEXT, agent_id TEXT, status TEXT, source_session_id TEXT,
		runtime_owner TEXT, updated_at TEXT, PRIMARY KEY (agent_id, id))`); err != nil {
		t.Fatal(err)
	}
	return db
}

func runActivity(t *testing.T, db *sql.DB, agent string, payload map[string]any) []string {
	t.Helper()
	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	events, err := projectNativeActivityTx(context.Background(), tx, "", agent, payload)
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	out := make([]string, 0, len(events))
	for _, event := range events {
		out = append(out, event.Type+"|"+event.Data["updated_at"].(string)+"|"+event.Data["status"].(string))
	}
	return out
}

func TestNativeActivityAdvancesUpdatedAtInWireFormat(t *testing.T) {
	db := activityDB(t)
	db.Exec(`INSERT INTO sessions(id,agent_id,status,updated_at) VALUES('s1','a1','idle','2026-10-11 00:00:00')`)
	at := time.Date(2026, 10, 11, 0, 51, 17, 0, time.UTC)
	got := runActivity(t, db, "a1", map[string]any{"session_id": "s1", "observed_at": float64(at.Unix())})
	if len(got) != 1 || got[0] != "session.upsert|2026-10-11T00:51:17.000Z|idle" {
		t.Fatalf("unexpected events: %v", got)
	}
	var stored string
	db.QueryRow(`SELECT updated_at FROM sessions WHERE id='s1'`).Scan(&stored)
	if !strings.HasPrefix(stored, "2026-10-11 00:51:17") {
		t.Fatalf("stored updated_at = %q", stored)
	}
}

func TestNativeActivityNeverMovesBackwardsOrChangesStatus(t *testing.T) {
	db := activityDB(t)
	db.Exec(`INSERT INTO sessions(id,agent_id,status,updated_at) VALUES('s1','a1','running','2026-10-11 01:00:00')`)
	old := time.Date(2026, 10, 11, 0, 30, 0, 0, time.UTC)
	if got := runActivity(t, db, "a1", map[string]any{"session_id": "s1", "observed_at": float64(old.Unix())}); len(got) != 0 {
		t.Fatalf("stale activity emitted %v", got)
	}
	newer := time.Date(2026, 10, 11, 2, 0, 0, 0, time.UTC)
	got := runActivity(t, db, "a1", map[string]any{"session_id": "s1", "observed_at": float64(newer.Unix())})
	if len(got) != 1 || !strings.HasSuffix(got[0], "|running") {
		t.Fatalf("status must be untouched, got %v", got)
	}
}

func TestNativeActivityIgnoresUntrackedSessionsAndMatchesSourceID(t *testing.T) {
	db := activityDB(t)
	db.Exec(`INSERT INTO sessions(id,agent_id,status,source_session_id,updated_at) VALUES('app-1','a1','idle','native-1','2026-10-11 00:00:00')`)
	at := float64(time.Date(2026, 10, 11, 3, 0, 0, 0, time.UTC).Unix())
	if got := runActivity(t, db, "a1", map[string]any{"session_id": "unknown", "observed_at": at}); len(got) != 0 {
		t.Fatalf("untracked session produced %v", got)
	}
	if got := runActivity(t, db, "other-agent", map[string]any{"session_id": "native-1", "observed_at": at}); len(got) != 0 {
		t.Fatalf("other agent's session matched: %v", got)
	}
	if got := runActivity(t, db, "a1", map[string]any{"session_id": "native-1", "observed_at": at}); len(got) != 1 {
		t.Fatalf("source_session_id not matched: %v", got)
	}
	var count int
	db.QueryRow(`SELECT count(*) FROM sessions`).Scan(&count)
	if count != 1 {
		t.Fatalf("activity must never create sessions, have %d", count)
	}
}

func TestRuntimeOwnerPublishesSessionUpsertInWireFormat(t *testing.T) {
	db := activityDB(t)
	db.Exec(`INSERT INTO sessions(id,agent_id,status,updated_at) VALUES('s1','a1','idle','2026-10-10 00:00:00')`)
	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	events, err := projectRuntimeOwnerTx(context.Background(), tx, "s1", map[string]any{"agent_id": "a1", "session_id": "s1", "owner_pid": float64(7)}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	types := []string{}
	for _, event := range events {
		types = append(types, event.Type)
		if event.Type == "session.upsert" {
			stamp, _ := event.Data["updated_at"].(string)
			if !strings.HasSuffix(stamp, "Z") || !strings.Contains(stamp, "T") {
				t.Fatalf("session.upsert updated_at is not RFC3339: %q", stamp)
			}
		}
	}
	if strings.Join(types, ",") != "runtime.owner,session.upsert" {
		t.Fatalf("events = %v", types)
	}
}
