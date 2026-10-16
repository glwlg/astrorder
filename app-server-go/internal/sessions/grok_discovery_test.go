package sessions

import (
	"context"
	"encoding/json"
	"net"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"astrorder.dev/app-server/internal/daemonclient"
	"astrorder.dev/app-server/internal/store"
	pb "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type grokDiscoveryServer struct {
	fakeCombinedDaemonServer
	mu       sync.Mutex
	requests []*pb.GrokSessionListQuery
	pages    map[string]map[string]any
}

func (s *grokDiscoveryServer) QueryNative(_ context.Context, req *pb.NativeQueryRequest) (*pb.NativeQueryResponse, error) {
	query := req.GetGrokSessionList()
	if query == nil || req.Target.GetRuntime() != pb.RuntimeKind_RUNTIME_KIND_GROK {
		return nil, status.Error(codes.InvalidArgument, "expected Grok list selector")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.requests = append(s.requests, query)
	body := s.pages[query.GetCursor()]
	if body == nil {
		body = map[string]any{"sessions": []any{}}
	}
	raw, _ := json.Marshal(body)
	return &pb.NativeQueryResponse{Result: &pb.NativeJson{Utf8Json: raw}}, nil
}

func newGrokDiscoveryFixture(t *testing.T) (*Handler, *store.Store, *grokDiscoveryServer) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if _, err := st.UpsertAgent(context.Background(), map[string]any{"id": "grok-1", "kind": "grok", "name": "Grok", "status": "ready"}); err != nil {
		t.Fatal(err)
	}
	srv := &grokDiscoveryServer{pages: map[string]map[string]any{}}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := grpc.NewServer()
	pb.RegisterDaemonControlServer(server, srv)
	pb.RegisterRuntimeQueryServer(server, srv)
	go server.Serve(listener)
	t.Cleanup(server.Stop)
	client, err := daemonclient.Dial(context.Background(), daemonclient.Config{Target: listener.Addr().String(), Credential: "test-secret", RequestTimeout: 2 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { client.Close() })
	return &Handler{store: st, client: client, publisher: &mockPublisher{}}, st, srv
}

func TestRefreshGrokRejectsExplicitPartialCatalog(t *testing.T) {
	h, st, srv := newGrokDiscoveryFixture(t)
	srv.pages[""] = map[string]any{"sessions": []any{map[string]any{"sessionId": "partial", "title": "A"}}, "_meta": map[string]any{"x.ai/partial": map[string]any{"conversations": true}}}
	report, err := h.RefreshActivity(context.Background(), "grok-1")
	if err != nil {
		t.Fatal(err)
	}
	entries := report["agents"].([]map[string]any)
	rows, _ := st.ListSessions(context.Background(), "grok-1")
	if len(entries) != 1 || entries[0]["error"] == nil || len(rows) != 0 {
		t.Fatalf("partial native catalog reported complete: %v rows=%v", report, rows)
	}
}

func TestRefreshGrokAcceptsOptionalActivityTimestamp(t *testing.T) {
	h, st, srv := newGrokDiscoveryFixture(t)
	srv.pages[""] = map[string]any{"sessions": []any{map[string]any{"sessionId": "no-time", "cwd": "P:/work"}}}
	report, err := h.RefreshActivity(context.Background(), "grok-1")
	row, readErr := st.GetSession(context.Background(), "grok-1", "no-time")
	if err != nil || readErr != nil || row == nil {
		t.Fatalf("optional ACP timestamp rejected: report=%v row=%v err=%v read=%v", report, row, err, readErr)
	}
}

func TestRefreshGrokPreservesRunningStatusAndManualTitle(t *testing.T) {
	h, st, srv := newGrokDiscoveryFixture(t)
	ctx := context.Background()
	_, err := st.UpsertSession(ctx, map[string]any{"id": "native-a", "agent_id": "grok-1", "title": "我的标题", "status": "running", "updated_at": "2026-10-16T00:00:00Z"})
	if err != nil {
		t.Fatal(err)
	}
	srv.pages[""] = map[string]any{"sessions": []any{map[string]any{"sessionId": "native-a", "title": "原生标题", "cwd": "P:/work", "updatedAt": "2026-10-16T04:00:00Z"}}}
	if _, err := h.RefreshActivity(ctx, "grok-1"); err != nil {
		t.Fatal(err)
	}
	row, err := st.GetSession(ctx, "grok-1", "native-a")
	if err != nil || row["status"] != "running" || row["title"] != "我的标题" {
		t.Fatalf("discovery overwrote user/live state: %v %v", row, err)
	}
	if _, err := st.DeleteSession(ctx, "grok-1", "native-a"); err != nil {
		t.Fatal(err)
	}
	if _, err := h.RefreshActivity(ctx, "grok-1"); err != nil {
		t.Fatal(err)
	}
	row, err = st.GetSession(ctx, "grok-1", "native-a")
	if err != nil || row != nil {
		t.Fatalf("removed session resurrected: %v %v", row, err)
	}
}

func TestRefreshGrokReportsRepeatedCursorWithoutPartialDiscovery(t *testing.T) {
	h, st, srv := newGrokDiscoveryFixture(t)
	page := map[string]any{"sessions": []any{map[string]any{"sessionId": "native-a", "title": "A"}}, "nextCursor": "repeat"}
	srv.pages[""] = page
	srv.pages["repeat"] = page
	report, err := h.RefreshActivity(context.Background(), "grok-1")
	if err != nil {
		t.Fatal(err)
	}
	entries := report["agents"].([]map[string]any)
	if len(entries) != 1 || entries[0]["error"] == nil {
		t.Fatalf("incomplete discovery reported success: %v", report)
	}
	rows, _ := st.ListSessions(context.Background(), "grok-1")
	if len(rows) != 0 {
		t.Fatalf("partial malformed catalog was persisted: %v", rows)
	}
}

func TestRefreshDiscoversGrokNativeSessionsAcrossPages(t *testing.T) {
	h, st, srv := newGrokDiscoveryFixture(t)
	srv.pages[""] = map[string]any{"sessions": []any{map[string]any{"sessionId": "native-a", "title": "原生 Grok 会话", "cwd": "P:/workspace/proj", "updatedAt": "2026-10-16T03:04:05Z"}}, "nextCursor": "page-two"}
	srv.pages["page-two"] = map[string]any{"sessions": []any{map[string]any{"sessionId": "native-b", "title": "第二页", "cwd": "P:/workspace/other", "updatedAt": "2026-10-16T04:05:06Z"}}}
	report, err := h.RefreshActivity(context.Background(), "grok-1")
	if err != nil {
		t.Fatal(err)
	}
	rows, err := st.ListSessions(context.Background(), "grok-1")
	if err != nil || len(rows) != 2 {
		t.Fatalf("Grok discovery omitted sessions: rows=%v report=%v err=%v", rows, report, err)
	}
	row, _ := st.GetSession(context.Background(), "grok-1", "native-a")
	if row["title"] != "原生 Grok 会话" || row["source_session_id"] != "native-a" || row["status"] != "idle" {
		t.Fatalf("native metadata lost: %v", row)
	}
	if len(srv.requests) != 2 || srv.requests[0].Cursor != nil || srv.requests[1].GetCursor() != "page-two" {
		t.Fatalf("pagination not preserved: %v", srv.requests)
	}
}
