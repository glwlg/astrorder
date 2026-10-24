package agentapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"astrorder.dev/app-server/internal/auth"
	"astrorder.dev/app-server/internal/blackboard"
	"astrorder.dev/app-server/internal/daemonclient"
	"astrorder.dev/app-server/internal/store"
	"astrorder.dev/app-server/internal/swarm"
	sessiondv1 "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
	"google.golang.org/grpc"
)

type mockPublisher struct {
	events []struct {
		eventType string
		data      map[string]any
	}
}

func (m *mockPublisher) Publish(ctx context.Context, eventType string, data map[string]any) error {
	m.events = append(m.events, struct {
		eventType string
		data      map[string]any
	}{eventType: eventType, data: data})
	return nil
}

type mockSessionControl struct {
	sessiondv1.SessionControlClient
	createCalls    int32
	sendCalls      int32
	createFunc     func(ctx context.Context, in *sessiondv1.CreateSessionRequest) (*sessiondv1.CommandReceipt, error)
	sendFunc       func(ctx context.Context, in *sessiondv1.SendRequest) (*sessiondv1.CommandReceipt, error)
	createReqSaved *sessiondv1.CreateSessionRequest
	sendReqSaved   *sessiondv1.SendRequest
}

func (m *mockSessionControl) CreateSession(ctx context.Context, in *sessiondv1.CreateSessionRequest, opts ...grpc.CallOption) (*sessiondv1.CommandReceipt, error) {
	atomic.AddInt32(&m.createCalls, 1)
	m.createReqSaved = in
	if m.createFunc != nil {
		return m.createFunc(ctx, in)
	}
	return &sessiondv1.CommandReceipt{
		Accepted:       true,
		OperationState: sessiondv1.OperationState_OPERATION_STATE_COMPLETED,
		Status:         sessiondv1.SessionStatus_SESSION_STATUS_IDLE,
		Session: &sessiondv1.SessionRef{
			SessionId: "child-sess-100",
			AgentId:   in.Target.AgentId,
		},
		Outcome: &sessiondv1.CommandReceipt_Completed{
			Completed: &sessiondv1.CommandResult{Accepted: true},
		},
	}, nil
}

func (m *mockSessionControl) Send(ctx context.Context, in *sessiondv1.SendRequest, opts ...grpc.CallOption) (*sessiondv1.CommandReceipt, error) {
	atomic.AddInt32(&m.sendCalls, 1)
	m.sendReqSaved = in
	if m.sendFunc != nil {
		return m.sendFunc(ctx, in)
	}
	return &sessiondv1.CommandReceipt{
		Accepted:       true,
		OperationState: sessiondv1.OperationState_OPERATION_STATE_COMPLETED,
		Status:         sessiondv1.SessionStatus_SESSION_STATUS_RUNNING,
		Session:        in.Session,
		Outcome: &sessiondv1.CommandReceipt_Completed{
			Completed: &sessiondv1.CommandResult{Accepted: true},
		},
	}, nil
}

func setupDelegationTestEnvWithPublisher(t *testing.T) (*Handler, *store.Store, *mockSessionControl, *mockPublisher) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "delegation_test.db"))
	if err != nil {
		t.Fatalf("failed to open test store: %v", err)
	}

	ctx := context.Background()

	// Register caller agent
	_, err = st.UpsertAgent(ctx, map[string]any{
		"id":            "parent-hermes-default",
		"kind":          "hermes",
		"name":          "Parent Hermes",
		"status":        "ready",
		"connection_id": "local",
		"profile_name":  "default",
	})
	if err != nil {
		t.Fatalf("failed to upsert parent agent: %v", err)
	}

	// Register parent session
	_, err = st.UpsertSession(ctx, map[string]any{
		"id":         "parent-session-1",
		"agent_id":   "parent-hermes-default",
		"title":      "Parent Session",
		"workspace":  "C:/parent/work",
		"status":     "idle",
		"updated_at": time.Now().UTC().Format(time.RFC3339Nano),
	})
	if err != nil {
		t.Fatalf("failed to upsert parent session: %v", err)
	}

	// Register target child agent
	_, err = st.UpsertAgent(ctx, map[string]any{
		"id":            "child-codex-1",
		"kind":          "codex",
		"name":          "Child Codex",
		"status":        "ready",
		"connection_id": "local",
		"profile_name":  "default",
	})
	if err != nil {
		t.Fatalf("failed to upsert child agent: %v", err)
	}

	mockCtrl := &mockSessionControl{}
	mockPub := &mockPublisher{}
	h := &Handler{
		options: Options{
			Store:     st,
			Publisher: mockPub,
			Client: &daemonclient.Client{
				Session: mockCtrl,
			},
			DefaultWorkspace: "C:/default/workspace",
		},
	}
	h.blackboard = blackboard.NewService(st, mockPub)
	h.swarm = swarm.NewService(st, mockPub, h)

	return h, st, mockCtrl, mockPub
}

