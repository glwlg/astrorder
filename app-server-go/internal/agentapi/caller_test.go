package agentapi

import (
	"context"
	"testing"
	"time"

	"astrorder.dev/app-server/internal/daemonclient"
	"astrorder.dev/app-server/internal/store"
	sessiondv1 "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
	"google.golang.org/grpc"
)

type mockRuntimeQueryClient struct {
	sessiondv1.RuntimeQueryClient
	queryFunc func(ctx context.Context, in *sessiondv1.NativeQueryRequest) (*sessiondv1.NativeQueryResponse, error)
}

func (m *mockRuntimeQueryClient) QueryNative(ctx context.Context, in *sessiondv1.NativeQueryRequest, opts ...grpc.CallOption) (*sessiondv1.NativeQueryResponse, error) {
	if m.queryFunc != nil {
		return m.queryFunc(ctx, in)
	}
	return &sessiondv1.NativeQueryResponse{}, nil
}

func TestParseCaller(t *testing.T) {
	// Missing caller object
	_, err := ParseCaller(nil)
	if err == nil {
		t.Fatal("expected error on nil caller")
	}

	// Missing fields
	incomplete := map[string]any{
		"agent_id": "test-agent",
		"runtime":  "hermes",
	}
	_, err = ParseCaller(incomplete)
	if err == nil {
		t.Fatal("expected error on missing fields")
	}

	// Native ID cannot be empty
	withEmptyID := map[string]any{
		"agent_id":      "local-hermes-default",
		"connection_id": "local",
		"profile_name":  "default",
		"runtime":       "hermes",
		"session_id":    "",
	}
	_, err = ParseCaller(withEmptyID)
	if err == nil {
		t.Fatal("expected error on empty session_id")
	}

	// Null profile must be rejected
	nullProfile := map[string]any{
		"agent_id":      "local-hermes-default",
		"connection_id": "local",
		"profile_name":  nil,
		"runtime":       "hermes",
		"session_id":    "sess-1",
	}
	_, err = ParseCaller(nullProfile)
	if err == nil {
		t.Fatal("expected error on null profile_name")
	}

	// Empty connection must be rejected
	emptyConn := map[string]any{
		"agent_id":      "local-hermes-default",
		"connection_id": "",
		"profile_name":  "default",
		"runtime":       "hermes",
		"session_id":    "sess-1",
	}
	_, err = ParseCaller(emptyConn)
	if err == nil {
		t.Fatal("expected error on empty connection_id")
	}

	// Whitespace in fields must be rejected (leading, trailing, and internal)
	spaceFields := []map[string]any{
		{"agent_id": " leading", "connection_id": "local", "profile_name": "default", "runtime": "hermes", "session_id": "sess-1"},
		{"agent_id": "agent", "connection_id": "local ", "profile_name": "default", "runtime": "hermes", "session_id": "sess-1"},
		{"agent_id": "agent", "connection_id": "local", "profile_name": " default ", "runtime": "hermes", "session_id": "sess-1"},
		{"agent_id": "agent", "connection_id": "local", "profile_name": "default", "runtime": " hermes", "session_id": "sess-1"},
		{"agent_id": "agent", "connection_id": "local", "profile_name": "default", "runtime": "hermes", "session_id": "sess-1 "},
		{"agent_id": "agent with space", "connection_id": "local", "profile_name": "default", "runtime": "hermes", "session_id": "sess-1"},
		{"agent_id": "agent", "connection_id": "local", "profile_name": "default", "runtime": "hermes", "session_id": "ab cd"},
		{"agent_id": "agent", "connection_id": "local", "profile_name": "default", "runtime": "hermes", "session_id": 12345},
	}
	for i, sf := range spaceFields {
		_, err := ParseCaller(sf)
		if err == nil {
			t.Fatalf("case %d: expected error on whitespace field in caller", i)
		}
	}

	// Valid caller with exact explicit fields
	valid := map[string]any{
		"agent_id":      "local-hermes-default",
		"connection_id": "local",
		"profile_name":  "default",
		"runtime":       "hermes",
		"session_id":    "20261019_123456_abc",
	}
	c, err := ParseCaller(valid)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c.SessionID != "20261019_123456_abc" {
		t.Fatalf("native ID mismatch: got '%s'", c.SessionID)
	}
	if c.ConnectionID != "local" {
		t.Fatalf("expected local connection, got %s", c.ConnectionID)
	}
	if c.ProfileName != "default" {
		t.Fatalf("expected default profile, got %s", c.ProfileName)
	}
}

func setupTestStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatalf("failed to create test store: %v", err)
	}
	return st
}

func TestValidateCaller(t *testing.T) {
	ctx := context.Background()
	st := setupTestStore(t)
	defer st.Close()

	// Create test agent
	_, err := st.UpsertAgent(ctx, map[string]any{
		"id":            "agent-codex-1",
		"kind":          "codex",
		"name":          "Local Codex",
		"status":        "ready",
		"connection_id": "local",
		"profile_name":  "default",
	})
	if err != nil {
		t.Fatalf("failed to upsert agent: %v", err)
	}

	h := &Handler{
		options: Options{
			Store: st,
		},
	}

	// Test 1: Mismatched runtime kind
	mismatchedRuntime := &Caller{
		AgentID:      "agent-codex-1",
		ConnectionID: "local",
		ProfileName:  "default",
		Runtime:      "hermes", // wrong, agent is codex
		SessionID:    "sess-1",
	}
	_, err = h.ValidateCaller(ctx, mismatchedRuntime)
	if err == nil {
		t.Fatal("expected error for mismatched runtime kind")
	}

	// Test 2: Mismatched connection_id
	mismatchedConn := &Caller{
		AgentID:      "agent-codex-1",
		ConnectionID: "ssh-remote-1", // wrong
		ProfileName:  "default",
		Runtime:      "codex",
		SessionID:    "sess-1",
	}
	_, err = h.ValidateCaller(ctx, mismatchedConn)
	if err == nil {
		t.Fatal("expected error for mismatched connection_id")
	}

	// Test 3: Mismatched profile_name
	mismatchedProfile := &Caller{
		AgentID:      "agent-codex-1",
		ConnectionID: "local",
		ProfileName:  "custom-profile", // wrong
		Runtime:      "codex",
		SessionID:    "sess-1",
	}
	_, err = h.ValidateCaller(ctx, mismatchedProfile)
	if err == nil {
		t.Fatal("expected error for mismatched profile_name")
	}

	// Test 4: Existing session in store
	_, err = st.UpsertSession(ctx, map[string]any{
		"id":         "sess-existing-1",
		"agent_id":   "agent-codex-1",
		"title":      "Existing Session",
		"workspace":  "C:/work",
		"status":     "idle",
		"updated_at": time.Now().UTC().Format(time.RFC3339Nano),
	})
	if err != nil {
		t.Fatalf("failed to insert session: %v", err)
	}

	validCaller := &Caller{
		AgentID:      "agent-codex-1",
		ConnectionID: "local",
		ProfileName:  "default",
		Runtime:      "codex",
		SessionID:    "sess-existing-1",
	}
	sess, err := h.ValidateCaller(ctx, validCaller)
	if err != nil {
		t.Fatalf("unexpected validation error: %v", err)
	}
	if sess["id"] != "sess-existing-1" {
		t.Fatalf("expected session id sess-existing-1, got %v", sess["id"])
	}
	// Test 5: External Codex session verified via QueryNative with matching thread ID
	queryCalled := false
	mockQuery := &mockRuntimeQueryClient{
		queryFunc: func(ctx context.Context, in *sessiondv1.NativeQueryRequest) (*sessiondv1.NativeQueryResponse, error) {
			queryCalled = true
			if in.GetCodexThreadRead() == nil || in.GetCodexThreadRead().ThreadId != "external-thread-1" {
				t.Fatalf("unexpected query payload: %+v", in)
			}
			return &sessiondv1.NativeQueryResponse{
				Result: &sessiondv1.NativeJson{
					Schema:   "astrorder.codex.thread.v1",
					Utf8Json: []byte(`{"thread":{"id":"external-thread-1","title":"Codex Ext Thread","cwd":"C:/codex/work"}}`),
				},
			}, nil
		},
	}
	hWithDaemon := &Handler{
		options: Options{
			Store: st,
			Client: &daemonclient.Client{
				Runtime: mockQuery,
			},
			DefaultWorkspace: "C:/work",
		},
	}

	externalCaller := &Caller{
		AgentID:      "agent-codex-1",
		ConnectionID: "local",
		ProfileName:  "default",
		Runtime:      "codex",
		SessionID:    "external-thread-1",
	}
	extSess, err := hWithDaemon.ValidateCaller(ctx, externalCaller)
	if err != nil {
		t.Fatalf("unexpected error validating external caller: %v", err)
	}
	if !queryCalled {
		t.Fatal("expected QueryNative to be called for external session")
	}
	if extSess["id"] != "external-thread-1" {
		t.Fatalf("expected registered external session id external-thread-1, got %v", extSess["id"])
	}

	// Test 6: External Codex session with mismatched thread ID in response -> rejected
	mockQueryMismatched := &mockRuntimeQueryClient{
		queryFunc: func(ctx context.Context, in *sessiondv1.NativeQueryRequest) (*sessiondv1.NativeQueryResponse, error) {
			return &sessiondv1.NativeQueryResponse{
				Result: &sessiondv1.NativeJson{
					Schema:   "astrorder.codex.thread.v1",
					Utf8Json: []byte(`{"thread":{"id":"different-thread-id"}}`),
				},
			}, nil
		},
	}
	hMismatched := &Handler{
		options: Options{
			Store:  st,
			Client: &daemonclient.Client{Runtime: mockQueryMismatched},
		},
	}
	_, err = hMismatched.ValidateCaller(ctx, &Caller{
		AgentID:      "agent-codex-1",
		ConnectionID: "local",
		ProfileName:  "default",
		Runtime:      "codex",
		SessionID:    "requested-thread",
	})
	if err == nil {
		t.Fatal("expected error on mismatched thread ID in codex query response")
	}

	// Test 7: External Grok session verification -> rejected as unsupported
	_, _ = st.UpsertAgent(ctx, map[string]any{
		"id":            "agent-grok-1",
		"kind":          "grok",
		"name":          "Local Grok",
		"status":        "ready",
		"connection_id": "local",
		"profile_name":  "default",
	})
	hGrok := &Handler{
		options: Options{
			Store:  st,
			Client: &daemonclient.Client{Runtime: mockQuery},
		},
	}
	_, err = hGrok.ValidateCaller(ctx, &Caller{
		AgentID:      "agent-grok-1",
		ConnectionID: "local",
		ProfileName:  "default",
		Runtime:      "grok",
		SessionID:    "grok-sess-1",
	})
	if err == nil {
		t.Fatal("expected unsupported error for external grok session")
	}

	// Test 8: External Hermes session verified via session history and profile matching
	_, _ = st.UpsertAgent(ctx, map[string]any{
		"id":            "agent-hermes-1",
		"kind":          "hermes",
		"name":          "Local Hermes",
		"status":        "ready",
		"connection_id": "local",
		"profile_name":  "default",
	})
	mockHermesQuery := &mockRuntimeQueryClient{
		queryFunc: func(ctx context.Context, in *sessiondv1.NativeQueryRequest) (*sessiondv1.NativeQueryResponse, error) {
			return &sessiondv1.NativeQueryResponse{
				Result: &sessiondv1.NativeJson{
					Schema: "astrorder.hermes.session.history.v1",
					Utf8Json: []byte(`{
						"result": {
							"session_id": "local-3c366f26d8764917a751c8e52d1d5d4621121ba4b6ff48fef8010141d2cefcaf",
							"stored_session_id": "local-3c366f26d8764917a751c8e52d1d5d4621121ba4b6ff48fef8010141d2cefcaf",
							"info": {
								"profile_name": "default",
								"cwd": "P:/workspace/glwlg/ai/astrorder",
								"title": "Hermes Session A"
							}
						}
					}`),
				},
			}, nil
		},
	}
	hHermes := &Handler{
		options: Options{
			Store:  st,
			Client: &daemonclient.Client{Runtime: mockHermesQuery},
		},
	}
	hermesCaller := &Caller{
		AgentID:      "agent-hermes-1",
		ConnectionID: "local",
		ProfileName:  "default",
		Runtime:      "hermes",
		SessionID:    "local-3c366f26d8764917a751c8e52d1d5d4621121ba4b6ff48fef8010141d2cefcaf",
	}
	hSess, err := hHermes.ValidateCaller(ctx, hermesCaller)
	if err != nil {
		t.Fatalf("unexpected error validating external hermes caller: %v", err)
	}
	if hSess["id"] != hermesCaller.SessionID {
		t.Fatalf("expected registered session id %s, got %v", hermesCaller.SessionID, hSess["id"])
	}

	// Test 9: External Hermes session with mismatched profile -> rejected
	hermesCallerWrongProfile := &Caller{
		AgentID:      "agent-hermes-1",
		ConnectionID: "local",
		ProfileName:  "custom-profile",
		Runtime:      "hermes",
		SessionID:    "local-3c366f26d8764917a751c8e52d1d5d4621121ba4b6ff48fef8010141d2cefcaf",
	}
	_, err = hHermes.ValidateCaller(ctx, hermesCallerWrongProfile)
	if err == nil {
		t.Fatal("expected error on hermes profile mismatch")
	}
}
