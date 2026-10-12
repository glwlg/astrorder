package sessions

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"astrorder.dev/app-server/internal/application"
	"astrorder.dev/app-server/internal/auth"
	"astrorder.dev/app-server/internal/config"
	"astrorder.dev/app-server/internal/daemonclient"
	"astrorder.dev/app-server/internal/store"
	sessiondv1 "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
	"google.golang.org/grpc"
)

type fakeRuntimeQueryServer = fakeCombinedDaemonServer

type fakeCombinedDaemonServer struct {
	sessiondv1.UnimplementedDaemonControlServer
	sessiondv1.UnimplementedRuntimeQueryServer

	currentProvider string
	currentModel    string
	currentEffort   string
	currentMode     string

	rejectNextSetModel     bool
	staleReadbackSetModel  bool
	rejectNextSetReasoning bool
	rejectNextSetApproval  bool
}

func (s *fakeCombinedDaemonServer) GetCapabilities(ctx context.Context, req *sessiondv1.GetCapabilitiesRequest) (*sessiondv1.GetCapabilitiesResponse, error) {
	return &sessiondv1.GetCapabilitiesResponse{
		ProtocolMajor: 1,
		ProtocolMinor: 0,
		DaemonId:      "daemon-test-123",
		Services: []string{
			"astrorder.sessiond.v1.ConnectorIngress",
			"astrorder.sessiond.v1.DaemonControl",
			"astrorder.sessiond.v1.EventStream",
			"astrorder.sessiond.v1.ModelConfig",
			"astrorder.sessiond.v1.OperationQuery",
			"astrorder.sessiond.v1.RuntimeQuery",
			"astrorder.sessiond.v1.SessionControl",
			"astrorder.sessiond.v1.TerminalStream",
		},
		CanonicalizationVersions: []sessiondv1.CanonicalizationVersion{
			sessiondv1.CanonicalizationVersion_CANONICALIZATION_VERSION_LEGACY_JSON_V1,
		},
		MaxWireBytes:       sessiondv1.ContractLimit_CONTRACT_LIMIT_MAX_GRPC_WIRE_BYTES,
		MaxBusinessBytes:   sessiondv1.ContractLimit_CONTRACT_LIMIT_MAX_COMPAT_BUSINESS_BYTES,
		EventStreamVersion: sessiondv1.EventStreamVersion_EVENT_STREAM_VERSION_V1,
		OperationLookup:    true,
	}, nil
}

func (s *fakeCombinedDaemonServer) ListModels(ctx context.Context, req *sessiondv1.ListModelsRequest) (*sessiondv1.ListModelsResponse, error) {
	lbl := "Hermes Default"
	return &sessiondv1.ListModelsResponse{
		Items: []*sessiondv1.ModelOption{
			{
				Provider:         "hermes",
				Model:            "chat",
				Label:            &lbl,
				SupportedEfforts: []string{"low", "high"},
			},
		},
	}, nil
}

func (s *fakeCombinedDaemonServer) ListCommands(ctx context.Context, req *sessiondv1.ListCommandsRequest) (*sessiondv1.ListCommandsResponse, error) {
	desc := "Run bash"
	return &sessiondv1.ListCommandsResponse{
		Items: []*sessiondv1.CommandOption{
			{
				Name:        "bash",
				Description: &desc,
			},
		},
	}, nil
}

func (s *fakeCombinedDaemonServer) ReadModel(ctx context.Context, req *sessiondv1.ReadModelRequest) (*sessiondv1.ModelState, error) {
	p := s.currentProvider
	m := s.currentModel
	e := s.currentEffort
	return &sessiondv1.ModelState{
		Provider: &p,
		Model:    &m,
		Effort:   &e,
	}, nil
}