func setupDelegationTestEnv(t *testing.T) (*Handler, *store.Store, *mockSessionControl) {
	h, st, ctrl, _ := setupDelegationTestEnvWithPublisher(t)
	return h, st, ctrl
}

func TestDelegationOperationLifecycle(t *testing.T) {
	h, st, mockCtrl := setupDelegationTestEnv(t)
	defer st.Close()
	ctx := context.Background()

	input := map[string]any{
		"operation_id": "op-del-1",
		"agent_id":     "child-codex-1",
		"title":        "Sub Task A",
		"workspace":    "C:/custom/workspace",
		"model":        "antigravity/gemini-3.8-flash",
		"effort":       "high",
		"prompt":       "Execute step 1 and verify",
		"caller": map[string]any{
			"agent_id":      "parent-hermes-default",
			"connection_id": "local",
			"profile_name":  "default",
			"runtime":       "hermes",
			"session_id":    "parent-session-1",
		},
	}

	res, err := h.Invoke(ctx, "sessions.delegate", input)
	if err != nil {
		t.Fatalf("delegate failed: %v", err)
	}

	respMap, ok := res.(map[string]any)
	if !ok || respMap["ok"] != true {
		t.Fatalf("expected ok=true response, got %+v", res)
	}

	if respMap["stage"] != "completed" {
		t.Fatalf("expected completed stage, got %v", respMap["stage"])
	}

	// Verify child creation constraints
	if mockCtrl.createReqSaved == nil {
		t.Fatal("expected CreateSession to be called")
	}
	codexOpt := mockCtrl.createReqSaved.GetCodex()
	if codexOpt == nil {
		t.Fatal("expected Codex options")
	}
	if codexOpt.ParentSessionId != nil {
		t.Fatalf("child session must NOT have ParentSessionId/fork, got %v", *codexOpt.ParentSessionId)
	}
	if codexOpt.GetModel() != "antigravity/gemini-3.8-flash" {
		t.Fatalf("expected model passed to codex options, got %s", codexOpt.GetModel())
	}
	if codexOpt.GetEffort() != "high" {
		t.Fatalf("expected effort high, got %s", codexOpt.GetEffort())
	}

	// Verify prompt was sent
	if mockCtrl.sendReqSaved == nil {
		t.Fatal("expected Send to be called")
	}
	if len(mockCtrl.sendReqSaved.Input) == 0 || mockCtrl.sendReqSaved.Input[0].GetText().Text != "Execute step 1 and verify" {
		t.Fatalf("unexpected prompt sent: %+v", mockCtrl.sendReqSaved.Input)
	}

	// Verify explicit delegation edge in Store
	parentEdge, found, err := st.GetDelegationParent(ctx, "child-codex-1", "child-sess-100")
	if err != nil || !found {
		t.Fatalf("expected delegation edge to exist in store: %v", err)
	}
	if parentEdge.ParentAgentID != "parent-hermes-default" || parentEdge.ParentSessionID != "parent-session-1" {
		t.Fatalf("unexpected parent edge: %+v", parentEdge)
	}

	// Verify session projection includes delegation_parent_*
	childRow, err := st.GetSession(ctx, "child-codex-1", "child-sess-100")
	if err != nil || childRow == nil {
		t.Fatalf("expected child session row: %v", err)
	}
	if childRow["delegation_parent_agent_id"] != "parent-hermes-default" || childRow["delegation_parent_session_id"] != "parent-session-1" {
		t.Fatalf("child session row missing delegation_parent_* projection: %+v", childRow)
	}

	// Verify query operation via sessions.operation
	opRes, err := h.Invoke(ctx, "sessions.operation", map[string]any{
		"operation_id": "op-del-1",
		"caller": map[string]any{
			"agent_id":      "parent-hermes-default",
			"connection_id": "local",
			"profile_name":  "default",
			"runtime":       "hermes",
			"session_id":    "parent-session-1",
		},
	})
	if err != nil {
		t.Fatalf("sessions.operation failed: %v", err)
	}
	opResp, _ := opRes.(map[string]any)
	if opResp["stage"] != "completed" || opResp["operation_id"] != "op-del-1" {
		t.Fatalf("unexpected op query response: %+v", opRes)
	}

	// Verify unauthorized caller query fails
	_, err = h.Invoke(ctx, "sessions.operation", map[string]any{
		"operation_id": "op-del-1",
		"caller": map[string]any{
			"agent_id":      "parent-hermes-default",
			"connection_id": "local",
			"profile_name":  "default",
			"runtime":       "hermes",
			"session_id":    "other-session",
		},
	})
	if err == nil {
		t.Fatal("expected error querying operation from foreign session")
	}
}

