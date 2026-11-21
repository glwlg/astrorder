package projection

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"

	"astrorder.dev/app-server/internal/daemonclient"
	"astrorder.dev/app-server/internal/store"
	pb "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
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
	if _, err = projectRuntimeOwnerTx(context.Background(), tx, "s1", map[string]any{"agent_id": "local-antigravity", "runtime_owned": true, "status": "running"}, nil); !errors.Is(err, ErrCorruptedFrame) {
		t.Fatalf("expected local owner without PID to be rejected, got %v", err)
	}
}

func TestSSHAntigravityOwnerWithoutPIDReplaysStatusAndAdvancesCheckpoint(t *testing.T) {
	ctx := context.Background()
	const (
		epoch        = "epoch"
		streamID     = "session"
		connectionID = "ssh-remote"
		agentID      = "ssh-antigravity-" + connectionID
	)
	st, err := store.Open(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if _, err := st.UpsertAgent(ctx, map[string]any{
		"id": agentID, "kind": "antigravity", "name": "Antigravity", "status": "connected", "connection_id": connectionID,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.UpsertSession(ctx, map[string]any{
		"id": streamID, "agent_id": agentID, "status": "idle", "connection_id": connectionID,
		"updated_at": "2026-11-20T00:00:00Z",
	}); err != nil {
		t.Fatal(err)
	}
	pub := &noticePublisher{}
	p, err := New(&daemonclient.Client{}, st, pub)
	if err != nil {
		t.Fatal(err)
	}
	expected := map[string]int64{}
	for i, state := range []struct {
		name   string
		status pb.SessionStatus
	}{
		{"running", pb.SessionStatus_SESSION_STATUS_RUNNING},
		{"idle", pb.SessionStatus_SESSION_STATUS_IDLE},
		{"error", pb.SessionStatus_SESSION_STATUS_ERROR},
	} {
		seq := int64(i + 1)
		payload := map[string]any{
			"agent_id": agentID, "session_id": streamID, "runtime_owned": true,
			"status": state.name, "connection_id": connectionID,
		}
		raw, err := json.Marshal(payload)
		if err != nil {
			t.Fatal(err)
		}
		env := &pb.EventEnvelope{
			DaemonId: epoch, Session: &pb.SessionRef{SessionId: streamID},
			SeqId: seq, EventType: "runtime.owner", Status: state.status,
			Payload:       &pb.EventEnvelope_RuntimeOwner{RuntimeOwner: &pb.RuntimeOwnerEvent{}},
			NativePayload: &pb.NativeJson{Schema: "astrorder.event.payload.v1", Utf8Json: raw},
		}
		if err := p.processEnvelope(ctx, epoch, env, expected); err != nil {
			t.Fatalf("replay %s: %v", state.name, err)
		}
		if cp, err := st.GetCheckpoint(ctx, epoch, streamID); err != nil || cp != seq {
			t.Fatalf("checkpoint = %d, want %d, err = %v", cp, seq, err)
		}
		var status string
		var owner any
		err = st.DB().QueryRowContext(ctx, `SELECT status, runtime_owner FROM sessions WHERE agent_id=? AND id=?`, agentID, streamID).Scan(&status, &owner)
		if err != nil || status != state.name || owner != nil {
			t.Fatalf("projected status = %q, owner = %v, err = %v", status, owner, err)
		}
	}
	if len(pub.events) != 6 {
		t.Fatalf("published %d events, want 6", len(pub.events))
	}
	for i := 0; i < len(pub.events); i += 2 {
		if pub.events[i].Type != "session.upsert" || pub.events[i+1].Type != "session.status" {
			t.Fatalf("unexpected status events: %+v", pub.events[i:i+2])
		}
	}
}