func (s *fakeCombinedDaemonServer) SetModel(ctx context.Context, req *sessiondv1.SetModelRequest) (*sessiondv1.CommandReceipt, error) {
	if req.Meta == nil || req.Meta.OperationId == "" || req.Meta.Canonicalization != sessiondv1.CanonicalizationVersion_CANONICALIZATION_VERSION_LEGACY_JSON_V1 {
		return &sessiondv1.CommandReceipt{
			OperationState: sessiondv1.OperationState_OPERATION_STATE_UNCERTAIN,
		}, nil
	}
	if s.rejectNextSetModel {
		return &sessiondv1.CommandReceipt{
			OperationState: sessiondv1.OperationState_OPERATION_STATE_COMPLETED,
			Outcome: &sessiondv1.CommandReceipt_Rejected{
				Rejected: &sessiondv1.BusinessError{Message: "Model rejected by policy"},
			},
		}, nil
	}
	if !s.staleReadbackSetModel {
		s.currentProvider = req.Provider
		s.currentModel = req.Model
	}
	return &sessiondv1.CommandReceipt{
		OperationState: sessiondv1.OperationState_OPERATION_STATE_COMPLETED,
		Outcome: &sessiondv1.CommandReceipt_Completed{
			Completed: &sessiondv1.CommandResult{},
		},
	}, nil
}

func (s *fakeCombinedDaemonServer) SetReasoning(ctx context.Context, req *sessiondv1.SetReasoningRequest) (*sessiondv1.CommandReceipt, error) {
	if req.Meta == nil || req.Meta.OperationId == "" || req.Meta.Canonicalization != sessiondv1.CanonicalizationVersion_CANONICALIZATION_VERSION_LEGACY_JSON_V1 {
		return &sessiondv1.CommandReceipt{
			OperationState: sessiondv1.OperationState_OPERATION_STATE_UNCERTAIN,
		}, nil
	}
	if s.rejectNextSetReasoning {
		return &sessiondv1.CommandReceipt{
			OperationState: sessiondv1.OperationState_OPERATION_STATE_COMPLETED,
			Outcome: &sessiondv1.CommandReceipt_Rejected{
				Rejected: &sessiondv1.BusinessError{Message: "Effort rejected"},
			},
		}, nil
	}
	s.currentEffort = req.Effort
	return &sessiondv1.CommandReceipt{
		OperationState: sessiondv1.OperationState_OPERATION_STATE_COMPLETED,
		Outcome: &sessiondv1.CommandReceipt_Completed{
			Completed: &sessiondv1.CommandResult{},
		},
	}, nil
}

func (s *fakeCombinedDaemonServer) ReadApprovalMode(ctx context.Context, req *sessiondv1.ReadApprovalModeRequest) (*sessiondv1.ApprovalModeState, error) {
	return &sessiondv1.ApprovalModeState{
		Mode: s.currentMode,
	}, nil
}

func (s *fakeCombinedDaemonServer) SetApprovalMode(ctx context.Context, req *sessiondv1.SetApprovalModeRequest) (*sessiondv1.CommandReceipt, error) {
	if req.Meta == nil || req.Meta.OperationId == "" || req.Meta.Canonicalization != sessiondv1.CanonicalizationVersion_CANONICALIZATION_VERSION_LEGACY_JSON_V1 {
		return &sessiondv1.CommandReceipt{
			OperationState: sessiondv1.OperationState_OPERATION_STATE_UNCERTAIN,
		}, nil
	}
	if s.rejectNextSetApproval {
		return &sessiondv1.CommandReceipt{
			OperationState: sessiondv1.OperationState_OPERATION_STATE_COMPLETED,
			Outcome: &sessiondv1.CommandReceipt_Rejected{
				Rejected: &sessiondv1.BusinessError{Message: "Approval mode rejected"},
			},
		}, nil
	}
	s.currentMode = req.Mode
	return &sessiondv1.CommandReceipt{
		OperationState: sessiondv1.OperationState_OPERATION_STATE_COMPLETED,
		Outcome: &sessiondv1.CommandReceipt_Completed{
			Completed: &sessiondv1.CommandResult{},
		},
	}, nil
}