func TestDelegationIdempotencyAndConflict(t *testing.T) {
	h, st, mockCtrl := setupDelegationTestEnv(t)
	defer st.Close()
	ctx := context.Background()

	baseInput := map[string]any{
		"operation_id": "op-idemp-1",
		"agent_id":     "child-codex-1",
		"title":        "Original Title",
		"workspace":    "C:/custom/workspace",
		"prompt":       "Prompt with | pipe character inside",
		"caller": map[string]any{
			"agent_id":      "parent-hermes-default",
			"connection_id": "local",
			"profile_name":  "default",
			"runtime":       "hermes",
			"session_id":    "parent-session-1",
		},
	}

	// 1. First execution
	res1, err := h.Invoke(ctx, "sessions.delegate", baseInput)
	if err != nil {
		t.Fatalf("first call failed: %v", err)
	}
	if atomic.LoadInt32(&mockCtrl.createCalls) != 1 || atomic.LoadInt32(&mockCtrl.sendCalls) != 1 {
		t.Fatalf("expected 1 create and 1 send call, got create=%d send=%d", mockCtrl.createCalls, mockCtrl.sendCalls)
	}

	// 2. Duplicate replay with exact same parameters
	res2, err := h.Invoke(ctx, "sessions.delegate", baseInput)
	if err != nil {
		t.Fatalf("duplicate replay failed: %v", err)
	}
	if atomic.LoadInt32(&mockCtrl.createCalls) != 1 || atomic.LoadInt32(&mockCtrl.sendCalls) != 1 {
		t.Fatalf("duplicate call must NOT re-execute native calls, got create=%d send=%d", mockCtrl.createCalls, mockCtrl.sendCalls)
	}
	m1, _ := res1.(map[string]any)
	m2, _ := res2.(map[string]any)
	if m1["operation_id"] != m2["operation_id"] || m2["stage"] != "completed" {
		t.Fatalf("expected identical replay result, got res1=%+v res2=%+v", res1, res2)
	}

	// 3. Conflict: same operation_id but changed title
	confTitle := copyMap(baseInput)
	confTitle["title"] = "Changed Title"
	_, err = h.Invoke(ctx, "sessions.delegate", confTitle)
	if err == nil {
		t.Fatal("expected conflict error when title changes")
	}

	// 4. Conflict: same operation_id but changed profile_name in caller
	confProfile := copyMap(baseInput)
	confCaller := copyMap(baseInput["caller"].(map[string]any))
	confCaller["profile_name"] = "other-profile"
	confProfile["caller"] = confCaller
	_, err = h.Invoke(ctx, "sessions.delegate", confProfile)
	if err == nil {
		t.Fatal("expected conflict error when caller profile changes")
	}

	// 5. Conflict: prompt with '|' changed boundary
	confPrompt := copyMap(baseInput)
	confPrompt["prompt"] = "Prompt with | different ending"
	_, err = h.Invoke(ctx, "sessions.delegate", confPrompt)
	if err == nil {
		t.Fatal("expected conflict error when prompt text differs")
	}
}

func TestDelegationUncertainHandling(t *testing.T) {
	h, st, mockCtrl := setupDelegationTestEnv(t)
	defer st.Close()
	ctx := context.Background()

	// Make daemon return RPC error during create
	mockCtrl.createFunc = func(ctx context.Context, in *sessiondv1.CreateSessionRequest) (*sessiondv1.CommandReceipt, error) {
		return nil, errors.New("network timeout talking to runtime daemon")
	}

	input := map[string]any{
		"operation_id": "op-uncertain-1",
		"agent_id":     "child-codex-1",
		"title":        "Will Fail",
		"prompt":       "Do something",
		"caller": map[string]any{
			"agent_id":      "parent-hermes-default",
			"connection_id": "local",
			"profile_name":  "default",
			"runtime":       "hermes",
			"session_id":    "parent-session-1",
		},
	}

	_, err := h.Invoke(ctx, "sessions.delegate", input)
	if err == nil {
		t.Fatal("expected uncertain error on create failure")
	}

	// Check stored operation record is uncertain
	op, found, qErr := st.GetDelegationOperation(ctx, "op-uncertain-1")
	if qErr != nil || !found {
		t.Fatalf("expected stored operation record: %v", qErr)
	}
	if op.Stage != store.OpStageUncertain {
		t.Fatalf("expected stage uncertain, got %v", op.Stage)
	}

	// Replay attempt must return uncertain immediately and NOT retry create
	initCalls := atomic.LoadInt32(&mockCtrl.createCalls)
	_, err = h.Invoke(ctx, "sessions.delegate", input)
	if err == nil {
		t.Fatal("expected uncertain error on replay of uncertain operation")
	}
	if atomic.LoadInt32(&mockCtrl.createCalls) != initCalls {
		t.Fatalf("uncertain operation must NOT be blindly retried, calls before=%d now=%d", initCalls, mockCtrl.createCalls)
	}
}

