package sessions

import (
	pb "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAntigravityModelSwitchUsesNativeCapability(t *testing.T) {
	reasoningSupported := false
	h, st, _, cleanup := setupTestServer(t, &mockDaemonServer{readModelCb: func(*pb.ReadModelRequest) (*pb.ModelState, error) {
		if reasoningSupported {
			return &pb.ModelState{
				Effort: optString("medium"),
				Native: &pb.NativeJson{Utf8Json: []byte(`{"model_switch_supported":true,"reasoning_switch_supported":true,"supported_efforts":["low","medium","high"]}`)},
			}, nil
		}
		return &pb.ModelState{Native: &pb.NativeJson{Utf8Json: []byte(`{"model_switch_supported":true}`)}}, nil
	}})
	defer cleanup()
	ctx := context.Background()
	if _, err := st.UpsertAgent(ctx, map[string]any{"id": "local-antigravity", "kind": "antigravity", "name": "Antigravity", "status": "ready"}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.UpsertSession(ctx, map[string]any{"id": "native", "agent_id": "local-antigravity", "status": "idle", "control_state": "owned", "updated_at": "2026-10-17T00:00:00Z"}); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/sessions/native/model?agent_id=local-antigravity", nil)
	req.Header.Set("Authorization", "Bearer browser-sec")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	var binding map[string]any
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &binding) != nil {
		t.Fatalf("model: %d %s", rec.Code, rec.Body.String())
	}
	if binding["model_read_only"] == true {
		t.Fatal("native-confirmed switching is disabled")
	}
	if binding["reasoning_read_only"] != true {
		t.Fatal("unsupported reasoning must remain disabled")
	}

	reasoningSupported = true
	rec2 := httptest.NewRecorder()
	h.ServeHTTP(rec2, req)
	var binding2 map[string]any
	if rec2.Code != 200 || json.Unmarshal(rec2.Body.Bytes(), &binding2) != nil {
		t.Fatalf("model acp reasoning: %d %s", rec2.Code, rec2.Body.String())
	}
	if binding2["reasoning_read_only"] != false || binding2["effort"] != "medium" {
		t.Fatalf("ACP thought_level support was not exposed: %+v", binding2)
	}
}

func TestAntigravityModelSwitchSyncsEffortAndReasoningSyncsModel(t *testing.T) {
	curModel := "gemini-4-neon-xhigh"
	curEffort := "xhigh"
	srv := &mockDaemonServer{
		readModelCb: func(*pb.ReadModelRequest) (*pb.ModelState, error) {
			return &pb.ModelState{
				Provider: optString("antigravity"),
				Model:    optString(curModel),
				Effort:   optString(curEffort),
				Native:   &pb.NativeJson{Utf8Json: []byte(`{"model_switch_supported":true,"reasoning_switch_supported":true,"supported_efforts":["low","medium","high","xhigh"]}`)},
			}, nil
		},
		setModelCb: func(req *pb.SetModelRequest) (*pb.CommandReceipt, error) {
			curModel = req.Model
			return &pb.CommandReceipt{
				OperationState: pb.OperationState_OPERATION_STATE_COMPLETED,
				Outcome:        &pb.CommandReceipt_Completed{Completed: &pb.CommandResult{}},
			}, nil
		},
		setReasoningCb: func(req *pb.SetReasoningRequest) (*pb.CommandReceipt, error) {
			curEffort = req.Effort
			return &pb.CommandReceipt{
				OperationState: pb.OperationState_OPERATION_STATE_COMPLETED,
				Outcome:        &pb.CommandReceipt_Completed{Completed: &pb.CommandResult{}},
			}, nil
		},
	}
	h, st, _, cleanup := setupTestServer(t, srv)
	defer cleanup()
	ctx := context.Background()
	if _, err := st.UpsertAgent(ctx, map[string]any{"id": "local-antigravity", "kind": "antigravity", "name": "Antigravity", "status": "ready"}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.UpsertSession(ctx, map[string]any{"id": "native", "agent_id": "local-antigravity", "status": "idle", "control_state": "owned", "updated_at": "2026-10-17T00:00:00Z"}); err != nil {
		t.Fatal(err)
	}

	// Switch to 3-level Carbon model at high: persists both model and synced effort.
	curModel = "gemini-4-carbon-high"
	curEffort = ""
	body, _ := json.Marshal(map[string]any{"agent_id": "local-antigravity", "provider": "antigravity", "model": "gemini-4-carbon-high"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/sessions/native/model", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer browser-sec")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	var resp map[string]any
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &resp) != nil {
		t.Fatalf("set model failed: %d %s", rec.Code, rec.Body.String())
	}
	if resp["model"] != "gemini-4-carbon-high" || resp["effort"] != "high" {
		t.Fatalf("expected synced effort in set model response, got %+v", resp)
	}
	saved, err := st.GetSessionModelBinding(ctx, "local-antigravity", "native")
	if err != nil || saved == nil || saved["model"] != "gemini-4-carbon-high" || saved["effort"] != "high" {
		t.Fatalf("expected store binding to sync effort=high, got %+v err=%v", saved, err)
	}

	// Switch to non-effort Orchestrator model: clears stale effort.
	curModel = "gemini-pro-orchestrator"
	curEffort = ""
	body2, _ := json.Marshal(map[string]any{"agent_id": "local-antigravity", "provider": "antigravity", "model": "gemini-pro-orchestrator"})
	req2 := httptest.NewRequest(http.MethodPost, "/api/v1/sessions/native/model", bytes.NewReader(body2))
	req2.Header.Set("Authorization", "Bearer browser-sec")
	rec2 := httptest.NewRecorder()
	h.ServeHTTP(rec2, req2)
	if rec2.Code != 200 {
		t.Fatalf("set orchestrator failed: %d %s", rec2.Code, rec2.Body.String())
	}
	saved2, err := st.GetSessionModelBinding(ctx, "local-antigravity", "native")
	if err != nil || saved2 == nil || saved2["model"] != "gemini-pro-orchestrator" {
		t.Fatalf("unexpected orchestrator binding: %+v err=%v", saved2, err)
	}
	if eff, ok := saved2["effort"]; ok && eff != "" {
		t.Fatalf("expected cleared effort for orchestrator, got %+v", saved2)
	}

	// Switch reasoning effort via /reasoning: updates stored model ID too.
	curModel = "gemini-4-neon-medium"
	curEffort = "medium"
	rBody, _ := json.Marshal(map[string]any{"agent_id": "local-antigravity", "effort": "medium"})
	rReq := httptest.NewRequest(http.MethodPost, "/api/v1/sessions/native/reasoning", bytes.NewReader(rBody))
	rReq.Header.Set("Authorization", "Bearer browser-sec")
	rRec := httptest.NewRecorder()
	h.ServeHTTP(rRec, rReq)
	var rResp map[string]any
	if rRec.Code != 200 || json.Unmarshal(rRec.Body.Bytes(), &rResp) != nil {
		t.Fatalf("set reasoning failed: %d %s", rRec.Code, rRec.Body.String())
	}
	if rResp["effort"] != "medium" || rResp["model"] != "gemini-4-neon-medium" {
		t.Fatalf("expected updated model in reasoning response, got %+v", rResp)
	}
	saved3, err := st.GetSessionModelBinding(ctx, "local-antigravity", "native")
	if err != nil || saved3 == nil || saved3["model"] != "gemini-4-neon-medium" || saved3["effort"] != "medium" {
		t.Fatalf("expected store binding to sync model=gemini-4-neon-medium, got %+v err=%v", saved3, err)
	}
}
