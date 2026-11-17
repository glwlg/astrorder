package store

import (
	"context"
	"path/filepath"
	"testing"
)

func TestQueueClaimPreventsDuplicateDispatchAndStaleEdit(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	payload := map[string]any{"id": "cmd", "agent_id": "a", "session_id": "s", "action": "enqueue", "text": "before"}
	if _, _, err = st.CreateCommand(ctx, payload, "queued"); err != nil {
		t.Fatal(err)
	}
	edited, err := st.UpdateQueuedCommandText(ctx, "a", "s", "cmd", "after")
	if err != nil || edited["text"] != "after" {
		t.Fatalf("edit=%+v %v", edited, err)
	}
	payload["text"] = "after"
	if _, created, err := st.CreateCommand(ctx, payload, "queued"); err != nil || created {
		t.Fatalf("updated hash not preserved: %v %v", created, err)
	}
	first, claimed, err := st.ClaimQueuedCommand(ctx, "a", "s", "cmd")
	if err != nil || !claimed || first["state"] != "received" {
		t.Fatalf("claim=%+v %v %v", first, claimed, err)
	}
	if _, claimed, err = st.ClaimQueuedCommand(ctx, "a", "s", "cmd"); err != nil || claimed {
		t.Fatalf("duplicate claim: %v %v", claimed, err)
	}
	if _, err = st.UpdateQueuedCommandText(ctx, "a", "s", "cmd", "late"); err == nil {
		t.Fatal("edited command after dispatch")
	}
	if row, cancelled, err := st.CancelQueuedCommand(ctx, "a", "s", "cmd"); err != nil || cancelled || row["state"] != "received" {
		t.Fatalf("cancelled dispatched command: %+v %v %v", row, cancelled, err)
	}
	payload["id"] = "cancel-first"
	if _, _, err = st.CreateCommand(ctx, payload, "queued"); err != nil {
		t.Fatal(err)
	}
	if row, cancelled, err := st.CancelQueuedCommand(ctx, "a", "s", "cancel-first"); err != nil || !cancelled || row["state"] != "cancelled" {
		t.Fatalf("cancel=%+v %v %v", row, cancelled, err)
	}
	if _, claimed, err := st.ClaimQueuedCommand(ctx, "a", "s", "cancel-first"); err != nil || claimed {
		t.Fatalf("claimed cancelled command: %v %v", claimed, err)
	}
}

func TestRemoveUnknownQueueRecordKeepsEvidenceAndNeverRequeues(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	for _, command := range []struct{ id, action string }{{"unknown-queue", "enqueue"}, {"unknown-send", "send"}} {
		if _, _, err := st.CreateCommand(ctx, map[string]any{"id": command.id, "agent_id": "a", "session_id": "s", "action": command.action, "text": "keep evidence"}, "unknown"); err != nil {
			t.Fatal(err)
		}
		if _, err := st.SetCommandState(ctx, "a", "s", command.id, "unknown", "delivery unconfirmed", false); err != nil {
			t.Fatal(err)
		}
	}
	row, removed, err := st.CancelQueuedCommand(ctx, "a", "s", "unknown-queue")
	if err != nil || !removed || row["state"] != "cancelled" || row["error"] != "delivery unconfirmed" {
		t.Fatalf("unknown queue removal = %+v %v %v", row, removed, err)
	}
	if _, claimed, err := st.ClaimQueuedCommand(ctx, "a", "s", "unknown-queue"); err != nil || claimed {
		t.Fatalf("removed unknown command was requeued: %v %v", claimed, err)
	}
	row, err = st.SetCommandState(ctx, "a", "s", "unknown-queue", "accepted", nil, true)
	if err != nil || row["state"] != "cancelled" {
		t.Fatalf("late receipt restored removed command: %+v %v", row, err)
	}
	row, removed, err = st.CancelQueuedCommand(ctx, "a", "s", "unknown-send")
	if err != nil || removed || row["state"] != "unknown" {
		t.Fatalf("removed unrelated direct send: %+v %v %v", row, removed, err)
	}
}

func TestAutomaticQueueClaimRechecksFIFOAndConflictingCommands(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	if _, err := st.UpsertAgent(ctx, map[string]any{"id": "a", "kind": "codex", "name": "fixture", "status": "ready"}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.UpsertSession(ctx, map[string]any{"id": "s", "agent_id": "a", "status": "idle", "control_state": "owned", "updated_at": "2026-10-30T00:00:00Z"}); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"first", "second"} {
		if _, _, err := st.CreateCommand(ctx, map[string]any{"id": id, "agent_id": "a", "session_id": "s", "action": "enqueue", "text": id}, "queued"); err != nil {
			t.Fatal(err)
		}
	}
	if _, ok, err := st.ClaimAutomaticQueuedCommand(ctx, "a", "s", "second", "s", "", "codex"); err != nil || ok {
		t.Fatalf("later command claimed before FIFO head: %v %v", ok, err)
	}
	if _, ok, err := st.ClaimAutomaticQueuedCommand(ctx, "a", "s", "first", "s", "different", "codex"); err != nil || ok {
		t.Fatalf("claimed a rebound session: %v %v", ok, err)
	}
	if _, ok, err := st.ClaimAutomaticQueuedCommand(ctx, "a", "s", "first", "s", "", "codex"); err != nil || !ok {
		t.Fatalf("first command was not claimed: %v %v", ok, err)
	}
	if _, ok, err := st.ClaimAutomaticQueuedCommand(ctx, "a", "s", "second", "s", "", "codex"); err != nil || ok {
		t.Fatalf("second command crossed an outstanding claim: %v %v", ok, err)
	}
	if _, ok, err := st.SetQueuedNotice(ctx, "a", "s", "first", "stale decision"); err != nil || ok {
		t.Fatalf("stale decision overwrote claimed command: %v %v", ok, err)
	}
}