func TestMCPIsolationAndRejection(t *testing.T) {
	h, st, _ := setupDelegationTestEnv(t)
	defer st.Close()

	// 1. MCP exports no tools; the real HTTP catalog still has all 42 capabilities.
	if tools := Tools(); len(tools) != 0 {
		t.Fatalf("expected no MCP tools, got %d", len(tools))
	}
	if len(AllCapabilities) != 42 {
		t.Fatalf("expected 42 HTTP capabilities, got %d", len(AllCapabilities))
	}

	// 2. Calling sessions.delegate over MCP tools/call must be rejected
	mcpReq := map[string]any{
		"jsonrpc": "2.0",
		"id":      10,
		"method":  "tools/call",
		"params": map[string]any{
			"name": "sessions_delegate",
			"arguments": map[string]any{
				"operation_id": "op-mcp-bypass",
			},
		},
	}
	resp := HandleRPC(mcpReq, func(cap string, payload map[string]any) (any, error) {
		return h.Invoke(context.Background(), cap, payload)
	})
	if resp["error"] == nil {
		t.Fatal("expected MCP tools/call to reject sessions_delegate")
	}
}

func TestSessionsTreeExplicitEdge(t *testing.T) {
	h, st, _ := setupDelegationTestEnv(t)
	defer st.Close()
	ctx := context.Background()

	// Create root session
	_, err := st.UpsertSession(ctx, map[string]any{
		"id":         "root-sess-1",
		"agent_id":   "parent-hermes-default",
		"title":      "Root Session",
		"workspace":  "C:/parent/work",
		"status":     "idle",
		"updated_at": time.Now().UTC().Format(time.RFC3339Nano),
	})
	if err != nil {
		t.Fatalf("failed to insert root: %v", err)
	}

	// Create child session with explicit delegation edge
	_, err = st.UpsertSession(ctx, map[string]any{
		"id":         "child-sess-1",
		"agent_id":   "child-codex-1",
		"title":      "Child Session",
		"workspace":  "C:/child/work",
		"status":     "idle",
		"updated_at": time.Now().UTC().Format(time.RFC3339Nano),
	})
	if err != nil {
		t.Fatalf("failed to insert child: %v", err)
	}

	err = st.CreateDelegationEdge(ctx, store.DelegationEdge{
		ParentAgentID:   "parent-hermes-default",
		ParentSessionID: "root-sess-1",
		ChildAgentID:    "child-codex-1",
		ChildSessionID:  "child-sess-1",
	})
	if err != nil {
		t.Fatalf("failed to insert delegation edge: %v", err)
	}

	// Query tree starting from root
	res, err := h.Invoke(ctx, "sessions.tree", map[string]any{
		"key": "parent-hermes-default::root-sess-1",
	})
	if err != nil {
		t.Fatalf("sessions.tree failed: %v", err)
	}

	resMap := res.(map[string]any)
	treeNode := resMap["tree"].(map[string]any)
	children := treeNode["children"].([]map[string]any)
	if len(children) != 1 {
		t.Fatalf("expected 1 child in tree, got %d", len(children))
	}
	if children[0]["id"] != "child-sess-1" {
		t.Fatalf("expected child-sess-1, got %v", children[0]["id"])
	}
}

