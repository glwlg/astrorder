package sessions

import (
	"astrorder.dev/app-server/internal/store"
	pb "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestQueueDispatchRequiresLiveIdleAndSurvivesHandlerReplacement(t *testing.T) {
	owned, aid, kind := true, "a", pb.RuntimeKind_RUNTIME_KIND_HERMES
	srv := &guidanceModelServer{snapshot: &pb.SessionSnapshot{Session: &pb.SessionRef{SessionId: "s", AgentId: &aid}, RuntimeOwned: &owned, Runtime: &kind, Status: pb.SessionStatus_SESSION_STATUS_RUNNING}}
	h, st, _, cleanup := setupTestServer(t, &srv.mockDaemonServer)
	defer cleanup()
	addr, stop := startGuidanceStatusTarget(t, srv)
	defer stop()
	client := dialModelRestoreTarget(t, addr)
	defer client.Close()
	h.client.Daemon = client.Daemon
	h.client.Session = client.Session
	ctx := context.Background()
	_, err := st.UpsertAgent(ctx, map[string]any{"id": "a", "kind": "hermes", "name": "fixture", "status": "ready"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = st.UpsertSession(ctx, map[string]any{"id": "s", "agent_id": "a", "title": "fixture", "status": "idle", "control_state": "owned", "updated_at": "2026-10-15T00:00:00Z"})
	if err != nil {
		t.Fatal(err)
	}
	h.attachmentsDir = t.TempDir()
	blob := []byte("durable attachment fixture")
	if err := os.WriteFile(filepath.Join(h.attachmentsDir, "blob"), blob, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddAttachment(ctx, map[string]any{"id": "blob", "name": "photo.png", "media_type": "image/png", "size": len(blob), "storage_name": "blob"}); err != nil {
		t.Fatal(err)
	}
	calls := 0
	srv.sendCb = func(r *pb.SendRequest) (*pb.CommandReceipt, error) {
		calls++
		if r.GetMeta().GetCommandId() == "first" {
			if len(r.Input) != 2 || !bytes.Equal(r.Input[1].GetAttachment().GetContent(), blob) {
				t.Fatal("durable attachment bytes were lost")
			}
		}
		if string(r.GetPolicy().GetNativeOptions().GetUtf8Json()) != `{"idle_only":true}` {
			t.Error("queue lost idle-only admission")
		}
		return &pb.CommandReceipt{OperationState: pb.OperationState_OPERATION_STATE_COMPLETED, Accepted: true, Outcome: &pb.CommandReceipt_Completed{Completed: &pb.CommandResult{Accepted: true}}}, nil
	}
	for _, id := range []string{"first", "second"} {
		payload := map[string]any{"id": id, "agent_id": "a", "session_id": "s", "action": "enqueue", "text": id}
		if id == "first" {
			payload["attachment_ids"] = []any{"blob"}
		}
		_, err = h.Submit(ctx, payload)
		if err != nil {
			t.Fatal(err)
		}
	}
	dispatch := func() {
		t.Helper()
		runner, ok := any(h).(interface{ DispatchQueue(context.Context) error })
		if !ok {
			t.Fatal("server queue dispatcher is missing")
		}
		if err := runner.DispatchQueue(ctx); err != nil {
			t.Fatal(err)
		}
	}
	dispatch()
	if calls != 0 {
		t.Fatal("queued message steered into running turn")
	}
	srv.snapshot.Status = pb.SessionStatus_SESSION_STATUS_IDLE
	// Reopen the database and construct a new handler, as after an App restart.
	database, err := st.QueryRow(ctx, "PRAGMA database_list")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := store.Open(database["file"].(string))
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	st = reopened
	replacement, err := NewHandler(Options{Store: st, Client: h.client, Authorizer: h.authorizer, Publisher: h.publisher, AttachmentsDir: h.attachmentsDir})
	if err != nil {
		t.Fatal(err)
	}
	h = replacement.(*Handler)
	dispatch()
	dispatch()
	if calls != 1 {
		t.Fatalf("expected one FIFO dispatch, got %d", calls)
	}
	first, _ := st.GetCommand(ctx, "a", "s", "first")
	second, _ := st.GetCommand(ctx, "a", "s", "second")
	if first["state"] != "accepted" || second["state"] != "queued" {
		t.Fatalf("lost receipt or order: %v %v", first, second)
	}
	_, err = st.SetCommandState(ctx, "a", "s", "first", "completed", nil, false)
	if err != nil {
		t.Fatal(err)
	}
	dispatch()
	if calls != 2 {
		t.Fatalf("next message did not dispatch after completion: %d", calls)
	}
}
