package blackboard

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"astrorder.dev/app-server/internal/store"
)

func createTestStore(t *testing.T) *store.Store {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func seedSession(t *testing.T, st *store.Store, agentID, sessionID string) {
	t.Helper()
	ctx := context.Background()
	_, _ = st.UpsertAgent(ctx, map[string]any{
		"id":           agentID,
		"kind":         "hermes",
		"name":         agentID,
		"status":       "ready",
		"capabilities": []string{"chat"},
	})
	_, _ = st.UpsertSession(ctx, map[string]any{
		"id":         sessionID,
		"agent_id":   agentID,
		"title":      "Session " + sessionID,
		"status":     "running",
		"updated_at": time.Now().UTC().Format(time.RFC3339Nano),
	})
}

func TestResolveNamespaceRejectsUniqueActiveGuessing(t *testing.T) {
	st := createTestStore(t)
	svc := NewService(st, nil)
	ctx := context.Background()

	// Seed exactly one running session for agent-a
	seedSession(t, st, "agent-a", "s-1")

	// Call ResolveNamespace without session_id (only caller_agent_id)
	// Must FAIL clearly, NOT guess the active session!
	_, err := svc.ResolveNamespace(ctx, map[string]any{
		"caller_agent_id": "agent-a",
	})
	if err == nil {
		t.Fatalf("expected ResolveNamespace to fail without session identity, but it guessed active session")
	}

	// Also fails when agent_id is provided without session_id
	_, err = svc.ResolveNamespace(ctx, map[string]any{
		"agent_id": "agent-a",
	})
	if err == nil {
		t.Fatalf("expected ResolveNamespace to fail without session identity")
	}
}

func TestResolveNamespaceCallerObjectAndConflictValidation(t *testing.T) {
	st := createTestStore(t)
	svc := NewService(st, nil)
	ctx := context.Background()

	seedSession(t, st, "agent-1", "s-1")

	// 1. Valid nested caller object
	ns, err := svc.ResolveNamespace(ctx, map[string]any{
		"caller": map[string]any{
			"agent_id":   "agent-1",
			"session_id": "s-1",
		},
	})
	if err != nil {
		t.Fatalf("unexpected error with valid caller: %v", err)
	}
	if ns != "session:agent-1::s-1" {
		t.Fatalf("unexpected namespace: %s", ns)
	}

	// 2. Conflicting top-level vs nested caller
	_, err = svc.ResolveNamespace(ctx, map[string]any{
		"caller": map[string]any{
			"agent_id":   "agent-1",
			"session_id": "s-1",
		},
		"caller_session_id": "s-2",
	})
	if err == nil {
		t.Fatalf("expected conflict error between nested caller and top-level session_id")
	}

	// 3. Invalid caller type
	_, err = svc.ResolveNamespace(ctx, map[string]any{
		"caller": "not-an-object",
	})
	if err == nil {
		t.Fatalf("expected error for non-object caller")
	}
}

func TestGetSessionScopeCollaborationTreeVsFork(t *testing.T) {
	st := createTestStore(t)
	svc := NewService(st, nil)
	ctx := context.Background()

	// Seed sessions A, B, C
	seedSession(t, st, "agent-a", "s-a")
	seedSession(t, st, "agent-b", "s-b")
	seedSession(t, st, "agent-c", "s-c")

	// Set up delegation edges: A -> B -> C
	err := st.CreateDelegationEdge(ctx, store.DelegationEdge{
		ParentAgentID:   "agent-a",
		ParentSessionID: "s-a",
		ChildAgentID:    "agent-b",
		ChildSessionID:  "s-b",
	})
	if err != nil {
		t.Fatalf("CreateDelegationEdge A->B: %v", err)
	}
	err = st.CreateDelegationEdge(ctx, store.DelegationEdge{
		ParentAgentID:   "agent-b",
		ParentSessionID: "s-b",
		ChildAgentID:    "agent-c",
		ChildSessionID:  "s-c",
	})
	if err != nil {
		t.Fatalf("CreateDelegationEdge B->C: %v", err)
	}

	// A, B, C must all resolve to root swarm:agent-a::s-a
	for _, tc := range []struct {
		aid, sid string
	}{
		{"agent-a", "s-a"},
		{"agent-b", "s-b"},
		{"agent-c", "s-c"},
	} {
		scope, err := svc.GetSessionScope(ctx, tc.aid, tc.sid)
		if err != nil {
			t.Fatalf("GetSessionScope(%s::%s): %v", tc.aid, tc.sid, err)
		}
		if scope != "swarm:agent-a::s-a" {
			t.Fatalf("expected swarm:agent-a::s-a for %s::%s, got %s", tc.aid, tc.sid, scope)
		}
	}

	// Now seed session D as a normal fork/handoff from A (without delegation edge)
	_, _ = st.UpsertSession(ctx, map[string]any{
		"id":                      "s-fork",
		"agent_id":                "agent-d",
		"title":                   "Forked Session",
		"status":                  "idle",
		"handoff_from_agent_id":   "agent-a",
		"handoff_from_session_id": "s-a",
		"updated_at":              time.Now().UTC().Format(time.RFC3339Nano),
	})

	// D must NOT get swarm:agent-a::s-a! Must remain in isolated session scope!
	scopeFork, err := svc.GetSessionScope(ctx, "agent-d", "s-fork")
	if err != nil {
		t.Fatalf("GetSessionScope(s-fork): %v", err)
	}
	if scopeFork != "session:agent-d::s-fork" {
		t.Fatalf("fork session leaked into swarm scope: got %s, want session:agent-d::s-fork", scopeFork)
	}

	// ResolveNamespace using parent_key of D must also not guess swarm
	nsParentKey, err := svc.ResolveNamespace(ctx, map[string]any{
		"parent_key": "agent-d::s-fork",
	})
	if err != nil {
		t.Fatalf("ResolveNamespace with parent_key: %v", err)
	}
	if nsParentKey != "session:agent-d::s-fork" {
		t.Fatalf("parent_key for non-delegation session leaked into swarm: %s", nsParentKey)
	}

	// Explicit blackboard_scope preference overrides delegation tree
	_ = st.SetPreference(ctx, "blackboard_scope:agent-c::s-c", "custom-scope", true)
	scopeOverride, err := svc.GetSessionScope(ctx, "agent-c", "s-c")
	if err != nil || scopeOverride != "custom-scope" {
		t.Fatalf("preference override failed: got %s", scopeOverride)
	}
}

func TestResolveNamespaceMalformedAndNullRejections(t *testing.T) {
	st := createTestStore(t)
	svc := NewService(st, nil)
	ctx := context.Background()

	cases := []struct {
		name    string
		payload map[string]any
	}{
		{"null caller", map[string]any{"caller": nil}},
		{"empty caller object", map[string]any{"caller": map[string]any{}}},
		{"conflicting caller aid and agent_id", map[string]any{"caller": map[string]any{"aid": "a1", "agent_id": "a2", "sid": "s1"}}},
		{"conflicting caller sid and session_id", map[string]any{"caller": map[string]any{"aid": "a1", "sid": "s1", "session_id": "s2"}}},
		{"partial caller aid only", map[string]any{"caller": map[string]any{"aid": "a1"}}},
		{"partial caller sid only", map[string]any{"caller": map[string]any{"sid": "s1"}}},
		{"conflicting top-level agent_ids", map[string]any{"caller_agent_id": "a1", "agent_id": "a2", "caller_session_id": "s1"}},
		{"conflicting top-level session_ids", map[string]any{"caller_agent_id": "a1", "caller_session_id": "s1", "session_id": "s2"}},
		{"partial top-level agent_id only", map[string]any{"caller_agent_id": "a1"}},
		{"partial top-level session_id only", map[string]any{"caller_session_id": "s1"}},
		{"null caller_agent_id", map[string]any{"caller_agent_id": nil}},
		{"null agent_id", map[string]any{"agent_id": nil}},
		{"null caller_session_id", map[string]any{"caller_session_id": nil}},
		{"null session_id", map[string]any{"session_id": nil}},
		{"agent_id with whitespace", map[string]any{"caller_agent_id": "agent 1", "caller_session_id": "s1"}},
		{"agent_id leading whitespace (no trim)", map[string]any{"caller_agent_id": " agent-1", "caller_session_id": "s1"}},
		{"session_id trailing whitespace (no trim)", map[string]any{"caller_agent_id": "agent-1", "caller_session_id": "s1 "}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := svc.ResolveNamespace(ctx, tc.payload)
			if err == nil {
				t.Fatalf("expected error for case %q, got nil", tc.name)
			}
		})
	}
}

