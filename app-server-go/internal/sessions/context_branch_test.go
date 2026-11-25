package sessions

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	pb "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
)

type contextBranchMockRuntime struct {
	pb.UnimplementedRuntimeQueryServer
	pages       []*pb.HistoryPageResponse
	modelState  *pb.ModelState
	historyReqs int
}

func (m *contextBranchMockRuntime) GetHistoryPage(_ context.Context, req *pb.HistoryPageRequest) (*pb.HistoryPageResponse, error) {
	idx := m.historyReqs
	m.historyReqs++
	if idx < len(m.pages) {
		return m.pages[idx], nil
	}
	return &pb.HistoryPageResponse{Session: req.GetSession()}, nil
}

func (m *contextBranchMockRuntime) ReadModel(_ context.Context, _ *pb.ReadModelRequest) (*pb.ModelState, error) {
	if m.modelState != nil {
		return m.modelState, nil
	}
	return &pb.ModelState{}, nil
}

func TestContextBranchTruncatesAtTargetMessageForAntigravity(t *testing.T) {
	parentID := "11111111-1111-4111-8111-111111111111"
	childID := "22222222-2222-4222-8222-222222222222"
	agentID := "local-antigravity"

	var createdReq *pb.CreateSessionRequest
	srv := &mockDaemonServer{
		createCb: func(r *pb.CreateSessionRequest) (*pb.CommandReceipt, error) {
			createdReq = r
			return &pb.CommandReceipt{
				OperationState: pb.OperationState_OPERATION_STATE_COMPLETED,
				Session:        &pb.SessionRef{SessionId: childID, AgentId: r.Target.AgentId, ConnectionId: r.Target.ConnectionId},
				Status:         pb.SessionStatus_SESSION_STATUS_IDLE,
				Outcome:        &pb.CommandReceipt_Completed{Completed: &pb.CommandResult{}},
			}, nil
		},
	}

	rt := &contextBranchMockRuntime{
		modelState: &pb.ModelState{
			Provider: optString("antigravity"),
			Model:    optString("gemini-4-neon-xhigh"),
		},
		pages: []*pb.HistoryPageResponse{
			{
				Session: &pb.SessionRef{SessionId: parentID, AgentId: optString(agentID)},
				Items: []*pb.NativeJson{
					{Schema: "antigravity.message.v1", Utf8Json: []byte(`{"id":"antigravity:` + parentID + `:1","role":"user","kind":"message","text":"第一个需求"}`)},
					{Schema: "antigravity.message.v1", Utf8Json: []byte(`{"id":"antigravity:` + parentID + `:2","role":"assistant","kind":"message","text":"第一个回复方案"}`)},
					{Schema: "antigravity.message.v1", Utf8Json: []byte(`{"id":"antigravity:` + parentID + `:3","role":"user","kind":"message","text":"后续跑偏的追问"}`)},
					{Schema: "antigravity.message.v1", Utf8Json: []byte(`{"id":"antigravity:` + parentID + `:4","role":"assistant","kind":"message","text":"后续跑偏的回答"}`)},
				},
			},
		},
	}

	h, st, _, cleanup := setupTestServerWithCustomRuntime(t, srv, rt)
	defer cleanup()
	ctx := context.Background()

	_, _ = st.UpsertAgent(ctx, map[string]any{"id": agentID, "kind": "antigravity", "name": "Antigravity", "status": "ready"})
	_, _ = st.UpsertSession(ctx, map[string]any{
		"id":            parentID,
		"agent_id":      agentID,
		"title":         "原始任务",
		"workspace":     "/workspace/demo",
		"status":        "idle",
		"control_state": "owned",
		"history_state": "available",
		"updated_at":    "2026-11-25T01:00:00Z",
	})

	req := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/sessions/"+parentID+"/context-branch",
		strings.NewReader(`{"agent_id":"`+agentID+`","title":"从方案一继续","target_message_id":"antigravity:`+parentID+`:2"}`),
	)
	req.Header.Set("Authorization", "Bearer browser-sec")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if createdReq == nil || createdReq.GetAntigravity() == nil {
		t.Fatalf("expected Antigravity CreateSessionRequest, got %+v", createdReq)
	}
	if createdReq.GetAntigravity().GetParentSessionId() != "" {
		t.Fatalf("context branch must create clean session without native parent fork, got %q", createdReq.GetAntigravity().GetParentSessionId())
	}
	if createdReq.GetAntigravity().GetCwd() != "/workspace/demo" || createdReq.GetAntigravity().GetModel() != "gemini-4-neon-xhigh" {
		t.Fatalf("unexpected create options: %+v", createdReq.GetAntigravity())
	}

	var child map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &child); err != nil {
		t.Fatal(err)
	}
	if child["id"] != childID || child["title"] != "从方案一继续" {
		t.Fatalf("unexpected child response: %+v", child)
	}
	handoffCtx := stringValue(child, "handoff_context")
	if !strings.Contains(handoffCtx, "第一个需求") || !strings.Contains(handoffCtx, "第一个回复方案") {
		t.Fatalf("expected history up to target_message_id, got: %s", handoffCtx)
	}
	if strings.Contains(handoffCtx, "后续跑偏的追问") || strings.Contains(handoffCtx, "后续跑偏的回答") {
		t.Fatalf("messages after target_message_id must be excluded, got: %s", handoffCtx)
	}

	// Verify /handoff-context exposes the excerpt cleanly for HandoffSummaryCard.
	ctxReq := httptest.NewRequest(http.MethodGet, "/api/v1/sessions/"+childID+"/handoff-context?agent_id="+agentID, nil)
	ctxReq.Header.Set("Authorization", "Bearer browser-sec")
	ctxRec := httptest.NewRecorder()
	h.ServeHTTP(ctxRec, ctxReq)
	if ctxRec.Code != http.StatusOK {
		t.Fatalf("expected 200 on handoff-context, got %d: %s", ctxRec.Code, ctxRec.Body.String())
	}
	var ctxResp map[string]any
	_ = json.Unmarshal(ctxRec.Body.Bytes(), &ctxResp)
	if ctxResp["available"] != true || ctxResp["mode"] != "transcript_excerpt" || !strings.Contains(stringValue(ctxResp, "summary"), "第一个回复方案") {
		t.Fatalf("unexpected handoff-context payload: %+v", ctxResp)
	}

	// Rejects unknown target_message_id with 422 without creating a session.
	createdReq = nil
	rt.historyReqs = 0
	badReq := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/sessions/"+parentID+"/context-branch",
		strings.NewReader(`{"agent_id":"`+agentID+`","target_message_id":"antigravity:`+parentID+`:999"}`),
	)
	badReq.Header.Set("Authorization", "Bearer browser-sec")
	badRec := httptest.NewRecorder()
	h.ServeHTTP(badRec, badReq)
	if badRec.Code != http.StatusUnprocessableEntity || createdReq != nil {
		t.Fatalf("expected 422 without session creation, got %d created=%v body=%s", badRec.Code, createdReq != nil, badRec.Body.String())
	}
}