func TestReceiptStrictMatching(t *testing.T) {
	h, st, mockCtrl := setupDelegationTestEnv(t)
	defer st.Close()
	ctx := context.Background()

	// 1. CreateSession returns foreign agent_id
	foreignAgent := "foreign-agent-999"
	mockCtrl.createFunc = func(ctx context.Context, in *sessiondv1.CreateSessionRequest) (*sessiondv1.CommandReceipt, error) {
		return &sessiondv1.CommandReceipt{
			Accepted:       true,
			OperationState: sessiondv1.OperationState_OPERATION_STATE_COMPLETED,
			Status:         sessiondv1.SessionStatus_SESSION_STATUS_IDLE,
			Session: &sessiondv1.SessionRef{
				SessionId: "child-foreign",
				AgentId:   &foreignAgent,
			},
		}, nil
	}

	input := map[string]any{
		"operation_id": "op-receipt-fail-1",
		"agent_id":     "child-codex-1",
		"title":        "Will Fail on Receipt",
		"prompt":       "Prompt",
		"caller": map[string]any{
			"agent_id":      "parent-hermes-default",
			"connection_id": "local",
			"profile_name":  "default",
			"runtime":       "hermes",
			"session_id":    "parent-session-1",
		},
	}
	_, err := h.Invoke(ctx, "sessions.delegate", input)
	if err == nil {
		t.Fatal("expected rejection when create receipt returns foreign agent_id")
	}

	// 2. Send returns mismatched session_id
	mockCtrl.createFunc = nil // restore normal create
	mockCtrl.sendFunc = func(ctx context.Context, in *sessiondv1.SendRequest) (*sessiondv1.CommandReceipt, error) {
		return &sessiondv1.CommandReceipt{
			Accepted:       true,
			OperationState: sessiondv1.OperationState_OPERATION_STATE_COMPLETED,
			Status:         sessiondv1.SessionStatus_SESSION_STATUS_RUNNING,
			Session: &sessiondv1.SessionRef{
				SessionId: "different-child-session-id",
			},
		}, nil
	}

	input2 := map[string]any{
		"operation_id": "op-receipt-fail-2",
		"agent_id":     "child-codex-1",
		"title":        "Will Fail on Send Receipt",
		"prompt":       "Prompt",
		"caller": map[string]any{
			"agent_id":      "parent-hermes-default",
			"connection_id": "local",
			"profile_name":  "default",
			"runtime":       "hermes",
			"session_id":    "parent-session-1",
		},
	}
	_, err = h.Invoke(ctx, "sessions.delegate", input2)
	if err == nil {
		t.Fatal("expected rejection when send receipt returns mismatched session_id")
	}
}

func TestInvokeCallerCollisionAndNullRejection(t *testing.T) {
	h, st, _ := setupDelegationTestEnv(t)
	defer st.Close()
	ctx := context.Background()

	// 1. Explicit null caller must be rejected
	_, err := h.Invoke(ctx, "blackboard.get", map[string]any{
		"caller": nil,
		"key":    "test-key",
	})
	if err == nil {
		t.Fatal("expected rejection when caller is explicitly null")
	}

	// 2. Blackboard capability with caller and conflicting session_key
	_, err = h.Invoke(ctx, "blackboard.get", map[string]any{
		"caller": map[string]any{
			"agent_id":      "parent-hermes-default",
			"connection_id": "local",
			"profile_name":  "default",
			"runtime":       "hermes",
			"session_id":    "parent-session-1",
		},
		"session_key": "other-agent::other-session",
		"key":         "test-key",
	})
	if err == nil {
		t.Fatal("expected rejection when session_key conflicts with caller identity in blackboard")
	}

	// 3. Swarm telemetry with caller and conflicting agent_id
	_, err = h.Invoke(ctx, "swarm.telemetry.get", map[string]any{
		"caller": map[string]any{
			"agent_id":      "parent-hermes-default",
			"connection_id": "local",
			"profile_name":  "default",
			"runtime":       "hermes",
			"session_id":    "parent-session-1",
		},
		"agent_id": "conflicting-agent",
	})
	if err == nil {
		t.Fatal("expected rejection when agent_id conflicts with caller identity in swarm telemetry")
	}
}

