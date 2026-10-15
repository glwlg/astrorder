package sessions

import (
	pb "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
	"context"
	"encoding/json"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"net/http/httptest"
	"testing"
)

func (m *mockDaemonServer) ListBackgroundProcesses(ctx context.Context, req *pb.ListBackgroundProcessesRequest) (*pb.ListBackgroundProcessesResponse, error) {
	return m.backgroundCb(req)
}
func TestBackgroundProcessesExcludeFinishedAndUnboundTools(t *testing.T) {
	srv := &mockDaemonServer{}
	h, st, _, cleanup := setupTestServer(t, srv)
	defer cleanup()
	ctx := context.Background()
	_, err := st.UpsertAgent(ctx, map[string]any{"id": "agent-p", "name": "process test", "kind": "hermes", "status": "ready"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = st.UpsertSession(ctx, map[string]any{"id": "session-p", "source_session_id": "native-p", "agent_id": "agent-p", "status": "idle", "created_at": "2026-10-14T00:00:00Z", "updated_at": "2026-10-14T00:00:00Z"})
	if err != nil {
		t.Fatal(err)
	}
	srv.backgroundCb = func(req *pb.ListBackgroundProcessesRequest) (*pb.ListBackgroundProcessesResponse, error) {
		if req.Session.SessionId != "native-p" || req.Session.GetAgentId() != "agent-p" {
			t.Fatal(req)
		}
		rows := []map[string]any{{"session_id": "service", "status": "running", "command": "serve", "output_tail": "ready"}, {"session_id": "finished", "status": "completed"}, {"status": "running", "command": "read_file"}}
		response := &pb.ListBackgroundProcessesResponse{}
		for _, row := range rows {
			data, _ := json.Marshal(row)
			response.Processes = append(response.Processes, &pb.NativeJson{Utf8Json: data})
		}
		return response, nil
	}
	read := func() *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		h.handleBackgroundProcesses(w, httptest.NewRequest("GET", "/api/v1/sessions/session-p/background-processes?agent_id=agent-p", nil), "session-p")
		return w
	}
	w := read()
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var body struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Items) != 1 || body.Items[0]["target_id"] != "service" || body.Items[0]["progress"].(map[string]any)["output"] != "ready" {
		t.Fatal(body)
	}
	srv.backgroundCb = func(*pb.ListBackgroundProcessesRequest) (*pb.ListBackgroundProcessesResponse, error) {
		return nil, status.Error(codes.NotFound, "not attached")
	}
	w = read()
	if w.Code != 200 || w.Body.String() != "{\"items\":[]}\n" {
		t.Fatal(w.Code, w.Body.String())
	}
	srv.backgroundCb = func(*pb.ListBackgroundProcessesRequest) (*pb.ListBackgroundProcessesResponse, error) {
		return nil, status.Error(codes.Unavailable, "offline")
	}
	if w = read(); w.Code != 502 {
		t.Fatal(w.Code, w.Body.String())
	}
}
