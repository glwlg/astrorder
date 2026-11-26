package agentapi

import (
	"context"
	"path/filepath"
	"testing"

	"astrorder.dev/app-server/internal/daemonclient"
	"astrorder.dev/app-server/internal/store"
	sessiondv1 "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
	"google.golang.org/grpc"
)

type mockBotRuntimeQuery struct {
	sessiondv1.RuntimeQueryClient
	approvalReq *sessiondv1.SetApprovalModeRequest
	modelReq    *sessiondv1.SetModelRequest
	effortReq   *sessiondv1.SetReasoningRequest
	readState   *sessiondv1.ModelState
}

func (m *mockBotRuntimeQuery) SetApprovalMode(_ context.Context, in *sessiondv1.SetApprovalModeRequest, _ ...grpc.CallOption) (*sessiondv1.CommandReceipt, error) {
	m.approvalReq = in
	return &sessiondv1.CommandReceipt{
		Accepted:       true,
		OperationState: sessiondv1.OperationState_OPERATION_STATE_COMPLETED,
		Session:        in.Session,
	}, nil
}

func (m *mockBotRuntimeQuery) ReadModel(_ context.Context, _ *sessiondv1.ReadModelRequest, _ ...grpc.CallOption) (*sessiondv1.ModelState, error) {
	if m.readState != nil {
		return m.readState, nil
	}
	return &sessiondv1.ModelState{}, nil
}

func (m *mockBotRuntimeQuery) SetModel(_ context.Context, in *sessiondv1.SetModelRequest, _ ...grpc.CallOption) (*sessiondv1.CommandReceipt, error) {
	m.modelReq = in
	if m.readState != nil {
		p := in.Provider
		mod := in.Model
		m.readState.Provider = &p
		m.readState.Model = &mod
	}
	return &sessiondv1.CommandReceipt{
		Accepted:       true,
		OperationState: sessiondv1.OperationState_OPERATION_STATE_COMPLETED,
		Session:        in.Session,
	}, nil
}

func (m *mockBotRuntimeQuery) SetReasoning(_ context.Context, in *sessiondv1.SetReasoningRequest, _ ...grpc.CallOption) (*sessiondv1.CommandReceipt, error) {
	m.effortReq = in
	if m.readState != nil {
		eff := in.Effort
		m.readState.Effort = &eff
	}
	return &sessiondv1.CommandReceipt{
		Accepted:       true,
		OperationState: sessiondv1.OperationState_OPERATION_STATE_COMPLETED,
		Session:        in.Session,
	}, nil
}

func TestSpawnForBotAppliesFullAccessAndModelPolicy(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "bots_spawn.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	ctx := context.Background()
	_, err = st.UpsertAgent(ctx, map[string]any{
		"id":            "agy-local",
		"kind":          "antigravity",
		"name":          "Antigravity",
		"status":        "ready",
		"connection_id": "local",
	})
	if err != nil {
		t.Fatalf("upsert agent: %v", err)
	}

	mockSess := &mockSessionControl{}
	curProv := "antigravity"
	curModel := "gemini-4-neon-high"
	mockRT := &mockBotRuntimeQuery{
		readState: &sessiondv1.ModelState{
			Provider: &curProv,
			Model:    &curModel,
		},
	}

	var submitted map[string]any
	rawHandler, err := NewHandler(Options{
		Store: st,
		Client: &daemonclient.Client{
			Session: mockSess,
			Runtime: mockRT,
		},
		CommandSubmit: func(_ context.Context, cmd map[string]any) (map[string]any, error) {
			submitted = cmd
			return map[string]any{"accepted": true}, nil
		},
	})
	if err != nil {
		t.Fatalf("new handler: %v", err)
	}
	h := rawHandler.(*Handler)

	res, err := h.SpawnForBot(ctx, map[string]any{
		"operation_id": "op-bot-1",
		"agent_id":     "agy-local",
		"title":        "[视频制作] 宣传片",
		"workspace":    t.TempDir(),
		"provider":     "antigravity",
		"model":        "gemini-4-neon-xhigh",
		"effort":       "xhigh",
		"prompt":       "生成宣传片",
	})
	if err != nil {
		t.Fatalf("SpawnForBot error: %v", err)
	}
	if res["ok"] != true {
		t.Fatalf("expected ok=true, got %+v", res)
	}

	// Antigravity model already includes -xhigh suffix, so CreateSession passes Model without redundant Effort.
	agyOpts := mockSess.createReqSaved.GetAntigravity()
	if agyOpts == nil || agyOpts.GetModel() != "gemini-4-neon-xhigh" || agyOpts.Effort != nil {
		t.Fatalf("unexpected antigravity create options: %+v", agyOpts)
	}
	// Approval mode defaults to full_access and is applied via Runtime.SetApprovalMode.
	if mockRT.approvalReq == nil || mockRT.approvalReq.Mode != "full_access" {
		t.Fatalf("expected SetApprovalMode full_access, got %+v", mockRT.approvalReq)
	}
	// Because ReadModel returned gemini-4-neon-high, SetModel should enforce gemini-4-neon-xhigh.
	if mockRT.modelReq == nil || mockRT.modelReq.Model != "gemini-4-neon-xhigh" {
		t.Fatalf("expected SetModel gemini-4-neon-xhigh, got %+v", mockRT.modelReq)
	}
	if mockRT.effortReq != nil {
		t.Fatalf("did not expect redundant SetReasoning for antigravity suffixed model, got %+v", mockRT.effortReq)
	}
	if submitted == nil || submitted["text"] != "生成宣传片" {
		t.Fatalf("expected prompt submitted, got %+v", submitted)
	}

	// Verify SQLite bindings persisted for UI & reconnect restore.
	sess, err := st.GetSession(ctx, "agy-local", "child-sess-100")
	if err != nil || sess == nil {
		t.Fatalf("get persisted session: err=%v sess=%+v", err, sess)
	}
	if sess["selected_approval_mode"] != "full_access" {
		t.Fatalf("selected_approval_mode = %v, want full_access", sess["selected_approval_mode"])
	}
	if sess["selected_model"] != "gemini-4-neon-xhigh" {
		t.Fatalf("selected_model = %v, want gemini-4-neon-xhigh", sess["selected_model"])
	}
}
