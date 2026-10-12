package projection

import (
	"context"
	"testing"
	"time"
)

func TestNativeActivityReportsUnknownSessionWithoutCreatingIt(t *testing.T) {
	db := activityDB(t)
	at := float64(time.Date(2026, 10, 11, 3, 0, 0, 0, time.UTC).Unix())
	db.Exec(`INSERT INTO sessions(id,agent_id,status,updated_at) VALUES('known','a1','idle','2026-10-11 05:00:00')`)

	cases := []struct {
		name     string
		envAgent string // the Agent id on the event envelope, often empty
		payload  map[string]any
		want     string // "agent/native" or ""
	}{
		{"unknown session is reported", "a1", map[string]any{"session_id": "fresh-thread", "observed_at": at}, "a1/fresh-thread"},
		// The daemon's native.activity frame names the Agent only in its payload.
		{"agent taken from the payload", "", map[string]any{"agent_id": "a1", "session_id": "fresh-thread", "observed_at": at}, "a1/fresh-thread"},
		{"known session is not", "a1", map[string]any{"session_id": "known", "observed_at": at}, ""},
		{"missing instant is ignored", "a1", map[string]any{"session_id": "fresh-thread"}, ""},
		{"missing id is ignored", "a1", map[string]any{"observed_at": at}, ""},
		{"no agent anywhere is ignored", "", map[string]any{"session_id": "fresh-thread", "observed_at": at}, ""},
	}
	for _, c := range cases {
		tx, err := db.BeginTx(context.Background(), nil)
		if err != nil {
			t.Fatal(err)
		}
		_, unknown, err := projectNativeActivity(context.Background(), tx, "", c.envAgent, c.payload)
		_ = tx.Rollback()
		got := ""
		if unknown != nil {
			got = unknown.AgentID + "/" + unknown.NativeID
		}
		if err != nil || got != c.want {
			t.Fatalf("%s: unknown=%q err=%v, want %q", c.name, got, err, c.want)
		}
	}
	var count int
	db.QueryRow(`SELECT count(*) FROM sessions`).Scan(&count)
	if count != 1 {
		t.Fatalf("projection must never create sessions, have %d", count)
	}
}