func TestBlackboardKeyWithColonAndNamespacePreservation(t *testing.T) {
	h, st, _ := setupDelegationTestEnv(t)
	defer st.Close()
	ctx := context.Background()

	callerMap := map[string]any{
		"agent_id":      "parent-hermes-default",
		"connection_id": "local",
		"profile_name":  "default",
		"runtime":       "hermes",
		"session_id":    "parent-session-1",
	}

	// 1. Blackboard set with key containing "::" (e.g. 'dataset::row') must NOT be rejected
	_, err := h.Invoke(ctx, "blackboard.set", map[string]any{
		"caller": callerMap,
		"key":    "dataset::row",
		"value":  "row-data-123",
	})
	if err != nil {
		t.Fatalf("blackboard.set with key 'dataset::row' should succeed, got: %v", err)
	}

	// 2. Blackboard get with key containing "::" must succeed and return value
	getResp, err := h.Invoke(ctx, "blackboard.get", map[string]any{
		"caller": callerMap,
		"key":    "dataset::row",
	})
	if err != nil {
		t.Fatalf("blackboard.get with key 'dataset::row' should succeed, got: %v", err)
	}
	getMap, ok := getResp.(map[string]any)
	if !ok || getMap["exists"] != true || getMap["key"] != "dataset::row" {
		t.Fatalf("unexpected get result: %v", getResp)
	}

	// 3. Explicit parent_key in blackboard.set must not be shadowed by injected session_key
	_, err = h.Invoke(ctx, "blackboard.set", map[string]any{
		"caller":     callerMap,
		"parent_key": "custom_parent::parent_session",
		"key":        "parent_data",
		"value":      "parent_scoped_value",
	})
	if err != nil {
		t.Fatalf("blackboard.set with explicit parent_key should succeed, got: %v", err)
	}

	// 4. Explicit namespace in blackboard.set must succeed
	_, err = h.Invoke(ctx, "blackboard.set", map[string]any{
		"caller":    callerMap,
		"namespace": "global",
		"key":       "global_data",
		"value":     "global_val",
	})
	if err != nil {
		t.Fatalf("blackboard.set with explicit namespace should succeed, got: %v", err)
	}
}

func TestSwarmTelemetryInspectionAndReportKey(t *testing.T) {
	h, st, _ := setupDelegationTestEnv(t)
	defer st.Close()
	ctx := context.Background()

	callerMap := map[string]any{
		"agent_id":      "parent-hermes-default",
		"connection_id": "local",
		"profile_name":  "default",
		"runtime":       "hermes",
		"session_id":    "parent-session-1",
	}

	// 1. swarm.telemetry.get inspecting another worker's telemetry key must be allowed
	_, err := h.Invoke(ctx, "swarm.telemetry.get", map[string]any{
		"caller": callerMap,
		"key":    "other-agent::other-session",
	})
	if err != nil {
		t.Fatalf("swarm.telemetry.get should allow inspecting another worker key, got: %v", err)
	}

	// 2. swarm.telemetry.report with mismatched worker key must be rejected
	_, err = h.Invoke(ctx, "swarm.telemetry.report", map[string]any{
		"caller": callerMap,
		"key":    "wrong-agent::wrong-session",
		"state":  "running",
	})
	if err == nil {
		t.Fatal("expected rejection when swarm.telemetry.report key conflicts with caller")
	}
}

func TestParseDelegateInputValidation(t *testing.T) {
	validCaller := map[string]any{
		"agent_id":      "parent-hermes-default",
		"connection_id": "local",
		"profile_name":  "default",
		"runtime":       "hermes",
		"session_id":    "parent-session-1",
	}

	// 1. operation_id with internal whitespace
	_, err := ParseDelegateInput(map[string]any{
		"operation_id": "op del 1",
		"agent_id":     "child-codex-1",
		"prompt":       "test",
		"caller":       validCaller,
	})
	if err == nil {
		t.Fatal("expected error on operation_id with whitespace")
	}

	// 2. agent_id with internal whitespace
	_, err = ParseDelegateInput(map[string]any{
		"operation_id": "op-del-1",
		"agent_id":     "child codex 1",
		"prompt":       "test",
		"caller":       validCaller,
	})
	if err == nil {
		t.Fatal("expected error on agent_id with whitespace")
	}

	// 3. title is null
	_, err = ParseDelegateInput(map[string]any{
		"operation_id": "op-del-1",
		"agent_id":     "child-codex-1",
		"prompt":       "test",
		"title":        nil,
		"caller":       validCaller,
	})
	if err == nil {
		t.Fatal("expected error on null title")
	}

	// 4. model is non-string
	_, err = ParseDelegateInput(map[string]any{
		"operation_id": "op-del-1",
		"agent_id":     "child-codex-1",
		"prompt":       "test",
		"model":        12345,
		"caller":       validCaller,
	})
	if err == nil {
		t.Fatal("expected error on non-string model")
	}

	// 5. workspace is non-string
	_, err = ParseDelegateInput(map[string]any{
		"operation_id": "op-del-1",
		"agent_id":     "child-codex-1",
		"prompt":       "test",
		"workspace":    true,
		"caller":       validCaller,
	})
	if err == nil {
		t.Fatal("expected error on boolean workspace")
	}
}

