package projection

import (
	"context"
	"testing"
)

func TestLegacyAntigravityOwnerWithoutPIDProjectsStatusOnly(t *testing.T) {
	db := activityDB(t)
	if _, err := db.Exec(`CREATE TABLE agents(id TEXT PRIMARY KEY,kind TEXT); INSERT INTO agents VALUES('local-antigravity','antigravity'); INSERT INTO sessions(id,agent_id,status,updated_at) VALUES('s1','local-antigravity','running','2026-10-17 00:00:00')`); err != nil {
		t.Fatal(err)
	}
	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	events, err := projectRuntimeOwnerTx(context.Background(), tx, "s1", map[string]any{"agent_id": "local-antigravity", "runtime_owned": true, "status": "idle"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 || events[0].Type != "session.upsert" || events[0].Data["status"] != "idle" {
		t.Fatalf("legacy status: %+v", events)
	}
	var owner any
	if err = tx.QueryRow(`SELECT runtime_owner FROM sessions WHERE id='s1'`).Scan(&owner); err != nil {
		t.Fatal(err)
	}
	if owner != nil {
		t.Fatalf("legacy frame fabricated owner: %v", owner)
	}
	if _, err = projectRuntimeOwnerTx(context.Background(), tx, "s1", map[string]any{"agent_id": "local-antigravity", "runtime_owned": true, "status": "running"}, nil); err == nil {
		t.Fatal("nonterminal malformed owner accepted")
	}
}
