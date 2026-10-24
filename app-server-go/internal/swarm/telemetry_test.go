package swarm

import (
	"context"
	"encoding/json"
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
		"status":     "idle",
		"updated_at": time.Now().UTC().Format(time.RFC3339Nano),
	})
}

func TestTelemetryReportRequiresWorkerIdentity(t *testing.T) {
	st := createTestStore(t)
	svc := NewService(st, nil, nil)
	ctx := context.Background()

	// Empty payload must fail: anonymous_worker forbidden
	_, err := svc.TelemetryReport(ctx, map[string]any{
		"progress": 50,
		"phase":    "testing",
	})
	if err == nil {
		t.Fatalf("expected TelemetryReport to fail without worker identity, got nil")
	}

	// Empty key must fail
	_, err = svc.TelemetryReport(ctx, map[string]any{
		"key":      "",
		"progress": 50,
	})
	if err == nil {
		t.Fatalf("expected TelemetryReport to fail with empty key, got nil")
	}
}

func TestTelemetryReportConflictValidation(t *testing.T) {
	st := createTestStore(t)
	svc := NewService(st, nil, nil)
	ctx := context.Background()

	// Conflict between key and explicit agent_id/session_id
	_, err := svc.TelemetryReport(ctx, map[string]any{
		"key":        "agent-a::session-1",
		"agent_id":   "agent-b",
		"session_id": "session-1",
	})
	if err == nil {
		t.Fatalf("expected conflict error between key and agent_id, got nil")
	}
}

func TestTelemetryReportDelegationRootResolutionAndGetFilter(t *testing.T) {
	st := createTestStore(t)
	svc := NewService(st, nil, nil)
	ctx := context.Background()

	// Seed sessions A, B, C
	seedSession(t, st, "agent-a", "s-a")
	seedSession(t, st, "agent-b", "s-b")
	seedSession(t, st, "agent-c", "s-c")

	// Set up delegation: A -> B -> C
	_ = st.CreateDelegationEdge(ctx, store.DelegationEdge{
		ParentAgentID:   "agent-a",
		ParentSessionID: "s-a",
		ChildAgentID:    "agent-b",
		ChildSessionID:  "s-b",
	})
	_ = st.CreateDelegationEdge(ctx, store.DelegationEdge{
		ParentAgentID:   "agent-b",
		ParentSessionID: "s-b",
		ChildAgentID:    "agent-c",
		ChildSessionID:  "s-c",
	})

	// 1. Report telemetry for node C
	res, err := svc.TelemetryReport(ctx, map[string]any{
		"agent_id":   "agent-c",
		"session_id": "s-c",
		"progress":   80,
		"phase":      "testing",
		"status":     "ok",
		"summary":    "Running integration suite",
	})
	if err != nil {
		t.Fatalf("TelemetryReport failed: %v", err)
	}
	report := res["report"].(map[string]any)
	if report["delegation_root_key"] != "agent-a::s-a" {
		t.Fatalf("expected delegation_root_key agent-a::s-a, got %v", report["delegation_root_key"])
	}

	// 2. Report telemetry for root A
	_, err = svc.TelemetryReport(ctx, map[string]any{
		"key":      "agent-a::s-a",
		"progress": 20,
		"phase":    "coordinating",
	})
	if err != nil {
		t.Fatalf("TelemetryReport root failed: %v", err)
	}

	// 3. TelemetryGet by key
	byKey, err := svc.TelemetryGet(ctx, map[string]any{"key": "agent-c::s-c"})
	if err != nil || byKey["exists"] != true {
		t.Fatalf("TelemetryGet by key failed: %v", err)
	}

	// 4. TelemetryGet filtered by root_key
	byRoot, err := svc.TelemetryGet(ctx, map[string]any{"root_key": "agent-a::s-a"})
	if err != nil {
		t.Fatalf("TelemetryGet by root_key failed: %v", err)
	}
	items := byRoot["items"].(map[string]any)
	if len(items) != 2 {
		t.Fatalf("expected 2 items for root agent-a::s-a, got %d", len(items))
	}
	if items["agent-c::s-c"] == nil || items["agent-a::s-a"] == nil {
		t.Fatalf("missing expected worker in root items: %+v", items)
	}

	// 5. TelemetryGet all items (UI full listing)
	all, err := svc.TelemetryGet(ctx, map[string]any{})
	if err != nil {
		t.Fatalf("TelemetryGet all failed: %v", err)
	}
	allItems := all["items"].(map[string]any)
	if len(allItems) < 2 {
		t.Fatalf("expected at least 2 items in full listing, got %d", len(allItems))
	}
}

