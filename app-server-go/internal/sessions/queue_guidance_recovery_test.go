package sessions

import (
	pb "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestHermesAttachmentGuidanceFailureCanSendNextTurn(t *testing.T) {
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
	if _, err := st.UpsertAgent(ctx, map[string]any{"id": "a", "kind": "hermes", "name": "fixture", "status": "ready"}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.UpsertSession(ctx, map[string]any{"id": "s", "agent_id": "a", "status": "running", "control_state": "owned", "updated_at": "2026-10-15T00:00:00Z"}); err != nil {
		t.Fatal(err)
	}
	h.attachmentsDir = t.TempDir()
	blob := []byte("attachment preserved")
	if err := os.WriteFile(filepath.Join(h.attachmentsDir, "blob"), blob, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddAttachment(ctx, map[string]any{"id": "blob", "name": "photo.png", "media_type": "image/png", "size": len(blob), "storage_name": "blob"}); err != nil {
		t.Fatal(err)
	}
	calls := 0
	srv.sendCb = func(r *pb.SendRequest) (*pb.CommandReceipt, error) {
		calls++
		if len(r.Input) != 2 || !bytes.Equal(r.Input[1].GetAttachment().GetContent(), blob) {
			t.Fatal("lost attachment bytes")
		}
		return &pb.CommandReceipt{OperationState: pb.OperationState_OPERATION_STATE_COMPLETED, Accepted: true, Outcome: &pb.CommandReceipt_Completed{Completed: &pb.CommandResult{Accepted: true}}}, nil
	}
	payload := map[string]any{"id": "guidance", "agent_id": "a", "session_id": "s", "action": "enqueue", "text": "photos", "attachment_ids": []any{"blob"}}
	queued, err := h.Submit(ctx, payload)
	if err != nil {
		t.Fatal(err)
	}
	claimed, ok, err := st.ClaimQueuedCommand(ctx, "a", "s", "guidance")
	if err != nil || !ok {
		t.Fatal(err)
	}
	if _, err := h.submitStored(ctx, claimed); err == nil {
		t.Fatal("attachment guidance accepted")
	}
	failed, _ := st.GetCommand(ctx, "a", "s", "guidance")
	if failed["state"] != "failed" || calls != 0 {
		t.Fatalf("local rejection became uncertain: %+v calls=%d", failed, calls)
	}
	if _, cancelled, err := st.CancelQueuedCommand(ctx, "a", "s", "guidance"); err != nil || !cancelled {
		t.Fatal("failed guidance cannot be edited/deleted")
	}
	srv.snapshot.Status = pb.SessionStatus_SESSION_STATUS_IDLE
	queued["id"] = "next-turn"
	payload["id"] = "next-turn"
	if _, err := h.Submit(ctx, payload); err != nil {
		t.Fatal(err)
	}
	claimed, ok, err = st.ClaimQueuedCommand(ctx, "a", "s", "next-turn")
	if err != nil || !ok {
		t.Fatal(err)
	}
	accepted, err := h.submitStored(ctx, claimed)
	if err != nil || accepted["state"] != "accepted" || calls != 1 {
		t.Fatalf("retry failed: %+v %v calls=%d", accepted, err, calls)
	}
}
