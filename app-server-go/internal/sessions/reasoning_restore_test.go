package sessions

import (
	"astrorder.dev/app-server/internal/daemonclient"
	pb "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
	"context"
	"testing"
	"time"
)

func TestRestoreReasoningRequiresConfirmedNativeValue(t *testing.T) {
	srv := &fakeCombinedDaemonServer{currentProvider: "fixture", currentModel: "model", currentEffort: "max"}
	addr, stop := startFakeDaemonTarget(t, srv)
	defer stop()
	ctx := context.Background()
	dc, err := daemonclient.Dial(ctx, daemonclient.Config{Target: addr, Credential: "test-secret", RequestTimeout: 2 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer dc.Close()
	h := &Handler{client: dc}
	ref := &pb.SessionRef{SessionId: "s"}
	current, err := dc.Runtime.ReadModel(ctx, &pb.ReadModelRequest{Session: ref})
	if err != nil {
		t.Fatal(err)
	}
	if err := h.restoreReasoning(ctx, ref, map[string]any{"effort": "medium"}, current); err != nil {
		t.Fatal(err)
	}
	confirmed, err := dc.Runtime.ReadModel(ctx, &pb.ReadModelRequest{Session: ref})
	if err != nil || confirmed.GetEffort() != "medium" {
		t.Fatalf("readback=%v err=%v", confirmed, err)
	}
	srv.rejectNextSetReasoning = true
	if err := h.restoreReasoning(ctx, ref, map[string]any{"effort": "low"}, confirmed); err == nil {
		t.Fatal("rejected effort allowed send")
	}
}
