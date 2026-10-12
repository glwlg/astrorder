package projection_test

import (
	"astrorder.dev/app-server/internal/daemonclient"
	"astrorder.dev/app-server/internal/projection"
	"astrorder.dev/app-server/internal/store"
	pb "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestConnectorDeleteClearsDurableApproval(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	st, err := store.Open(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if _, err := st.UpsertSession(ctx, map[string]any{"agent_id": "a", "id": "s", "title": "fixture", "status": "waiting_approval", "updated_at": "2026-10-09T00:00:00Z"}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.AppendEvent(ctx, store.Event{ID: "approval", Type: "approval.upsert", AgentID: "a", SessionID: "s", Data: map[string]any{"id": "approval", "state": "pending"}}); err != nil {
		t.Fatal(err)
	}
	addr, stop := startFakeEventsServer(t, &fakeEventsServer{daemonID: "delete-epoch", frames: []*pb.EventStreamFrame{
		{Frame: &pb.EventStreamFrame_Snapshot{Snapshot: &pb.StateSnapshot{DaemonId: "delete-epoch"}}},
		{Frame: &pb.EventStreamFrame_LiveReady{LiveReady: &pb.LiveReady{DaemonId: "delete-epoch"}}},
		{Frame: &pb.EventStreamFrame_Live{Live: &pb.EventEnvelope{DaemonId: "delete-epoch", Session: &pb.SessionRef{SessionId: "connector-stream"}, SeqId: 1, EventType: "connector.event", NativePayload: makeNativePayload(map[string]any{"agent_id": "a", "type": "session.delete", "data": map[string]any{"id": "s"}})}}},
	}})
	defer stop()
	client, err := daemonclient.Dial(ctx, daemonclient.Config{Target: addr, Credential: "fixture", RequestTimeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	p, err := projection.New(client, st, &testPublisher{})
	if err != nil {
		t.Fatal(err)
	}
	runCtx, runCancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- p.RunReady(runCtx, make(chan struct{})) }()
	defer func() { runCancel(); <-done }()
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for {
		checkpoint, err := st.GetCheckpoint(ctx, "delete-epoch", "connector-stream")
		if err != nil {
			t.Fatal(err)
		}
		if checkpoint == 1 {
			break
		}
		select {
		case <-ticker.C:
		case <-ctx.Done():
			t.Fatal("delete event not committed")
		}
	}
	approvals, err := st.PendingApprovals(ctx)
	if err != nil || len(approvals) != 0 {
		t.Fatalf("deleted session has approvals=%v error=%v", approvals, err)
	}
	row, err := st.GetSession(ctx, "a", "s")
	if err != nil || row != nil {
		t.Fatalf("session was not deleted error=%v", err)
	}
}