func TestDelegationFailedOperationRetry(t *testing.T) {
	h, st, mockCtrl := setupDelegationTestEnv(t)
	defer st.Close()
	ctx := context.Background()

	caller := &Caller{
		AgentID:      "parent-hermes-default",
		ConnectionID: "local",
		ProfileName:  "default",
		Runtime:      "hermes",
		SessionID:    "parent-session-1",
	}

	input := &DelegateInput{
		OperationID: "op-failed-test",
		AgentID:     "child-codex-1",
		Prompt:      "prompt",
		Caller:      caller,
	}

	hash := computeRequestHash(input, caller)
	_, _, _ = st.ReserveDelegationOperation(ctx, store.DelegationOperation{
		OperationID:     "op-failed-test",
		RequestHash:     hash,
		Stage:           store.OpStageReserved,
		CallerAgentID:   caller.AgentID,
		CallerSessionID: caller.SessionID,
		TargetAgentID:   "child-codex-1",
	})
	_ = st.TransitionDelegationOperation(ctx, "op-failed-test", store.OpStageReserved, store.OpStageCreating, nil)
	_ = st.FailDelegationOperation(ctx, "op-failed-test", "unrecoverable sandbox crash")

	// Invoke sessions.delegate with same op
	resp, err := h.Invoke(ctx, "sessions.delegate", map[string]any{
		"operation_id": "op-failed-test",
		"agent_id":     "child-codex-1",
		"prompt":       "prompt",
		"caller": map[string]any{
			"agent_id":      "parent-hermes-default",
			"connection_id": "local",
			"profile_name":  "default",
			"runtime":       "hermes",
			"session_id":    "parent-session-1",
		},
	})
	if err == nil {
		t.Fatal("expected error on retrying failed operation")
	}

	respMap, ok := resp.(map[string]any)
	if !ok {
		t.Fatalf("expected map response, got: %v", resp)
	}
	if respMap["ok"] != false || respMap["in_progress"] != false || respMap["stage"] != "failed" {
		t.Fatalf("expected ok:false and in_progress:false on failed operation, got: %v", respMap)
	}
	if atomic.LoadInt32(&mockCtrl.createCalls) > 0 {
		t.Fatal("daemon CreateSession should NOT have been called on failed op retry")
	}
}

func TestSessionsOperationUncertainWithChild(t *testing.T) {
	h, st, _ := setupDelegationTestEnv(t)
	defer st.Close()
	ctx := context.Background()

	callerMap := map[string]any{
		"agent_id":      "parent-hermes-default",
		"connection_id": "local",
		"profile_name":  "default",
		"runtime":       "hermes",
		"session_id":    "parent-session-1",
	}

	childSID := "candidate-child-sess-456"
	errMsg := "network timeout after child creation"
	_, _, _ = st.ReserveDelegationOperation(ctx, store.DelegationOperation{
		OperationID:     "op-uncertain-child",
		RequestHash:     "dummyhash",
		Stage:           store.OpStageReserved,
		CallerAgentID:   "parent-hermes-default",
		CallerSessionID: "parent-session-1",
		TargetAgentID:   "child-codex-1",
	})
	_ = st.TransitionDelegationOperation(ctx, "op-uncertain-child", store.OpStageReserved, store.OpStageCreating, nil)
	_ = st.TransitionDelegationOperation(ctx, "op-uncertain-child", store.OpStageCreating, store.OpStageUncertain, &store.DelegationOperationUpdate{
		TargetSessionID: &childSID,
		ErrorMessage:    &errMsg,
	})

	// Query sessions.operation
	res, err := h.Invoke(ctx, "sessions.operation", map[string]any{
		"operation_id": "op-uncertain-child",
		"caller":       callerMap,
	})
	if err != nil {
		t.Fatalf("sessions.operation failed: %v", err)
	}
	resMap, ok := res.(map[string]any)
	if !ok {
		t.Fatalf("expected map, got: %v", res)
	}

	if resMap["ok"] != false || resMap["stage"] != "uncertain" || resMap["target_session_id"] != childSID {
		t.Fatalf("unexpected operation response: %v", resMap)
	}
	sess, ok := resMap["session"].(map[string]any)
	if !ok || sess["id"] != childSID || sess["delegation_parent_session_id"] != "parent-session-1" {
		t.Fatalf("expected session metadata with delegation parent, got: %v", resMap["session"])
	}
}

