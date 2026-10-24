package connections_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	pb "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
)

func TestConfirmedConnectSchedulesSessionRecoveryWithoutBlockingRequest(t *testing.T) {
	daemon := &fakeDaemonService{}
	h, _ := setupRestoreFixture(t, daemon, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	started, release, finished := make(chan string, 1), make(chan struct{}), make(chan struct{})
	defer close(release)
	h.SetSessionRecovery(ctx, func(ctx context.Context, conn, kind string) error {
		started <- conn + ":" + kind
		select {
		case <-release:
		case <-ctx.Done():
		}
		close(finished)
		return nil
	})
	done := make(chan int, 1)
	go func() {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/v1/environments/local/agents/codex/connect", nil))
		done <- w.Code
	}()
	select {
	case code := <-done:
		if code != 200 {
			t.Fatalf("HTTP %d", code)
		}
	case <-time.After(time.Second):
		t.Fatal("connect waited for scan")
	}
	select {
	case key := <-started:
		if key != "local:codex" {
			t.Fatal(key)
		}
	case <-time.After(time.Second):
		t.Fatal("connect did not schedule recovery")
	}
	cancel()
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("recovery did not stop with App context")
	}
}

func TestRapidDisconnectReconnectSchedulesFreshRecovery(t *testing.T) {
	daemon := &fakeDaemonService{}
	h, _ := setupRestoreFixture(t, daemon, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	started := make(chan struct{}, 2)
	release := make(chan struct{})
	defer close(release)
	h.SetSessionRecovery(ctx, func(ctx context.Context, _, _ string) error {
		started <- struct{}{}
		<-release
		return ctx.Err()
	})
	for _, action := range []string{"connect", "disconnect", "connect"} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/v1/environments/local/agents/codex/"+action, nil))
		if w.Code != 200 {
			t.Fatalf("%s: %d", action, w.Code)
		}
		if action == "connect" {
			select {
			case <-started:
			case <-time.After(time.Second):
				t.Fatal("fresh connection lost its recovery scan")
			}
		}
	}
}

func TestRestoreSchedulesAlreadyOwnedConnectionOnce(t *testing.T) {
	daemon := &fakeDaemonService{}
	h, st := setupRestoreFixture(t, daemon, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := st.SetAgentConnectionChoice(ctx, "local:codex", true); err != nil {
		t.Fatal(err)
	}
	owned := true
	kind := pb.RuntimeKind_RUNTIME_KIND_CODEX
	daemon.statusResponse.Store(&pb.GetStatusResponse{DaemonId: "daemon-test-123", Sessions: []*pb.SessionSnapshot{{Session: &pb.SessionRef{SessionId: "probe-codex-local"}, Runtime: &kind, RuntimeOwned: &owned}}})
	started := make(chan string, 4)
	h.SetSessionRecovery(ctx, func(_ context.Context, conn, kind string) error { started <- conn + ":" + kind; return nil })
	for range 3 {
		if err := h.Restore(ctx); err != nil {
			t.Fatal(err)
		}
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("startup did not scan existing connection")
	}
	select {
	case key := <-started:
		t.Fatalf("repeated scan on unchanged connection: %s", key)
	case <-time.After(50 * time.Millisecond):
	}
	if err := st.SetAgentConnectionChoice(ctx, "local:codex", false); err != nil {
		t.Fatal(err)
	}
	if err := h.Restore(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case key := <-started:
		t.Fatalf("disabled Agent scanned: %s", key)
	case <-time.After(50 * time.Millisecond):
	}
}
