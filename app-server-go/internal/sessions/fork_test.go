package sessions

import (
	pb "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
	"context"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestForkRouteRejectsAntigravityUnderACP(t *testing.T) {
	parentID := "11111111-1111-4111-8111-111111111111"
	called := false
	srv := &mockDaemonServer{createCb: func(r *pb.CreateSessionRequest) (*pb.CommandReceipt, error) {
		called = true
		return nil, nil
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
	if w.Code != 422 || called {
		t.Fatalf("expected 422 fork rejection for Antigravity ACP: code=%d called=%v body=%s", w.Code, called, w.Body.String())
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

func TestForkRoutePassesCodexParentWorkspace(t *testing.T) {
	called := false
	srv := &mockDaemonServer{createCb: func(r *pb.CreateSessionRequest) (*pb.CommandReceipt, error) {
		called = true
		o := r.GetCodex()
		if o == nil || o.GetParentSessionId() != "parent" || o.GetCwd() != "/project" {
			t.Fatalf("fork lost parent workspace: %v", r)
		}
		return &pb.CommandReceipt{OperationState: pb.OperationState_OPERATION_STATE_COMPLETED, Session: &pb.SessionRef{SessionId: "child", AgentId: r.Target.AgentId}, Status: pb.SessionStatus_SESSION_STATUS_IDLE, Outcome: &pb.CommandReceipt_Completed{Completed: &pb.CommandResult{}}}, nil
	}}
	h, st, _, cleanup := setupTestServer(t, srv)
	defer cleanup()
	ctx := context.Background()
	if _, err := st.UpsertAgent(ctx, map[string]any{"id": "a", "kind": "codex", "name": "test", "status": "ready"}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.UpsertSession(ctx, map[string]any{"id": "parent", "agent_id": "a", "title": "old", "workspace": "/project", "status": "idle", "control_state": "owned", "history_state": "available", "updated_at": "2026-10-15T00:00:00Z"}); err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("POST", "/api/v1/sessions/parent/fork", strings.NewReader(`{"agent_id":"a","title":"child"}`))
	r.Header.Set("Authorization", "Bearer browser-sec")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 200 || !called {
		t.Fatalf("fork: %d %s", w.Code, w.Body.String())
	}
	parent, err := st.GetSession(ctx, "a", "parent")
	if err != nil || parent["workspace"] != "/project" || parent["title"] != "old" {
		t.Fatalf("source session changed: %v %v", parent, err)
	}
	child, err := st.GetSession(ctx, "a", "child")
	if err != nil || child["workspace"] != "/project" || child["parent_session_id"] != "parent" {
		t.Fatalf("child lineage or workspace missing: %v %v", child, err)
	}
}
