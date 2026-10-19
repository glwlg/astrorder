package projection_test

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"astrorder.dev/app-server/internal/daemonclient"
	"astrorder.dev/app-server/internal/projection"
	"astrorder.dev/app-server/internal/store"
	pb "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
	"google.golang.org/grpc"
)

func TestProjectionLiveGapReplaysWithoutSkipping(t *testing.T) {
	for _, persistent := range []bool{false, true} {
		t.Run(fmt.Sprint("persistent=", persistent), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			st, err := store.Open(filepath.Join(t.TempDir(), "app.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer st.Close()
			const epoch = "live-gap-reconnect"
			const sid = "probe-antigravity-local"
			var subscriptions atomic.Int32
			fake := &fakeEventsServer{daemonID: epoch}
			fake.subscribe = func(req *pb.SubscribeRequest, stream grpc.ServerStreamingServer[pb.EventStreamFrame]) error {
				n := subscriptions.Add(1)
				if len(req.Cursors) != 0 {
					return fmt.Errorf("gap must not advance checkpoint: %v", req.Cursors)
				}
				through := int64(0)
				if n > 1 {
					through = 131
				}
				snap := &pb.StateSnapshot{DaemonId: epoch}
				ready := &pb.LiveReady{DaemonId: epoch}
				if n > 1 {
					snap.Sessions = []*pb.SessionSnapshot{{Session: &pb.SessionRef{SessionId: sid}, MinSeqId: 1, ReplayThroughSeqId: through}}
					ready.Watermarks = []*pb.SessionWatermark{{Session: &pb.SessionRef{SessionId: sid}, ReplayThroughSeqId: through}}
				}
				if err := stream.Send(&pb.EventStreamFrame{Frame: &pb.EventStreamFrame_Snapshot{Snapshot: snap}}); err != nil {
					return err
				}
				envelope := func(seq int64) *pb.EventEnvelope {
					return &pb.EventEnvelope{DaemonId: epoch, Session: &pb.SessionRef{SessionId: sid}, SeqId: seq, EventType: "ping", NativePayload: makeNativePayload(map[string]any{})}
				}
				if n > 1 {
					start := int64(1)
					if persistent {
						start = 131
					}
					for seq := start; seq <= 131; seq++ {
						if err := stream.Send(&pb.EventStreamFrame{Frame: &pb.EventStreamFrame_Replay{Replay: envelope(seq)}}); err != nil {
							return err
						}
					}
				}
				if err := stream.Send(&pb.EventStreamFrame{Frame: &pb.EventStreamFrame_LiveReady{LiveReady: ready}}); err != nil {
					return err
				}
				if n == 1 {
					if err := stream.Send(&pb.EventStreamFrame{Frame: &pb.EventStreamFrame_Live{Live: envelope(131)}}); err != nil {
						return err
					}
				}
				<-stream.Context().Done()
				return stream.Context().Err()
			}
			addr, stop := startFakeEventsServer(t, fake)
			defer stop()
			client, err := daemonclient.Dial(ctx, daemonclient.Config{Target: addr, Credential: "cred"})
			if err != nil {
				t.Fatal(err)
			}
			defer client.Close()
			proj, err := projection.New(client, st, &testPublisher{})
			if err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() { done <- proj.Run(ctx) }()
			if persistent {
				select {
				case err := <-done:
					if !errors.Is(err, projection.ErrNonContiguous) {
						t.Fatalf("real replay gap must fail: %v", err)
					}
				case <-ctx.Done():
					t.Fatal("persistent gap did not fail")
				}
				cp, _ := st.GetCheckpoint(ctx, epoch, sid)
				if cp != 0 {
					t.Fatalf("gap advanced checkpoint: %d", cp)
				}
			} else {
				for {
					cp, err := st.GetCheckpoint(ctx, epoch, sid)
					if err != nil {
						t.Fatal(err)
					}
					if cp == 131 {
						break
					}
					select {
					case err := <-done:
						t.Fatalf("live gap crashed projection instead of replaying: %v", err)
					case <-ctx.Done():
						t.Fatal("replay did not complete")
					case <-time.After(10 * time.Millisecond):
					}
				}
				cancel()
				<-done
			}
			if subscriptions.Load() != 2 {
				t.Fatalf("expected exactly one replay reconnect, got %d", subscriptions.Load())
			}
		})
	}
}
