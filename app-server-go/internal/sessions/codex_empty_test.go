package sessions

import (
	"astrorder.dev/app-server/internal/daemonclient"
	pb "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
	"context"
	"encoding/json"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"net"
	"net/http/httptest"
	"testing"
	"time"
)

type emptyThreadServer struct {
	fakeCombinedDaemonServer
	thread    map[string]any
	readError bool
	queries   int
}

func (s *emptyThreadServer) GetHistoryPage(context.Context, *pb.HistoryPageRequest) (*pb.HistoryPageResponse, error) {
	return nil, status.Error(codes.Unavailable, "native session read failed")
}
func (s *emptyThreadServer) ListBackgroundProcesses(context.Context, *pb.ListBackgroundProcessesRequest) (*pb.ListBackgroundProcessesResponse, error) {
	return nil, status.Error(codes.Unavailable, "native session read failed")
}
func (s *emptyThreadServer) QueryNative(_ context.Context, r *pb.NativeQueryRequest) (*pb.NativeQueryResponse, error) {
	s.queries++
	if r.GetCodexThreadRead() == nil || s.readError {
		return nil, status.Error(codes.Unavailable, "native runtime query failed")
	}
	if r.GetCodexThreadRead().ThreadId != "empty" || r.GetCodexThreadRead().IncludeTurns == nil || !*r.GetCodexThreadRead().IncludeTurns {
		return nil, status.Error(codes.InvalidArgument, "must read actual turns")
	}
	raw, _ := json.Marshal(map[string]any{"thread": s.thread})
	return &pb.NativeQueryResponse{Result: &pb.NativeJson{Utf8Json: raw}}, nil
}
func TestNewCodexEmptyReadsRequireNativeProof(t *testing.T) {
	cases := []struct {
		name      string
		thread    map[string]any
		readError bool
		empty     bool
	}{
		{"confirmed empty", map[string]any{"id": "empty", "turns": []any{}}, false, true},
		{"nonempty", map[string]any{"id": "empty", "turns": []any{map[string]any{"id": "turn"}}}, false, false},
		{"omitted turns", map[string]any{"id": "empty"}, false, false},
		{"null turns", map[string]any{"id": "empty", "turns": nil}, false, false},
		{"foreign thread", map[string]any{"id": "other", "turns": []any{}}, false, false},
		{"native failure", nil, true, false},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			h, st, _, _ := newNativeFixture(t)
			srv := &emptyThreadServer{thread: test.thread, readError: test.readError}
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			server := grpc.NewServer()
			pb.RegisterDaemonControlServer(server, srv)
			pb.RegisterRuntimeQueryServer(server, srv)
			go server.Serve(listener)
			defer server.Stop()
			client, err := daemonclient.Dial(context.Background(), daemonclient.Config{Target: listener.Addr().String(), Credential: "fixture", RequestTimeout: time.Second})
			if err != nil {
				t.Fatal(err)
			}
			defer client.Close()
			h.client = client
			_, err = st.UpsertSession(context.Background(), map[string]any{"agent_id": "codex-1", "id": "empty", "source_session_id": "empty", "workspace": "C:/work", "title": "custom title", "updated_at": "2026-10-16T08:29:22Z", "control_state": "owned", "status": "idle"})
			if err != nil {
				t.Fatal(err)
			}
			for _, endpoint := range []string{"messages", "background-processes"} {
				w := httptest.NewRecorder()
				r := httptest.NewRequest("GET", "/api/v1/sessions/empty/"+endpoint+"?agent_id=codex-1", nil)
				if endpoint == "messages" {
					h.handleListMessages(w, r, "empty")
				} else {
					h.handleBackgroundProcesses(w, r, "empty")
				}
				expected := 503
				if endpoint == "background-processes" {
					expected = 502
				}
				if test.empty {
					expected = 200
				}
				if w.Code != expected {
					t.Fatalf("%s got %d %s want %d", endpoint, w.Code, w.Body.String(), expected)
				}
				if test.empty {
					var body map[string]any
					if json.Unmarshal(w.Body.Bytes(), &body) != nil {
						t.Fatal(w.Body.String())
					}
					items, ok := body["items"].([]any)
					if !ok || len(items) != 0 {
						t.Fatal(body)
					}
				}
			}
			if test.empty {
				queries := srv.queries
				w := httptest.NewRecorder()
				h.handleListMessages(w, httptest.NewRequest("GET", "/api/v1/sessions/empty/messages?agent_id=codex-1&before=items:older", nil), "empty")
				if w.Code != 503 || srv.queries != queries+1 {
					t.Fatalf("pagination inferred empty: %d queries=%d", w.Code, srv.queries-queries)
				}
				// Emptiness is checked each time; it is never cached across input.
				srv.thread = map[string]any{"id": "empty", "turns": []any{map[string]any{"id": "new-turn"}}}
				w = httptest.NewRecorder()
				h.handleBackgroundProcesses(w, httptest.NewRequest("GET", "/api/v1/sessions/empty/background-processes?agent_id=codex-1", nil), "empty")
				if w.Code != 502 {
					t.Fatalf("stale empty proof reused: %d", w.Code)
				}
			}
		})
	}
}
