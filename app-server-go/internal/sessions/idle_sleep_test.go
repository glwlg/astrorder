package sessions

import (
	pb "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
	"context"
	"testing"
	"time"
)

func TestIdleSleepProtectsViewAndResumesExactSession(t *testing.T) {
	ctx := context.Background()
	now := time.Unix(10000, 0)
	closed := 0
	snap := refreshOwnedSnapshot("native", "a", nil, pb.RuntimeKind_RUNTIME_KIND_CODEX, pb.SessionStatus_SESSION_STATUS_IDLE)
	srv := &mockDaemonServer{protocolMinor: 2, statusCb: func() (*pb.GetStatusResponse, error) {
		return &pb.GetStatusResponse{DaemonId: "test-daemon", Sessions: []*pb.SessionSnapshot{snap}}, nil
	}, closeCb: func(r *pb.CloseSessionRequest) (*pb.CommandReceipt, error) {
		if r.Session.SessionId != "native" || r.Session.GetAgentId() != "a" {
			t.Error("wrong identity")
		}
		closed++
		return &pb.CommandReceipt{OperationState: pb.OperationState_OPERATION_STATE_COMPLETED, Outcome: &pb.CommandReceipt_Completed{Completed: &pb.CommandResult{Accepted: true}}}, nil
	}}
	h, st, _, cleanup := setupTestServer(t, srv)
	defer cleanup()
	seedRefreshAgent(t, st, "a", "codex", "")
	seedRefreshSession(t, st, "a", "display", map[string]any{"source_session_id": "native", "status": "idle"})
	since := map[string]time.Time{}
	run := func(at time.Time) {
		t.Helper()
		if err := h.reclaimIdle(ctx, at, since); err != nil {
			t.Fatal(err)
		}
	}
	run(now)
	run(now.Add(9 * time.Minute))
	if closed != 0 {
		t.Fatal("slept too early")
	}
	h.views.touch("a::display", now.Add(10*time.Minute))
	run(now.Add(10 * time.Minute))
	if closed != 0 {
		t.Fatal("closed viewed session")
	}
	run(now.Add(12 * time.Minute))
	run(now.Add(22 * time.Minute))
	if closed != 1 {
		t.Fatalf("close count %d", closed)
	}
	pref, err := st.GetPreference(ctx, sleepPreferenceKey("a", "display"))
	if err != nil || pref.Value != true {
		t.Fatal("missing sleep marker", err)
	}
	row, err := st.GetSession(ctx, "a", "display")
	if err != nil || row == nil {
		t.Fatal("lost history identity")
	}
	if err := h.ensureNativeBinding(ctx, row); err != nil {
		t.Fatal(err)
	}
	pref, _ = st.GetPreference(ctx, sleepPreferenceKey("a", "display"))
	if pref.Exists {
		t.Fatal("explicit wake kept sleep marker")
	}
}
