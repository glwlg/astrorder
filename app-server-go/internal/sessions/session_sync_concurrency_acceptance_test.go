package sessions

import (
	pb "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
	"context"
	"net/http"
	"testing"
	"time"
)

// A status refresh must not overwrite product fields changed while the
// authority RPC was in flight.
func TestAcceptanceSyncPreservesConcurrentSessionMetadata(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	srv := &refreshDaemonServer{statusCb: func() (*pb.GetStatusResponse, error) {
		close(entered)
		<-release
		return &pb.GetStatusResponse{DaemonId: "test-daemon", Sessions: []*pb.SessionSnapshot{refreshOwnedSnapshot("s", "a", nil, pb.RuntimeKind_RUNTIME_KIND_HERMES, pb.SessionStatus_SESSION_STATUS_IDLE)}}, nil
	}}
	h, st, _, cleanup := setupRefreshServer(t, srv)
	defer cleanup()
	seedRefreshAgent(t, st, "a", "hermes", "")
	seedRefreshSession(t, st, "a", "s", map[string]any{"title": "old title", "workspace": "C:/old", "status": "running"})
	result := make(chan int, 1)
	go func() { result <- refreshCall(h, http.MethodPost, "/api/v1/sessions/s/sync?agent_id=a", true).Code }()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("sync never read daemon status")
	}
	if _, err := st.UpdateSession(context.Background(), "a", "s", map[string]any{"title": "user changed title", "workspace": "C:/new"}); err != nil {
		close(release)
		t.Fatal(err)
	}
	close(release)
	select {
	case code := <-result:
		if code != 200 {
			t.Fatalf("sync status=%d", code)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("sync never completed")
	}
	row, err := st.GetSession(context.Background(), "a", "s")
	if err != nil {
		t.Fatal(err)
	}
	if row["title"] != "user changed title" || row["workspace"] != "C:/new" {
		t.Fatalf("status refresh overwrote concurrent metadata: title=%v workspace=%v", row["title"], row["workspace"])
	}
	if row["status"] != "idle" {
		t.Fatalf("authoritative status not refreshed: %v", row["status"])
	}
}

// A deletion that lands while the authority RPC is in flight must surface as
// not-found: no resurrect, no fabricated event.
func TestAcceptanceSyncDoesNotResurrectSessionDeletedInFlight(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	srv := &refreshDaemonServer{statusCb: func() (*pb.GetStatusResponse, error) {
		close(entered)
		<-release
		return &pb.GetStatusResponse{DaemonId: "test-daemon", Sessions: []*pb.SessionSnapshot{refreshOwnedSnapshot("s", "a", nil, pb.RuntimeKind_RUNTIME_KIND_HERMES, pb.SessionStatus_SESSION_STATUS_IDLE)}}, nil
	}}
	h, st, pub, cleanup := setupRefreshServer(t, srv)
	defer cleanup()
	seedRefreshAgent(t, st, "a", "hermes", "")
	seedRefreshSession(t, st, "a", "s", map[string]any{"status": "running"})
	result := make(chan int, 1)
	go func() { result <- refreshCall(h, http.MethodPost, "/api/v1/sessions/s/sync?agent_id=a", true).Code }()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("sync never read daemon status")
	}
	if deleted, err := st.DeleteSession(context.Background(), "a", "s"); err != nil || !deleted {
		close(release)
		t.Fatalf("delete in flight: deleted=%v err=%v", deleted, err)
	}
	close(release)
	select {
	case code := <-result:
		if code != http.StatusNotFound {
			t.Fatalf("sync of deleted session status=%d", code)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("sync never completed")
	}
	if row, err := st.GetSession(context.Background(), "a", "s"); err != nil || row != nil {
		t.Fatalf("deleted session resurrected: %+v err=%v", row, err)
	}
	if len(pub.events) != 0 {
		t.Fatalf("deleted sync published events: %+v", pub.events)
	}
}

// A rebinding that lands while the authority RPC is in flight must reject the
// stale snapshot instead of applying it to the new binding.
func TestAcceptanceSyncRejectsReboundSessionInFlight(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	srv := &refreshDaemonServer{statusCb: func() (*pb.GetStatusResponse, error) {
		close(entered)
		<-release
		return &pb.GetStatusResponse{DaemonId: "test-daemon", Sessions: []*pb.SessionSnapshot{refreshOwnedSnapshot("s", "a", nil, pb.RuntimeKind_RUNTIME_KIND_HERMES, pb.SessionStatus_SESSION_STATUS_IDLE)}}, nil
	}}
	h, st, pub, cleanup := setupRefreshServer(t, srv)
	defer cleanup()
	seedRefreshAgent(t, st, "a", "hermes", "")
	seedRefreshSession(t, st, "a", "s", map[string]any{"status": "running", "title": "keep"})
	result := make(chan int, 1)
	go func() { result <- refreshCall(h, http.MethodPost, "/api/v1/sessions/s/sync?agent_id=a", true).Code }()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("sync never read daemon status")
	}
	if _, err := st.DB().ExecContext(context.Background(), "UPDATE sessions SET source_session_id=? WHERE agent_id=? AND id=?", "native-other", "a", "s"); err != nil {
		close(release)
		t.Fatal(err)
	}
	close(release)
	select {
	case code := <-result:
		if code != http.StatusConflict {
			t.Fatalf("sync of rebound session status=%d", code)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("sync never completed")
	}
	row, err := st.GetSession(context.Background(), "a", "s")
	if err != nil || row["status"] != "running" || row["title"] != "keep" || row["source_session_id"] != "native-other" {
		t.Fatalf("rebound session mutated: %+v err=%v", row, err)
	}
	if len(pub.events) != 0 {
		t.Fatalf("rebound sync published events: %+v", pub.events)
	}
}