func TestTelemetryReportMalformedAndNullRejections(t *testing.T) {
	st := createTestStore(t)
	svc := NewService(st, nil, nil)
	ctx := context.Background()

	cases := []struct {
		name    string
		payload map[string]any
	}{
		{"null key", map[string]any{"key": nil}},
		{"empty key", map[string]any{"key": ""}},
		{"key without separator", map[string]any{"key": "just-a-key"}},
		{"key with multiple separators", map[string]any{"key": "a::b::c"}},
		{"key with empty agent", map[string]any{"key": "::session-1"}},
		{"key with empty session", map[string]any{"key": "agent-1::"}},
		{"key with whitespace", map[string]any{"key": "agent-1:: session-1"}},
		{"null caller", map[string]any{"caller": nil}},
		{"empty caller object", map[string]any{"caller": map[string]any{}}},
		{"conflicting caller aid vs agent_id", map[string]any{"caller": map[string]any{"aid": "a1", "agent_id": "a2", "sid": "s1"}}},
		{"conflicting caller sid vs session_id", map[string]any{"caller": map[string]any{"aid": "a1", "sid": "s1", "session_id": "s2"}}},
		{"partial caller aid only", map[string]any{"caller": map[string]any{"aid": "a1"}}},
		{"partial caller sid only", map[string]any{"caller": map[string]any{"sid": "s1"}}},
		{"conflicting top-level agent_ids", map[string]any{"caller_agent_id": "a1", "agent_id": "a2", "caller_session_id": "s1"}},
		{"conflicting top-level session_ids", map[string]any{"caller_agent_id": "a1", "caller_session_id": "s1", "session_id": "s2"}},
		{"partial top-level agent_id only", map[string]any{"caller_agent_id": "a1"}},
		{"partial top-level session_id only", map[string]any{"caller_session_id": "s1"}},
		{"null top-level agent_id", map[string]any{"agent_id": nil}},
		{"null top-level session_id", map[string]any{"session_id": nil}},
		{"null caller_agent_id", map[string]any{"caller_agent_id": nil}},
		{"null caller_session_id", map[string]any{"caller_session_id": nil}},
		{"agent_id with whitespace", map[string]any{"agent_id": "agent 1", "session_id": "s1"}},
		{"session_id with leading whitespace (no trim)", map[string]any{"agent_id": "agent-1", "session_id": " s1"}},
		{"conflict between key and caller", map[string]any{
			"key":    "agent-1::session-1",
			"caller": map[string]any{"aid": "agent-2", "sid": "session-1"},
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := svc.TelemetryReport(ctx, tc.payload)
			if err == nil {
				t.Fatalf("expected error for case %q, got nil", tc.name)
			}
		})
	}
}

func TestTelemetryGetWhitespaceAndEmptyValidation(t *testing.T) {
	st := createTestStore(t)
	svc := NewService(st, nil, nil)
	ctx := context.Background()

	// 1. key with illegal whitespace
	_, err := svc.TelemetryGet(ctx, map[string]any{"key": "agent-1 ::s-1"})
	if err == nil {
		t.Fatalf("expected error for key with whitespace")
	}

	// 2. empty key
	_, err = svc.TelemetryGet(ctx, map[string]any{"key": ""})
	if err == nil {
		t.Fatalf("expected error for empty key")
	}

	// 3. root_key with whitespace
	_, err = svc.TelemetryGet(ctx, map[string]any{"root_key": "agent-1 ::s-1"})
	if err == nil {
		t.Fatalf("expected error for root_key with whitespace")
	}

	// 4. empty root_key
	_, err = svc.TelemetryGet(ctx, map[string]any{"root_key": ""})
	if err == nil {
		t.Fatalf("expected error for empty root_key")
	}
}

