package store

import (
	"context"
	"path/filepath"
	"testing"
)

// TestAcceptanceMessageReupsertPreservesCanonicalCreatedAt reproduces the
// real defect found in the Go dev build: a repeated projection upsert of an
// existing message (no authoritative_created_at flag) preserved the previous
// created_at with fmt.Sprint, which formats the driver-scanned time.Time in
// Go's default "2006-01-02 15:04:05.999999999 -0700 MST" form. That string
// was written back into the DATETIME column, and the presence endpoint later
// failed with "last_user_at: invalid timestamp ... +0000 UTC" because
// MAX(created_at) is returned as a raw string that parseTime could not read.
func TestAcceptanceMessageReupsertPreservesCanonicalCreatedAt(t *testing.T) {
	ctx := context.Background()
	st, err := Open(filepath.Join(t.TempDir(), "app.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	if _, err := st.UpsertSession(ctx, map[string]any{
		"id": "session-a", "agent_id": "agent-a", "status": "idle",
		"updated_at": "2026-10-09T09:47:55Z",
	}); err != nil {
		t.Fatal(err)
	}

	payload := map[string]any{
		"id": "message-a", "agent_id": "agent-a", "session_id": "session-a",
		"role": "user", "kind": "message", "text": "hello",
		"attachments": []any{}, "created_at": "2026-10-09T09:47:55.337078Z",
	}
	if _, err := st.UpsertMessage(ctx, payload); err != nil {
		t.Fatal(err)
	}
	// The second projection of the same native row carries no
	// authoritative_created_at flag, exactly like the history endpoint.
	if _, err := st.UpsertMessage(ctx, payload); err != nil {
		t.Fatal(err)
	}

	var bad int
	if err := st.DB().QueryRowContext(ctx, "SELECT COUNT(*) FROM messages WHERE CAST(created_at AS TEXT) LIKE '% +0000 %'").Scan(&bad); err != nil {
		t.Fatal(err)
	}
	if bad != 0 {
		t.Fatalf("%d rows carry a Go-formatted timestamp after re-upsert", bad)
	}

	activity, err := st.PresenceActivityRows(ctx)
	if err != nil {
		t.Fatalf("presence failed on re-projected messages: %v", err)
	}
	if len(activity) != 0 {
		t.Fatalf("presence rows = %+v", activity)
	}
}

// TestAcceptanceWireTimeAcceptsGoDefaultTimeFormat proves reads tolerate the
// legacy Go time.Time.String() form already present in databases written by
// the pre-fix re-upsert path: rejecting it bricks the whole presence
// endpoint for every session in the database.
func TestAcceptanceWireTimeAcceptsGoDefaultTimeFormat(t *testing.T) {
	stamp, err := wireTime("2026-10-09 09:47:55.337078 +0000 UTC")
	if err != nil {
		t.Fatalf("legacy Go-formatted timestamp rejected: %v", err)
	}
	if stamp != "2026-10-09T09:47:55.337Z" {
		t.Fatalf("stamp = %q", stamp)
	}
	if _, err := wireTime("not a timestamp"); err == nil {
		t.Fatal("invalid timestamp was accepted")
	}
}
