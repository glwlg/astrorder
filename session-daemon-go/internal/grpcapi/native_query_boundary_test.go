package grpcapi_test

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	sessiondv1 "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
	core "astrorder.dev/session-daemon/internal/runtime"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type waitingNativeQuery struct {
	calls    atomic.Int32
	entered  chan struct{}
	returned chan struct{}
	result   map[string]any
}

func (q *waitingNativeQuery) Create(context.Context, core.Request) (core.Result, error) {
	panic("query cannot create")
}
func (q *waitingNativeQuery) Query(ctx context.Context, _ core.Request) (map[string]any, error) {
	q.calls.Add(1)
	if q.entered == nil {
		return q.result, nil
	}
	close(q.entered)
	<-ctx.Done()
	close(q.returned)
	return nil, ctx.Err()
}
func simpleNativeQuery() *sessiondv1.NativeQueryRequest {
	return &sessiondv1.NativeQueryRequest{Target: &sessiondv1.RuntimeTarget{Runtime: sessiondv1.RuntimeKind_RUNTIME_KIND_HERMES}, Query: &sessiondv1.NativeQueryRequest_HermesModelsList{HermesModelsList: &sessiondv1.HermesModelsListQuery{}}}
}

func TestQueryNativeRejectsSharedDaemonDrainBeforeDispatch(t *testing.T) {
	f := startFixture(t)
	q := &waitingNativeQuery{result: map[string]any{}}
	f.daemon.RegisterRuntime("hermes", q)
	decision, err := f.daemon.AuthorizeShutdown(nil, false)
	if err != nil || !decision.Accepted {
		t.Fatalf("isolated idle shutdown admission: %+v %v", decision, err)
	}
	defer f.daemon.CancelShutdown()
	client := sessiondv1.NewRuntimeQueryClient(f.conn)
	if _, err := client.QueryNative(f.ctx, simpleNativeQuery()); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("drain response: %v", err)
	}
	if q.calls.Load() != 0 {
		t.Fatal("query dispatched while draining")
	}
	f.daemon.CancelShutdown()
	if _, err := client.QueryNative(f.ctx, simpleNativeQuery()); err != nil {
		t.Fatal(err)
	}
}

func TestQueryNativeCancellationReleasesNativeAdmission(t *testing.T) {
	f := startFixture(t)
	q := &waitingNativeQuery{entered: make(chan struct{}), returned: make(chan struct{})}
	f.daemon.RegisterRuntime("hermes", q)
	ctx, cancel := context.WithCancel(f.ctx)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := sessiondv1.NewRuntimeQueryClient(f.conn).QueryNative(ctx, simpleNativeQuery())
		done <- err
	}()
	select {
	case <-q.entered:
	case <-time.After(3 * time.Second):
		t.Fatal("native query not entered")
	}
	if _, err := f.daemon.AuthorizeShutdown(nil, false); err == nil {
		f.daemon.CancelShutdown()
		t.Fatal("busy query invisible to daemon shutdown authority")
	}
	cancel()
	select {
	case err := <-done:
		if status.Code(err) != codes.Canceled {
			t.Fatalf("cancelled query: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("client cancellation stalled")
	}
	select {
	case <-q.returned:
	case <-time.After(3 * time.Second):
		t.Fatal("native runtime did not receive cancellation")
	}
	// A fresh call proves transport reuse; shutdown authority below proves the
	// cancelled server handler released both shared admission leases.
	f.daemon.RegisterRuntime("hermes", &waitingNativeQuery{result: map[string]any{}})
	if _, err := sessiondv1.NewRuntimeQueryClient(f.conn).QueryNative(f.ctx, simpleNativeQuery()); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		decision, err := f.daemon.AuthorizeShutdown(nil, false)
		if err == nil && decision.Accepted {
			f.daemon.CancelShutdown()
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("cancelled query retained admission: %v", err)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestQueryNativeBoundsAggregateRequestAndNativeResponse(t *testing.T) {
	f := startFixture(t)
	q := &waitingNativeQuery{result: map[string]any{"large": strings.Repeat("x", 16_000_000)}}
	f.daemon.RegisterRuntime("hermes", q)
	client := sessiondv1.NewRuntimeQueryClient(f.conn)
	request := simpleNativeQuery()
	request.GetHermesModelsList().Options = &sessiondv1.NativeJson{Utf8Json: []byte(`{"extension":"` + strings.Repeat("x", 8_000_000) + `"}`)}
	request.TopLevelPayload = &sessiondv1.NativeJson{Utf8Json: []byte(`"` + strings.Repeat("x", 8_000_000) + `"`)}
	if _, err := client.QueryNative(f.ctx, request); status.Code(err) != codes.ResourceExhausted {
		t.Fatalf("aggregate request: %v", err)
	}
	if q.calls.Load() != 0 {
		t.Fatal("oversize aggregate request reached native runtime")
	}
	if _, err := client.QueryNative(f.ctx, simpleNativeQuery()); status.Code(err) != codes.ResourceExhausted {
		t.Fatalf("oversize native response: %v", err)
	}
	if q.calls.Load() != 1 {
		t.Fatalf("native response wasn't exercised: %d", q.calls.Load())
	}
}