func TestTelemetryReportStoreErrorPropagation(t *testing.T) {
	st := createTestStore(t)
	svc := NewService(st, nil, nil)
	ctx := context.Background()

	// Close store to force database errors
	_ = st.Close()

	_, err := svc.TelemetryReport(ctx, map[string]any{
		"agent_id":   "agent-a",
		"session_id": "s-a",
		"progress":   50,
	})
	if err == nil {
		t.Fatalf("expected error from TelemetryReport when store is closed, but got nil (error swallowed)")
	}
}

func TestTelemetryReportJsonNumberProgressAndClamp(t *testing.T) {
	st := createTestStore(t)
	svc := NewService(st, nil, nil)
	ctx := context.Background()

	// 1. json.Number("100") must be saved as 100 and read back via TelemetryGet as exactly 100
	repRes, err := svc.TelemetryReport(ctx, map[string]any{
		"key":      "agent-test::session-json-num",
		"progress": json.Number("100"),
	})
	if err != nil {
		t.Fatalf("TelemetryReport failed: %v", err)
	}
	rep, ok := repRes["report"].(map[string]any)
	if !ok {
		t.Fatalf("expected report in response, got %+v", repRes)
	}
	if rep["progress"] != 100 {
		t.Fatalf("expected report progress to be 100, got %v (%T)", rep["progress"], rep["progress"])
	}

	getRes, err := svc.TelemetryGet(ctx, map[string]any{
		"key": "agent-test::session-json-num",
	})
	if err != nil {
		t.Fatalf("TelemetryGet failed: %v", err)
	}
	if getRes["exists"] != true {
		t.Fatalf("expected report to exist")
	}
	readRep, ok := getRes["report"].(map[string]any)
	if !ok {
		t.Fatalf("expected report in get response, got %+v", getRes)
	}
	var readProgress int
	switch p := readRep["progress"].(type) {
	case int:
		readProgress = p
	case int64:
		readProgress = int(p)
	case json.Number:
		i, _ := p.Int64()
		readProgress = int(i)
	default:
		t.Fatalf("unexpected type for read back progress: %v (%T)", readRep["progress"], readRep["progress"])
	}
	if readProgress != 100 {
		t.Fatalf("expected read back progress to be 100, got %d", readProgress)
	}

	// 2. Test clamp 0..100 and various input types (json.Number, float, string, int)
	clampCases := []struct {
		name     string
		input    any
		expected int
	}{
		{"json.Number 0", json.Number("0"), 0},
		{"json.Number 50", json.Number("50"), 50},
		{"json.Number 100", json.Number("100"), 100},
		{"json.Number negative clamp", json.Number("-10"), 0},
		{"json.Number overflow clamp", json.Number("150"), 100},
		{"json.Number float string", json.Number("42.9"), 42},
		{"json.Number invalid", json.Number("not-a-number"), 0},
		{"int within range", 75, 75},
		{"int64 within range", int64(80), 80},
		{"float64 within range", 65.5, 65},
		{"float64 overflow clamp", 120.5, 100},
		{"float64 negative clamp", -5.5, 0},
		{"string valid int", "90", 90},
		{"string negative clamp", "-20", 0},
		{"string overflow clamp", "200", 100},
		{"string invalid fallback", "invalid", 0},
		{"nil fallback", nil, 0},
	}

	for _, tc := range clampCases {
		t.Run(tc.name, func(t *testing.T) {
			res, err := svc.TelemetryReport(ctx, map[string]any{
				"key":      "agent-test::session-clamp",
				"progress": tc.input,
			})
			if err != nil {
				t.Fatalf("TelemetryReport failed: %v", err)
			}
			r := res["report"].(map[string]any)
			if r["progress"] != tc.expected {
				t.Fatalf("for input %v (%T), expected progress %d, got %v (%T)", tc.input, tc.input, tc.expected, r["progress"], r["progress"])
			}
		})
	}
}