func TestResolveNamespaceExplicitNamespaceCannotBypassInvalidCaller(t *testing.T) {
	st := createTestStore(t)
	svc := NewService(st, nil)
	ctx := context.Background()

	// 1. Explicit global namespace MUST NOT bypass invalid caller!
	_, err := svc.ResolveNamespace(ctx, map[string]any{
		"namespace": "global",
		"caller": map[string]any{
			"aid":      "agent-1",
			"agent_id": "agent-2", // conflict!
			"sid":      "s-1",
		},
	})
	if err == nil {
		t.Fatalf("expected error when caller has conflict, even if explicit namespace=global is provided")
	}

	// 2. Explicit session_key MUST NOT bypass invalid caller!
	_, err = svc.ResolveNamespace(ctx, map[string]any{
		"session_key": "group::ops",
		"caller":      nil, // null caller error!
	})
	if err == nil {
		t.Fatalf("expected error when caller is null, even if explicit session_key is provided")
	}

	// 3. Valid caller with explicit namespace works fine
	ns, err := svc.ResolveNamespace(ctx, map[string]any{
		"namespace": "global",
		"caller": map[string]any{
			"aid": "agent-1",
			"sid": "s-1",
		},
	})
	if err != nil {
		t.Fatalf("unexpected error for valid caller with explicit namespace: %v", err)
	}
	if ns != "global" {
		t.Fatalf("expected global, got %s", ns)
	}

	// 4. Bare sid in session_key without caller agent cannot guess
	_, err = svc.ResolveNamespace(ctx, map[string]any{
		"session_key": "s-bare-id",
	})
	if err == nil {
		t.Fatalf("expected error for bare session_key without explicit agent_id, guessing must be rejected")
	}

	// 5. Bare sid in session: namespace without caller agent cannot guess
	_, err = svc.ResolveNamespace(ctx, map[string]any{
		"namespace": "session:s-bare-id",
	})
	if err == nil {
		t.Fatalf("expected error for bare session namespace without explicit agent_id, guessing must be rejected")
	}
}

func TestGetSessionScopeStoreErrorPropagation(t *testing.T) {
	st := createTestStore(t)
	svc := NewService(st, nil)
	ctx := context.Background()

	// Close store to force database errors
	_ = st.Close()

	_, err := svc.GetSessionScope(ctx, "agent-a", "s-a")
	if err == nil {
		t.Fatalf("expected error from GetSessionScope when store is closed, but got nil (error swallowed)")
	}
}
