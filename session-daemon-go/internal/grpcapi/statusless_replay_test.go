package grpcapi_test

import (
	"context"
	"testing"
	"time"

	pb "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
)

func TestSubscribeReplaysStatuslessHistoryBeforeHello(t *testing.T) {
	f := startFixture(t)
	const sid = "probe-antigravity-local"
	for seq := 1; seq <= 130; seq++ {
		if err := f.daemon.Emit(sid, "connector.event", map[string]any{"type": "session.upsert"}, ""); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithTimeout(f.ctx, 5*time.Second)
	defer cancel()
	stream, err := pb.NewEventStreamClient(f.conn).Subscribe(ctx, &pb.SubscribeRequest{IncludeSnapshot: true, ReplayPageSize: 17})
	if err != nil {
		t.Fatal(err)
	}
	frame, err := stream.Recv()
	if err != nil {
		t.Fatal(err)
	}
	snapshot := frame.GetSnapshot()
	if snapshot == nil || len(snapshot.Sessions) != 1 {
		t.Fatalf("missing statusless history snapshot: %v", frame)
	}
	s := snapshot.Sessions[0]
	if s.Session.SessionId != sid || s.MinSeqId != 1 || s.ReplayThroughSeqId != 130 {
		t.Fatalf("snapshot=%v", s)
	}
	for seq := int64(1); seq <= 130; seq++ {
		frame, err = stream.Recv()
		if err != nil {
			t.Fatal(err)
		}
		event := frame.GetReplay()
		if event == nil || event.SeqId != seq || event.Session.SessionId != sid {
			t.Fatalf("expected replay %d, got %v", seq, frame)
		}
	}
	frame, err = stream.Recv()
	if err != nil {
		t.Fatal(err)
	}
	ready := frame.GetLiveReady()
	if ready == nil || len(ready.Watermarks) != 1 || ready.Watermarks[0].ReplayThroughSeqId != 130 {
		t.Fatalf("invalid ready: %v", frame)
	}
	if err := f.daemon.Emit(sid, "connector.hello", map[string]any{"id": "local-antigravity", "kind": "antigravity"}, ""); err != nil {
		t.Fatal(err)
	}
	frame, err = stream.Recv()
	if err != nil {
		t.Fatal(err)
	}
	if live := frame.GetLive(); live == nil || live.SeqId != 131 {
		t.Fatalf("invalid live hello: %v", frame)
	}
}
