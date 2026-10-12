package grpcapi_test

import (
	"context"
	"testing"
	"time"

	sessiondv1 "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
	core "astrorder.dev/session-daemon/internal/runtime"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type blockedSessionRead struct {
	sessionReadFixture
	entered, returned chan struct{}
}

func (a *blockedSessionRead) Create(context.Context, core.Request) (core.Result, error) {
	return core.Result{SessionID: "read-owned", Status: "idle", Payload: map[string]any{"agent_id": "agent-fixture"}}, nil
}

func (a *blockedSessionRead) Command(ctx context.Context, r core.Request) (core.Result, error) {
	close(a.entered)
	<-ctx.Done()
	close(a.returned)
	return core.Result{}, ctx.Err()
}
func TestSessionReadCancellationReleasesAdmissionAndDrainRejects(t *testing.T) {
	f := startFixture(t)
	a := &blockedSessionRead{entered: make(chan struct{}), returned: make(chan struct{})}
	f.daemon.RegisterRuntime("hermes", a)
	// Fixture owns idle session to isolate in-flight admission from activity.
	legacyWSOperation(t, f.daemon, map[string]any{"action": "session.create", "agent_type": "hermes"})
	client := sessiondv1.NewRuntimeQueryClient(f.conn)
	request := &sessiondv1.ListModelsRequest{Session: &sessiondv1.SessionRef{SessionId: "read-owned"}}
	ctx, cancel := context.WithCancel(f.ctx)
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := client.ListModels(ctx, request); done <- err }()
	select {
	case <-a.entered:
	case <-time.After(3 * time.Second):
		t.Fatal("read never entered")
	}
	if _, err := f.daemon.AuthorizeShutdown(nil, false); err == nil {
		f.daemon.CancelShutdown()
		t.Fatal("in-flight read invisible")
	}
	cancel()
	select {
	case err := <-done:
		if status.Code(err) != codes.Canceled {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("cancelled RPC stalled")
	}
	select {
	case <-a.returned:
	case <-time.After(3 * time.Second):
		t.Fatal("native context retained")
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		decision, err := f.daemon.AuthorizeShutdown(nil, false)
		if err == nil && decision.Accepted {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("cancelled read retained shared admission: %v", err)
		}
		time.Sleep(time.Millisecond)
	}
	defer f.daemon.CancelShutdown()
	if _, err := client.ListModels(f.ctx, request); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("drain read: %v", err)
	}
}

func TestSessionReadHistoryRejectsMalformedNativePage(t *testing.T) {
	for _, payload := range []map[string]any{
		{"thread": map[string]any{"turns": []any{}}},
		{"items": []any{1, 2}, "next_cursor": nil},
		{"items": []any{1}, "has_more": true, "next_cursor": nil},
	} {
		f := startFixture(t)
		a := &sessionReadFixture{replies: map[string]map[string]any{"session.history_page": payload}}
		f.daemon.RegisterRuntime("hermes", a)
		legacyWSOperation(t, f.daemon, map[string]any{"action": "session.create", "agent_type": "hermes"})
		if _, err := sessiondv1.NewRuntimeQueryClient(f.conn).GetHistoryPage(f.ctx, &sessiondv1.HistoryPageRequest{Session: &sessiondv1.SessionRef{SessionId: "read-owned"}, Limit: 1}); status.Code(err) != codes.Unavailable {
			t.Fatalf("malformed native page accepted: %v %#v", err, payload)
		}
	}
}
