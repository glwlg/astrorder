package sessions

import (
	pb "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
	"context"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestForkRouteUsesNativeAntigravityBoundary(t *testing.T) {
	parentID := "11111111-1111-4111-8111-111111111111"
	called := false
	srv := &mockDaemonServer{createCb: func(r *pb.CreateSessionRequest) (*pb.CommandReceipt, error) {
		called = true
		o := r.GetAntigravity()
		if o == nil || o.GetParentSessionId() != parentID || o.GetTargetMessageId() != "antigravity:"+parentID+":1" {
			t.Fatalf("wrong native boundary: %v", r)
		}
		return &pb.CommandReceipt{OperationState: pb.OperationState_OPERATION_STATE_COMPLETED, Session: &pb.SessionRef{SessionId: "22222222-2222-4222-8222-222222222222", AgentId: r.Target.AgentId}, Status: pb.SessionStatus_SESSION_STATUS_IDLE, Outcome: &pb.CommandReceipt_Completed{Completed: &pb.CommandResult{}}}, nil
	}}
	h, st, _, cleanup := setupTestServer(t, srv)
	defer cleanup()
	ctx := context.Background()
	st.UpsertAgent(ctx, map[string]any{"id": "a", "kind": "antigravity", "name": "test", "status": "ready"})
	st.UpsertSession(ctx, map[string]any{"id": parentID, "agent_id": "a", "title": "old", "workspace": "C:/work", "status": "idle", "control_state": "owned", "history_state": "available", "updated_at": "2026-10-18T00:00:00Z"})
	r := httptest.NewRequest("POST", "/api/v1/sessions/"+parentID+"/fork", strings.NewReader(`{"agent_id":"a","target_message_id":"antigravity:`+parentID+`:1"}`))
	r.Header.Set("Authorization", "Bearer browser-sec")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 200 || !called {
		t.Fatalf("fork: %d %s", w.Code, w.Body.String())
	}
}

func TestForkRouteUsesNativeHermesBoundary(t *testing.T) {
	called := false
	srv := &mockDaemonServer{createCb: func(r *pb.CreateSessionRequest) (*pb.CommandReceipt, error) {
		called = true
		o := r.GetHermes()
		if o == nil || o.GetParentSessionId() != "parent" || !strings.Contains(string(o.NativeOptions.Utf8Json), `"target_message_id":9007199254740993`) {
			t.Fatalf("wrong native boundary: %v", r)
		}
		return &pb.CommandReceipt{OperationState: pb.OperationState_OPERATION_STATE_COMPLETED, Session: &pb.SessionRef{SessionId: "child", AgentId: r.Target.AgentId}, Status: pb.SessionStatus_SESSION_STATUS_IDLE, Outcome: &pb.CommandReceipt_Completed{Completed: &pb.CommandResult{}}}, nil
	}}
	h, st, _, cleanup := setupTestServer(t, srv)
	defer cleanup()
	ctx := context.Background()
	st.UpsertAgent(ctx, map[string]any{"id": "a", "kind": "hermes", "name": "test", "status": "ready"})
	st.UpsertSession(ctx, map[string]any{"id": "parent", "agent_id": "a", "title": "old", "workspace": "C:/work", "status": "idle", "control_state": "owned", "history_state": "available", "updated_at": "2026-10-15T00:00:00Z"})
	r := httptest.NewRequest("POST", "/api/v1/sessions/parent/fork", strings.NewReader(`{"agent_id":"a","title":"child","target_message_id":"native:parent:9007199254740993","create_worktree":false}`))
	r.Header.Set("Authorization", "Bearer browser-sec")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 200 || !called {
		t.Fatalf("fork: %d %s", w.Code, w.Body.String())
	}
	for _, body := range []string{
		`{"agent_id":"a","target_message_id":"native:foreign:42"}`,
		`{"agent_id":"a","target_message_id":"native:parent:42suffix"}`,
		`{"agent_id":"a","create_worktree":true}`,
	} {
		called = false
		r = httptest.NewRequest("POST", "/api/v1/sessions/parent/fork", strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer browser-sec")
		w = httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 422 || called {
			t.Fatalf("invalid fork dispatched: %d %s", w.Code, w.Body.String())
		}
	}
	row, _ := st.GetSession(ctx, "a", "child")
	if row == nil || row["parent_session_id"] != "parent" {
		t.Fatalf("child missing: %v", row)
	}
}
