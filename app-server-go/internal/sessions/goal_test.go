package sessions

import (
	"astrorder.dev/app-server/internal/daemonclient"
	"astrorder.dev/app-server/internal/store"
	pb "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
	"bytes"
	"context"
	"encoding/json"
	"google.golang.org/grpc"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type goalServer struct {
	mockDaemonServer
	called   int
	omitGoal bool
}

func (s *goalServer) goalReceipt(ref *pb.SessionRef, goal any) (*pb.CommandReceipt, error) {
	s.called++
	data := map[string]any{"goal": goal}
	if s.omitGoal {
		data = map[string]any{}
	}
	raw, _ := json.Marshal(data)
	return &pb.CommandReceipt{Session: ref, OperationState: pb.OperationState_OPERATION_STATE_COMPLETED, Outcome: &pb.CommandReceipt_Completed{Completed: &pb.CommandResult{Accepted: true, Native: &pb.NativeJson{Utf8Json: raw}}}}, nil
}
func (s *goalServer) SetGoal(_ context.Context, r *pb.SetGoalRequest) (*pb.CommandReceipt, error) {
	if r.Session.SessionId != "native" || r.Session.GetAgentId() != "a" || r.Session.GetConnectionId() != "remote" {
		return nil, context.Canceled
	}
	state := "complete"
	if r.Status != "" { state = r.Status }
	return s.goalReceipt(r.Session, map[string]any{"objective": r.Objective, "status": state})
}
func (s *goalServer) ClearGoal(_ context.Context, r *pb.ClearGoalRequest) (*pb.CommandReceipt, error) {
	return s.goalReceipt(r.Session, nil)
}
func TestGoalHTTPUsesNativeIdentityAndRequiresReadback(t *testing.T) {
	srv := &goalServer{}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := grpc.NewServer()
	pb.RegisterDaemonControlServer(server, srv)
	pb.RegisterRuntimeQueryServer(server, srv)
	go server.Serve(listener)
	defer server.Stop()
	dc, err := daemonclient.Dial(context.Background(), daemonclient.Config{Target: listener.Addr().String(), Credential: "test-secret", RequestTimeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer dc.Close()
	st, err := store.Open(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	_, _ = st.UpsertAgent(context.Background(), map[string]any{"id": "a", "kind": "codex", "name": "test", "status": "ready"})
	_, _ = st.UpsertSession(context.Background(), map[string]any{"id": "projected", "source_session_id": "native", "agent_id": "a", "connection_id": "remote", "status": "idle", "updated_at": time.Now().UTC().Format(time.RFC3339)})
	h := &Handler{store: st, client: dc}
	call := func(method, body string) *httptest.ResponseRecorder {
		path := "/api/v1/sessions/projected/goal?agent_id=a"
		name, params := parseRoute(method, "/api/v1/sessions/projected/goal")
		if name != "manage_session_goal" || params.SessionID != "projected" {
			t.Fatal("route", name, params)
		}
		w := httptest.NewRecorder()
		h.handleSessionGoal(w, httptest.NewRequest(method, path, bytes.NewBufferString(body)), "projected")
		return w
	}
	if w := call(http.MethodPut, `{"objective":"new"}`); w.Code != 200 || !strings.Contains(w.Body.String(), "new") {
		t.Fatal(w.Code, w.Body.String())
	}
	for _, state := range []string{"paused", "active"} {
		if w := call(http.MethodPut, `{"status":"`+state+`"}`); w.Code != 200 || !strings.Contains(w.Body.String(), state) {
			t.Fatal("lifecycle", w.Code, w.Body.String())
		}
	}
	if w := call(http.MethodDelete, ""); w.Code != 200 || !strings.Contains(w.Body.String(), `"goal":null`) {
		t.Fatal(w.Code, w.Body.String())
	}
	srv.omitGoal = true
	if w := call(http.MethodDelete, ""); w.Code != 502 {
		t.Fatal("missing readback accepted", w.Code)
	}
	before := srv.called
	if w := call(http.MethodPut, `{"objective":" "}`); w.Code != 422 || srv.called != before {
		t.Fatal("invalid mutation", w.Code)
	}
}