func TestCreateReceiptAcceptedFalsePreservesCandidate(t *testing.T) {
	h, st, mockCtrl := setupDelegationTestEnv(t)
	defer st.Close()
	ctx := context.Background()

	mockCtrl.createFunc = func(ctx context.Context, in *sessiondv1.CreateSessionRequest) (*sessiondv1.CommandReceipt, error) {
		aid := in.Target.AgentId
		return &sessiondv1.CommandReceipt{
			Accepted:       false,
			OperationState: sessiondv1.OperationState_OPERATION_STATE_COMPLETED,
			Session: &sessiondv1.SessionRef{
				SessionId: "cand-child-789",
				AgentId:   aid,
			},
		}, nil
	}

	callerMap := map[string]any{
		"agent_id":      "parent-hermes-default",
		"connection_id": "local",
		"profile_name":  "default",
		"runtime":       "hermes",
		"session_id":    "parent-session-1",
	}

	_, err := h.Invoke(ctx, "sessions.delegate", map[string]any{
		"operation_id": "op-cand-preserve",
		"agent_id":     "child-codex-1",
		"prompt":       "test",
		"caller":       callerMap,
	})
	if err == nil {
		t.Fatal("expected uncertain error when Accepted=false")
	}

	op, found, _ := st.GetDelegationOperation(ctx, "op-cand-preserve")
	if !found {
		t.Fatal("operation record not found")
	}
	if op.Stage != store.OpStageUncertain || op.TargetSessionID != "cand-child-789" {
		t.Fatalf("expected uncertain stage with candidate child preserved, got: stage=%s, target_session_id=%s", op.Stage, op.TargetSessionID)
	}

	// Verify no delegation edge was created
	edges, _ := st.ListDelegationChildren(ctx, "parent-hermes-default", "parent-session-1")
	if len(edges) != 0 {
		t.Fatalf("expected 0 delegation edges for unaccepted child, got: %d", len(edges))
	}
}

func TestDelegationPublishesSessionUpsert(t *testing.T) {
	h, st, _, mockPub := setupDelegationTestEnvWithPublisher(t)
	defer st.Close()
	ctx := context.Background()

	callerMap := map[string]any{
		"agent_id":      "parent-hermes-default",
		"connection_id": "local",
		"profile_name":  "default",
		"runtime":       "hermes",
		"session_id":    "parent-session-1",
	}

	_, err := h.Invoke(ctx, "sessions.delegate", map[string]any{
		"operation_id": "op-publish-test",
		"agent_id":     "child-codex-1",
		"prompt":       "test prompt",
		"caller":       callerMap,
	})
	if err != nil {
		t.Fatalf("sessions.delegate failed: %v", err)
	}

	// Verify publisher received session.upsert for child session with delegation fields
	var foundChildUpsert bool
	for _, ev := range mockPub.events {
		if ev.eventType == "session.upsert" && ev.data["id"] == "child-sess-100" {
			foundChildUpsert = true
			if ev.data["delegation_parent_agent_id"] != "parent-hermes-default" {
				t.Fatalf("expected delegation_parent_agent_id 'parent-hermes-default', got: %v", ev.data["delegation_parent_agent_id"])
			}
			if ev.data["delegation_parent_session_id"] != "parent-session-1" {
				t.Fatalf("expected delegation_parent_session_id 'parent-session-1', got: %v", ev.data["delegation_parent_session_id"])
			}
		}
	}
	if !foundChildUpsert {
		t.Fatal("expected session.upsert event for created child session")
	}
}

type mockTestAuthorizer struct{}

func (m *mockTestAuthorizer) AuthorizeAgent(r *http.Request) (auth.Principal, *auth.Error) {
	return auth.Principal("test-agent"), nil
}

func (m *mockTestAuthorizer) AuthorizeBrowser(r *http.Request) *auth.Error {
	return nil
}

func TestServeHTTPCatalogReturnsAllCapabilities(t *testing.T) {
	h, st, _ := setupDelegationTestEnv(t)
	defer st.Close()
	h.options.Authorizer = &mockTestAuthorizer{}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/agent/catalog", nil)
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got: %d", rec.Code)
	}

	var body struct {
		Items []struct {
			ID string `json:"id"`
		} `json:"items"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if len(body.Items) != len(AllCapabilities) {
		t.Fatalf("expected %d capabilities, got: %d", len(AllCapabilities), len(body.Items))
	}

	var hasDelegate, hasOperation bool
	for _, item := range body.Items {
		if item.ID == "sessions.delegate" {
			hasDelegate = true
		}
		if item.ID == "sessions.operation" {
			hasOperation = true
		}
	}
	if !hasDelegate || !hasOperation {
		t.Fatal("expected AllCapabilities to include sessions.delegate and sessions.operation")
	}
}

func copyMap(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}