func (s *fakeCombinedDaemonServer) QueryNative(ctx context.Context, req *sessiondv1.NativeQueryRequest) (*sessiondv1.NativeQueryResponse, error) {
	resBytes, _ := json.Marshal(map[string]any{
		"data": []any{
			map[string]any{
				"skills": []any{
					map[string]any{
						"name":             "doc-search",
						"shortDescription": "Search docs",
						"path":             "/skills/doc-search",
						"enabled":          true,
					},
				},
			},
		},
	})
	return &sessiondv1.NativeQueryResponse{
		Result: &sessiondv1.NativeJson{
			Utf8Json: resBytes,
		},
	}, nil
}

func startFakeDaemonTarget(t *testing.T, srv *fakeCombinedDaemonServer) (string, func()) {
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s := grpc.NewServer()
	sessiondv1.RegisterDaemonControlServer(s, srv)
	sessiondv1.RegisterRuntimeQueryServer(s, srv)
	go func() {
		_ = s.Serve(lis)
	}()
	return lis.Addr().String(), func() {
		s.Stop()
		_ = lis.Close()
	}
}

func TestSettingsModelsAndCommands(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	ctx := context.Background()
	_, err = st.UpsertAgent(ctx, map[string]any{"id": "agent-1", "kind": "hermes", "name": "Agent 1", "status": "ready"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = st.UpsertAgent(ctx, map[string]any{"id": "codex-1", "kind": "codex", "name": "Codex 1", "status": "ready"})
	if err != nil {
		t.Fatal(err)
	}
	_, _ = st.UpsertSession(ctx, map[string]any{
		"id":         "sess-1",
		"agent_id":   "agent-1",
		"status":     "idle",
		"updated_at": time.Now().UTC().Format(time.RFC3339),
	})
	_, _ = st.UpsertSession(ctx, map[string]any{
		"id":         "sess-codex",
		"agent_id":   "codex-1",
		"status":     "idle",
		"updated_at": time.Now().UTC().Format(time.RFC3339),
	})

	srv := &fakeCombinedDaemonServer{
		currentProvider: "hermes",
		currentModel:    "chat",
		currentEffort:   "high",
		currentMode:     "auto",
	}
	addr, stop := startFakeDaemonTarget(t, srv)
	defer stop()

	dc, err := daemonclient.Dial(ctx, daemonclient.Config{
		Target:         addr,
		Credential:     "test-secret",
		RequestTimeout: 2 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer dc.Close()

	handler, err := NewHandler(Options{
		Store:      st,
		Client:     dc,
		Authorizer: auth.New(config.Config{BrowserSecret: "fixture"}, nil),
		Publisher:  application.NewEventHub(st),
	})
	if err != nil {
		t.Fatal(err)
	}

	h := handler.(*Handler)

	// List models
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/sessions/sess-1/models?agent_id=agent-1", nil)
	h.handleSessionModels(w, r, "sess-1")
	if w.Code != 200 {
		t.Fatalf("handleSessionModels failed: %d %s", w.Code, w.Body.String())
	}

	// Read model
	w = httptest.NewRecorder()
	r = httptest.NewRequest(http.MethodGet, "/api/v1/sessions/sess-1/model?agent_id=agent-1", nil)
	h.handleGetSessionModel(w, r, "sess-1")
	if w.Code != 200 {
		t.Fatalf("handleGetSessionModel failed: %d %s", w.Code, w.Body.String())
	}

	// Set model happy path
	setBody, _ := json.Marshal(map[string]any{
		"agent_id": "agent-1",
		"provider": "hermes",
		"model":    "custom-v2",
	})
	w = httptest.NewRecorder()
	r = httptest.NewRequest(http.MethodPost, "/api/v1/sessions/sess-1/model", bytes.NewReader(setBody))
	h.handleSetSessionModel(w, r, "sess-1")
	if w.Code != 200 {
		t.Fatalf("handleSetSessionModel failed: %d %s", w.Code, w.Body.String())
	}
	binding, err := st.GetSessionModelBinding(ctx, "agent-1", "sess-1")
	if err != nil || binding == nil || binding["model"] != "custom-v2" {
		t.Fatalf("binding not persisted: %+v, err: %v", binding, err)
	}

	// Set model with empty provider (Codex model without separate provider)
	setEmptyProviderBody, _ := json.Marshal(map[string]any{
		"agent_id": "agent-1",
		"provider": "",
		"model":    "o3-mini",
	})
	w = httptest.NewRecorder()
	r = httptest.NewRequest(http.MethodPost, "/api/v1/sessions/sess-1/model", bytes.NewReader(setEmptyProviderBody))
	h.handleSetSessionModel(w, r, "sess-1")
	if w.Code != 200 {
		t.Fatalf("handleSetSessionModel with empty provider failed: %d %s", w.Code, w.Body.String())
	}
	bindingEmptyProvider, err := st.GetSessionModelBinding(ctx, "agent-1", "sess-1")
	if err != nil || bindingEmptyProvider == nil || bindingEmptyProvider["model"] != "o3-mini" || bindingEmptyProvider["provider"] != "" {
		t.Fatalf("binding for empty provider not persisted correctly: %+v, err: %v", bindingEmptyProvider, err)
	}

	// Set model where frontend specifies provider but daemon returns empty provider (Codex native readback)
	srv.currentModel = "gpt-6-astra"
	srv.currentProvider = ""
	setCodexProviderBody, _ := json.Marshal(map[string]any{
		"agent_id": "agent-1",
		"provider": "opencodex",
		"model":    "gpt-6-astra",
	})
	w = httptest.NewRecorder()
	r = httptest.NewRequest(http.MethodPost, "/api/v1/sessions/sess-1/model", bytes.NewReader(setCodexProviderBody))
	h.handleSetSessionModel(w, r, "sess-1")
	if w.Code != 200 {
		t.Fatalf("handleSetSessionModel with codex empty readback provider failed: %d %s", w.Code, w.Body.String())
	}
	bindingCodex, err := st.GetSessionModelBinding(ctx, "agent-1", "sess-1")
	if err != nil || bindingCodex == nil || bindingCodex["model"] != "gpt-6-astra" {
		t.Fatalf("binding for codex not persisted correctly: %+v, err: %v", bindingCodex, err)
	}

	// 3b. Set model rejection (must not persist)
	srv.rejectNextSetModel = true
	rejBody, _ := json.Marshal(map[string]any{
		"agent_id": "agent-1",
		"provider": "hermes",
		"model":    "rejected-model",
	})
	w = httptest.NewRecorder()
	r = httptest.NewRequest(http.MethodPost, "/api/v1/sessions/sess-1/model", bytes.NewReader(rejBody))
	h.handleSetSessionModel(w, r, "sess-1")
	if w.Code != 502 {
		t.Fatalf("expected 502 on rejection: got %d", w.Code)
	}
	bindingRej, _ := st.GetSessionModelBinding(ctx, "agent-1", "sess-1")
	if bindingRej != nil && bindingRej["model"] == "rejected-model" {
		t.Fatalf("rejected model must not be persisted")
	}

	// 3c. Stale readback (must not persist)
	srv.rejectNextSetModel = false
	srv.staleReadbackSetModel = true
	staleBody, _ := json.Marshal(map[string]any{
		"agent_id": "agent-1",
		"provider": "hermes",
		"model":    "stale-intended-model",
	})
	w = httptest.NewRecorder()
	r = httptest.NewRequest(http.MethodPost, "/api/v1/sessions/sess-1/model", bytes.NewReader(staleBody))
	h.handleSetSessionModel(w, r, "sess-1")
	if w.Code != 502 {
		t.Fatalf("expected 502 on stale readback: got %d", w.Code)
	}
	bindingStale, _ := st.GetSessionModelBinding(ctx, "agent-1", "sess-1")
	if bindingStale != nil && bindingStale["model"] == "stale-intended-model" {
		t.Fatalf("stale model must not be persisted")
	}
	srv.staleReadbackSetModel = false

	// 4. Set reasoning
	effortBody, _ := json.Marshal(map[string]any{
		"agent_id": "agent-1",
		"effort":   "medium",
	})
	w = httptest.NewRecorder()
	r = httptest.NewRequest(http.MethodPost, "/api/v1/sessions/sess-1/reasoning", bytes.NewReader(effortBody))
	h.handleSetSessionReasoning(w, r, "sess-1")
	if w.Code != 200 {
		t.Fatalf("handleSetSessionReasoning failed: %d %s", w.Code, w.Body.String())
	}
	binding2, err := st.GetSessionModelBinding(ctx, "agent-1", "sess-1")
	if err != nil || binding2 == nil || binding2["effort"] != "medium" {
		t.Fatalf("reasoning effort binding not persisted: %+v, err: %v", binding2, err)
	}

	// 5. Get and Set Approval mode
	w = httptest.NewRecorder()
	r = httptest.NewRequest(http.MethodGet, "/api/v1/sessions/sess-1/approval-mode?agent_id=agent-1", nil)
	h.handleGetSessionApprovalMode(w, r, "sess-1")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "auto") {
		t.Fatalf("handleGetSessionApprovalMode failed: %d %s", w.Code, w.Body.String())
	}

	modeBody, _ := json.Marshal(map[string]any{
		"agent_id": "agent-1",
		"mode":     "full_access",
	})
	w = httptest.NewRecorder()
	r = httptest.NewRequest(http.MethodPost, "/api/v1/sessions/sess-1/approval-mode", bytes.NewReader(modeBody))
	h.handleSetSessionApprovalMode(w, r, "sess-1")
	if w.Code != 200 {
		t.Fatalf("handleSetSessionApprovalMode failed: %d %s", w.Code, w.Body.String())
	}
	sessRow, _ := st.GetSession(ctx, "agent-1", "sess-1")
	if sessRow["selected_approval_mode"] != "full_access" {
		t.Fatalf("approval mode not persisted: %+v", sessRow)
	}

	// 6. List commands
	w = httptest.NewRecorder()
	r = httptest.NewRequest(http.MethodGet, "/api/v1/sessions/sess-1/agent-commands?agent_id=agent-1", nil)
	h.handleSessionAgentCommands(w, r, "sess-1")
	if w.Code != 200 {
		t.Fatalf("handleSessionAgentCommands failed: %d %s", w.Code, w.Body.String())
	}

	// 7. Mentions with codex returns parsed skills
	w = httptest.NewRecorder()
	r = httptest.NewRequest(http.MethodGet, "/api/v1/sessions/sess-codex/agent-mentions?agent_id=codex-1", nil)
	h.handleSessionAgentMentions(w, r, "sess-codex")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "doc-search") {
		t.Fatalf("handleSessionAgentMentions codex failed: %d %s", w.Code, w.Body.String())
	}

	// 8. Nil client returns 503
	handlerNoClient, _ := NewHandler(Options{
		Store:      st,
		Client:     nil,
		Authorizer: auth.New(config.Config{BrowserSecret: "fixture"}, nil),
		Publisher:  application.NewEventHub(st),
	})
	hNoClient := handlerNoClient.(*Handler)
	w = httptest.NewRecorder()
	r = httptest.NewRequest(http.MethodGet, "/api/v1/sessions/sess-1/models?agent_id=agent-1", nil)
	hNoClient.handleSessionModels(w, r, "sess-1")
	if w.Code != 503 {
		t.Fatalf("expected 503 for nil client: got %d", w.Code)
	}

	// 9. Unknown session returns 404
	w = httptest.NewRecorder()
	r = httptest.NewRequest(http.MethodGet, "/api/v1/sessions/nonexistent/models?agent_id=agent-1", nil)
	h.handleSessionModels(w, r, "nonexistent")
	if w.Code != 404 {
		t.Fatalf("expected 404 for unknown session: got %d", w.Code)
	}

	// 10. Exact agent identity mismatch returns 404
	w = httptest.NewRecorder()
	r = httptest.NewRequest(http.MethodGet, "/api/v1/sessions/sess-1/models?agent_id=wrong-agent", nil)
	h.handleSessionModels(w, r, "sess-1")
	if w.Code != 404 {
		t.Fatalf("expected 404 for wrong agent: got %d", w.Code)
	}
}
