package sessions

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	pb "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type swarmQueryMockRuntime struct {
	pb.UnimplementedRuntimeQueryServer
	lastReq *pb.NativeQueryRequest
	resp    map[string]any
	err     error
}

func (m *swarmQueryMockRuntime) QueryNative(_ context.Context, req *pb.NativeQueryRequest) (*pb.NativeQueryResponse, error) {
	m.lastReq = req
	if m.err != nil {
		return nil, m.err
	}
	b, _ := json.Marshal(m.resp)
	return &pb.NativeQueryResponse{
		Result: &pb.NativeJson{
			Schema:   "astrorder.session.swarm.v1",
			Utf8Json: b,
		},
	}, nil
}

func TestHandleGetSessionSwarmAntigravityAndNonAntigravity(t *testing.T) {
	coordID := "11111111-1111-4111-8111-111111111111"
	workerID := "22222222-2222-4222-8222-222222222222"
	rt := &swarmQueryMockRuntime{
		resp: map[string]any{
			"supported":              true,
			"available":              true,
			"active":                 true,
			"kind":                   "swarm",
			"title":                  "Swarm Demo",
			"coordinator_session_id": coordID,
			"current_session_id":     coordID,
			"is_coordinator":         true,
			"summary":                map[string]any{"total": 2, "working": 1, "waiting": 1, "stopped": 0, "killed": 0},
			"members": []any{
				map[string]any{"name": "coordinator", "is_coordinator": true, "conversation_id": coordID, "state": "waiting"},
				map[string]any{"name": "worker-1", "is_coordinator": false, "conversation_id": workerID, "state": "working"},
			},
			"board":      []any{},
			"updated_at": "2026-11-22T12:00:00Z",
		},
	}

	h, st, _, cleanup := setupTestServerWithCustomRuntime(t, &mockDaemonServer{}, rt)
	defer cleanup()
	ctx := context.Background()

	_, _ = st.UpsertAgent(ctx, map[string]any{"id": "local-antigravity", "kind": "antigravity", "name": "Antigravity", "status": "ready"})
	_, _ = st.UpsertAgent(ctx, map[string]any{"id": "local-codex", "kind": "codex", "name": "Codex", "status": "ready"})
	_, _ = st.UpsertSession(ctx, map[string]any{"id": coordID, "agent_id": "local-antigravity", "status": "idle", "control_state": "owned", "updated_at": "2026-11-22T12:00:00Z"})
	_, _ = st.UpsertSession(ctx, map[string]any{"id": "codex-1", "agent_id": "local-codex", "status": "idle", "control_state": "owned", "updated_at": "2026-11-22T12:00:00Z"})

	// 1. Antigravity session query forwards member_conversation_id & member_limit and enriches agent_id/in_catalog.
	req := httptest.NewRequest(http.MethodGet, "/api/v1/sessions/"+coordID+"/swarm?agent_id=local-antigravity&member_conversation_id="+workerID+"&member_limit=15", nil)
	req.Header.Set("Authorization", "Bearer browser-sec")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if rt.lastReq == nil || rt.lastReq.GetAntigravitySessionSwarm() == nil {
		t.Fatalf("expected AntigravitySessionSwarm gRPC query, got %+v", rt.lastReq)
	}
	q := rt.lastReq.GetAntigravitySessionSwarm()
	if q.GetConversationId() != coordID || q.GetMemberConversationId() != workerID || q.GetMemberLimit() != 15 {
		t.Fatalf("unexpected gRPC query params: %+v", q)
	}
	var payload map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	members := payload["members"].([]any)
	m0 := members[0].(map[string]any)
	m1 := members[1].(map[string]any)
	if m0["in_catalog"] != true || m0["agent_id"] != "local-antigravity" {
		t.Fatalf("expected coordinator in_catalog=true, got %+v", m0)
	}
	if m1["in_catalog"] != false || m1["agent_id"] != "local-antigravity" {
		t.Fatalf("expected worker in_catalog=false, got %+v", m1)
	}

	// 2. Non-Antigravity session returns supported=false cleanly without hitting gRPC.
	rt.lastReq = nil
	codexReq := httptest.NewRequest(http.MethodGet, "/api/v1/sessions/codex-1/swarm?agent_id=local-codex", nil)
	codexReq.Header.Set("Authorization", "Bearer browser-sec")
	codexRec := httptest.NewRecorder()
	h.ServeHTTP(codexRec, codexReq)
	if codexRec.Code != http.StatusOK {
		t.Fatalf("expected 200 for codex, got %d: %s", codexRec.Code, codexRec.Body.String())
	}
	if rt.lastReq != nil {
		t.Fatal("non-antigravity session must not invoke QueryNative")
	}
	var codexPayload map[string]any
	_ = json.Unmarshal(codexRec.Body.Bytes(), &codexPayload)
	if codexPayload["supported"] != false || codexPayload["available"] != false {
		t.Fatalf("unexpected codex response: %+v", codexPayload)
	}

	// 3. Pre-upgrade small kernel degrades gracefully with needs_daemon_upgrade=true.
	rt.err = status.Error(codes.InvalidArgument, "native query request is invalid")
	upReq := httptest.NewRequest(http.MethodGet, "/api/v1/sessions/"+coordID+"/swarm?agent_id=local-antigravity", nil)
	upReq.Header.Set("Authorization", "Bearer browser-sec")
	upRec := httptest.NewRecorder()
	h.ServeHTTP(upRec, upReq)
	if upRec.Code != http.StatusOK {
		t.Fatalf("expected graceful 200 on pre-upgrade daemon, got %d: %s", upRec.Code, upRec.Body.String())
	}
	var upPayload map[string]any
	_ = json.Unmarshal(upRec.Body.Bytes(), &upPayload)
	if upPayload["needs_daemon_upgrade"] != true || upPayload["supported"] != true {
		t.Fatalf("expected needs_daemon_upgrade=true, got %+v", upPayload)
	}

	// 4. Existing non-ephemeral Swarm worker rows in catalog are backfilled to ephemeral=true and titled @name.
	rt.err = nil
	_, _ = st.UpsertSession(ctx, map[string]any{"id": workerID, "agent_id": "local-antigravity", "title": workerID, "status": "idle", "control_state": "unbound", "ephemeral": false, "updated_at": "2026-11-22T12:00:00Z"})
	backfillReq := httptest.NewRequest(http.MethodGet, "/api/v1/sessions/"+coordID+"/swarm?agent_id=local-antigravity", nil)
	backfillReq.Header.Set("Authorization", "Bearer browser-sec")
	backfillRec := httptest.NewRecorder()
	h.ServeHTTP(backfillRec, backfillReq)
	if backfillRec.Code != http.StatusOK {
		t.Fatalf("expected 200 on backfill, got %d: %s", backfillRec.Code, backfillRec.Body.String())
	}
	workerRow, err := st.GetSession(ctx, "local-antigravity", workerID)
	if err != nil || workerRow == nil {
		t.Fatalf("GetSession(worker) failed: %v", err)
	}
	if workerRow["ephemeral"] != true || workerRow["title"] != "@worker-1" {
		t.Fatalf("expected worker backfilled to ephemeral=true and @worker-1, got %+v", workerRow)
	}
	coordRow, _ := st.GetSession(ctx, "local-antigravity", coordID)
	if coordRow["ephemeral"] == true {
		t.Fatalf("coordinator must remain non-ephemeral, got %+v", coordRow)
	}
}
