package store

import (
	"context"
	"database/sql"
	"testing"
	"time"
)

func TestDelegationEvents_StatusActivityPreservesEdge(t *testing.T) {
	st := createTestStore(t)
	ctx := context.Background()

	// Seed sessions
	seedSession(t, st, "parent-agent", "parent-sid")
	seedSession(t, st, "child-agent", "child-sid")

	// Insert delegation edge
	_, err := st.db.ExecContext(ctx, `INSERT INTO delegation_edges(child_agent_id, child_session_id, parent_agent_id, parent_session_id, created_at)
VALUES(?,?,?,?,?)`, "child-agent", "child-sid", "parent-agent", "parent-sid", time.Now().UTC())
	if err != nil {
		t.Fatalf("insert edge: %v", err)
	}

	// 1. AppendEvent session.upsert with status & activity timestamp
	evData := map[string]any{
		"id":         "child-sid",
		"agent_id":   "child-agent",
		"status":     "running",
		"title":      "Child Mission",
		"updated_at": "2026-10-19T10:00:00.000Z",
	}
	ev := Event{
		ID:        "ev-status-1",
		Type:      "session.upsert",
		AgentID:   "child-agent",
		SessionID: "child-sid",
		Data:      evData,
	}

	inserted, err := st.AppendEvent(ctx, ev)
	if err != nil {
		t.Fatalf("AppendEvent failed: %v", err)
	}
	if inserted == nil {
		t.Fatal("expected inserted event, got nil")
	}

	// Verify delegation parent preserved
	if inserted.Data["delegation_parent_agent_id"] != "parent-agent" {
		t.Fatalf("delegation_parent_agent_id = %v, want 'parent-agent'", inserted.Data["delegation_parent_agent_id"])
	}
	if inserted.Data["delegation_parent_session_id"] != "parent-sid" {
		t.Fatalf("delegation_parent_session_id = %v, want 'parent-sid'", inserted.Data["delegation_parent_session_id"])
	}
	// Verify other fields intact
	if inserted.Data["status"] != "running" {
		t.Fatalf("status = %v, want 'running'", inserted.Data["status"])
	}
	if inserted.Data["title"] != "Child Mission" {
		t.Fatalf("title = %v, want 'Child Mission'", inserted.Data["title"])
	}
	if inserted.Data["updated_at"] != "2026-10-19T10:00:00.000Z" {
		t.Fatalf("updated_at = %v, want '2026-10-19T10:00:00.000Z'", inserted.Data["updated_at"])
	}

	// 2. Also verify through ApplyFrame (daemon checkpoint transaction)
	projector, err := st.RegisterProjector("test-status-proj", func(tx *sql.Tx) ([]Event, error) {
		return []Event{
			{
				ID:        "ev-frame-1",
				Type:      "session.upsert",
				AgentID:   "child-agent",
				SessionID: "child-sid",
				Data: map[string]any{
					"id":         "child-sid",
					"agent_id":   "child-agent",
					"status":     "idle",
					"updated_at": "2026-10-19T10:05:00.000Z",
				},
			},
		}, nil
	})
	if err != nil {
		t.Fatalf("RegisterProjector failed: %v", err)
	}

	frameEvents, err := st.ApplyFrame(ctx, "epoch-1", "stream-1", 1, projector)
	if err != nil {
		t.Fatalf("ApplyFrame failed: %v", err)
	}
	if len(frameEvents) != 1 {
		t.Fatalf("ApplyFrame returned %d events, want 1", len(frameEvents))
	}
	if frameEvents[0].Data["delegation_parent_agent_id"] != "parent-agent" {
		t.Fatalf("ApplyFrame delegation_parent_agent_id = %v, want 'parent-agent'", frameEvents[0].Data["delegation_parent_agent_id"])
	}
	if frameEvents[0].Data["delegation_parent_session_id"] != "parent-sid" {
		t.Fatalf("ApplyFrame delegation_parent_session_id = %v, want 'parent-sid'", frameEvents[0].Data["delegation_parent_session_id"])
	}
	if frameEvents[0].Data["status"] != "idle" {
		t.Fatalf("ApplyFrame status = %v, want 'idle'", frameEvents[0].Data["status"])
	}
}

func TestDelegationEvents_NoEdgeClearsForgedFields(t *testing.T) {
	st := createTestStore(t)
	ctx := context.Background()

	seedSession(t, st, "child-agent", "child-sid")
	// Ensure NO edge exists in delegation_edges

	evData := map[string]any{
		"id":                           "child-sid",
		"agent_id":                     "child-agent",
		"status":                       "active",
		"delegation_parent_agent_id":   "forged-attacker-parent",
		"delegation_parent_session_id": "forged-attacker-sid",
	}
	ev := Event{
		ID:        "ev-forged-1",
		Type:      "session.upsert",
		AgentID:   "child-agent",
		SessionID: "child-sid",
		Data:      evData,
	}

	inserted, err := st.AppendEvent(ctx, ev)
	if err != nil {
		t.Fatalf("AppendEvent failed: %v", err)
	}
	if inserted == nil {
		t.Fatal("expected inserted event, got nil")
	}

	// Must be explicitly nil, not the forged attacker values
	if inserted.Data["delegation_parent_agent_id"] != nil {
		t.Fatalf("delegation_parent_agent_id = %v, want nil", inserted.Data["delegation_parent_agent_id"])
	}
	if inserted.Data["delegation_parent_session_id"] != nil {
		t.Fatalf("delegation_parent_session_id = %v, want nil", inserted.Data["delegation_parent_session_id"])
	}

	// Verify persisted event in database via ReplayEvents
	resync, events, err := st.ReplayEvents(ctx, 0)
	if err != nil {
		t.Fatalf("ReplayEvents: %v", err)
	}
	if resync {
		t.Fatal("unexpected resync")
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].Data["delegation_parent_agent_id"] != nil {
		t.Fatalf("persisted delegation_parent_agent_id = %v, want nil", events[0].Data["delegation_parent_agent_id"])
	}
	if events[0].Data["delegation_parent_session_id"] != nil {
		t.Fatalf("persisted delegation_parent_session_id = %v, want nil", events[0].Data["delegation_parent_session_id"])
	}
}

func TestDelegationEvents_CrossAgentSameSessionID(t *testing.T) {
	st := createTestStore(t)
	ctx := context.Background()

	// Both agents have session with id "common-sid"
	seedSession(t, st, "agent-alpha", "common-sid")
	seedSession(t, st, "agent-beta", "common-sid")
	seedSession(t, st, "agent-gamma", "common-sid")

	// agent-alpha has delegation edge to parent-alpha
	_, err := st.db.ExecContext(ctx, `INSERT INTO delegation_edges(child_agent_id, child_session_id, parent_agent_id, parent_session_id, created_at)
VALUES(?,?,?,?,?)`, "agent-alpha", "common-sid", "parent-alpha", "parent-sid-alpha", time.Now().UTC())
	if err != nil {
		t.Fatalf("insert edge alpha: %v", err)
	}

	// agent-beta has delegation edge to parent-beta
	_, err = st.db.ExecContext(ctx, `INSERT INTO delegation_edges(child_agent_id, child_session_id, parent_agent_id, parent_session_id, created_at)
VALUES(?,?,?,?,?)`, "agent-beta", "common-sid", "parent-beta", "parent-sid-beta", time.Now().UTC())
	if err != nil {
		t.Fatalf("insert edge beta: %v", err)
	}

	// agent-gamma has NO edge

	// 1. Append for alpha
	evAlpha, err := st.AppendEvent(ctx, Event{
		ID:        "ev-alpha",
		Type:      "session.upsert",
		AgentID:   "agent-alpha",
		SessionID: "common-sid",
		Data:      map[string]any{"id": "common-sid", "agent_id": "agent-alpha"},
	})
	if err != nil {
		t.Fatalf("AppendEvent alpha: %v", err)
	}
	if evAlpha.Data["delegation_parent_agent_id"] != "parent-alpha" || evAlpha.Data["delegation_parent_session_id"] != "parent-sid-alpha" {
		t.Fatalf("alpha parent mismatch: got agent=%v sid=%v", evAlpha.Data["delegation_parent_agent_id"], evAlpha.Data["delegation_parent_session_id"])
	}

	// 2. Append for beta
	evBeta, err := st.AppendEvent(ctx, Event{
		ID:        "ev-beta",
		Type:      "session.upsert",
		AgentID:   "agent-beta",
		SessionID: "common-sid",
		Data:      map[string]any{"id": "common-sid", "agent_id": "agent-beta"},
	})
	if err != nil {
		t.Fatalf("AppendEvent beta: %v", err)
	}
	if evBeta.Data["delegation_parent_agent_id"] != "parent-beta" || evBeta.Data["delegation_parent_session_id"] != "parent-sid-beta" {
		t.Fatalf("beta parent mismatch: got agent=%v sid=%v", evBeta.Data["delegation_parent_agent_id"], evBeta.Data["delegation_parent_session_id"])
	}

	// 3. Append for gamma (no edge)
	evGamma, err := st.AppendEvent(ctx, Event{
		ID:        "ev-gamma",
		Type:      "session.upsert",
		AgentID:   "agent-gamma",
		SessionID: "common-sid",
		Data:      map[string]any{"id": "common-sid", "agent_id": "agent-gamma"},
	})
	if err != nil {
		t.Fatalf("AppendEvent gamma: %v", err)
	}
	if evGamma.Data["delegation_parent_agent_id"] != nil || evGamma.Data["delegation_parent_session_id"] != nil {
		t.Fatalf("gamma parent mismatch: got agent=%v sid=%v, want nil", evGamma.Data["delegation_parent_agent_id"], evGamma.Data["delegation_parent_session_id"])
	}
}

func TestDelegationEvents_DBErrorNotSwallowed_RollsBack(t *testing.T) {
	st := createTestStore(t)
	ctx := context.Background()

	seedSession(t, st, "agent-1", "session-1")

	// Drop delegation_edges to simulate a DB table failure / corrupt schema
	if _, err := st.db.ExecContext(ctx, "DROP TABLE delegation_edges"); err != nil {
		t.Fatalf("drop delegation_edges: %v", err)
	}

	// 1. AppendEvent must return error, not swallow it
	_, err := st.AppendEvent(ctx, Event{
		ID:        "ev-dberr-1",
		Type:      "session.upsert",
		AgentID:   "agent-1",
		SessionID: "session-1",
		Data:      map[string]any{"id": "session-1", "agent_id": "agent-1"},
	})
	if err == nil {
		t.Fatal("expected error on missing delegation_edges table, got nil")
	}

	// Verify events table remains empty (transaction rolled back)
	var evCount int
	if err := st.db.QueryRowContext(ctx, "SELECT count(*) FROM events").Scan(&evCount); err != nil {
		t.Fatalf("count events: %v", err)
	}
	if evCount != 0 {
		t.Fatalf("events count = %d, want 0 after rollback", evCount)
	}

	// 2. ApplyFrame must also return error and rollback checkpoint
	projector, err := st.RegisterProjector("test-dberr-proj", func(tx *sql.Tx) ([]Event, error) {
		return []Event{
			{
				ID:        "ev-frame-dberr",
				Type:      "session.upsert",
				AgentID:   "agent-1",
				SessionID: "session-1",
				Data:      map[string]any{"id": "session-1", "agent_id": "agent-1"},
			},
		}, nil
	})
	if err != nil {
		t.Fatalf("RegisterProjector failed: %v", err)
	}

	_, frameErr := st.ApplyFrame(ctx, "epoch-err", "stream-err", 1, projector)
	if frameErr == nil {
		t.Fatal("expected ApplyFrame error on missing delegation_edges, got nil")
	}

	// Verify checkpoint was NOT advanced
	cp, err := st.GetCheckpoint(ctx, "epoch-err", "stream-err")
	if err != nil {
		t.Fatalf("GetCheckpoint: %v", err)
	}
	if cp != 0 {
		t.Fatalf("checkpoint seq = %d, want 0 after rollback", cp)
	}
}

func TestDelegationEvents_SourceMapNotModified(t *testing.T) {
	st := createTestStore(t)
	ctx := context.Background()

	seedSession(t, st, "agent-src", "session-src")

	origMap := map[string]any{
		"id":                           "session-src",
		"agent_id":                     "agent-src",
		"status":                       "idle",
		"delegation_parent_agent_id":   "forged-parent",
		"delegation_parent_session_id": "forged-sid",
	}

	ev := Event{
		ID:        "ev-src-1",
		Type:      "session.upsert",
		AgentID:   "agent-src",
		SessionID: "session-src",
		Data:      origMap,
	}

	inserted, err := st.AppendEvent(ctx, ev)
	if err != nil {
		t.Fatalf("AppendEvent failed: %v", err)
	}

	// Verify returned event has cleared forged fields (since no edge exists)
	if inserted.Data["delegation_parent_agent_id"] != nil {
		t.Fatalf("inserted.Data has parent %v, want nil", inserted.Data["delegation_parent_agent_id"])
	}

	// Verify source map origMap was NOT mutated in-place
	if origMap["delegation_parent_agent_id"] != "forged-parent" {
		t.Fatalf("origMap mutated: delegation_parent_agent_id = %v, want 'forged-parent'", origMap["delegation_parent_agent_id"])
	}
	if origMap["delegation_parent_session_id"] != "forged-sid" {
		t.Fatalf("origMap mutated: delegation_parent_session_id = %v, want 'forged-sid'", origMap["delegation_parent_session_id"])
	}
}

func TestDelegationEvents_IdentityResolutionAndConflictRejection(t *testing.T) {
	st := createTestStore(t)
	ctx := context.Background()

	seedSession(t, st, "parent-agent", "parent-sid")
	seedSession(t, st, "child-agent", "child-sid")

	_, err := st.db.ExecContext(ctx, `INSERT INTO delegation_edges(child_agent_id, child_session_id, parent_agent_id, parent_session_id, created_at)
VALUES(?,?,?,?,?)`, "child-agent", "child-sid", "parent-agent", "parent-sid", time.Now().UTC())
	if err != nil {
		t.Fatalf("insert edge: %v", err)
	}

	// 1. App Publish style: SessionID is empty on event, but present in Data["id"]
	evAppPublish := Event{
		ID:        "ev-app-pub-1",
		Type:      "session.upsert",
		AgentID:   "child-agent",
		SessionID: "", // empty on Event struct
		Data: map[string]any{
			"id":       "child-sid",
			"agent_id": "child-agent",
			"status":   "active",
		},
	}
	inserted, err := st.AppendEvent(ctx, evAppPublish)
	if err != nil {
		t.Fatalf("AppendEvent App Publish style failed: %v", err)
	}
	if inserted.SessionID != "child-sid" {
		t.Fatalf("inserted.SessionID = %q, want 'child-sid'", inserted.SessionID)
	}
	if inserted.Data["delegation_parent_agent_id"] != "parent-agent" {
		t.Fatalf("delegation_parent_agent_id = %v, want 'parent-agent'", inserted.Data["delegation_parent_agent_id"])
	}

	// 2. Session ID conflict: event.SessionID != Data["id"]
	_, err = st.AppendEvent(ctx, Event{
		ID:        "ev-conflict-sid",
		Type:      "session.upsert",
		AgentID:   "child-agent",
		SessionID: "child-sid",
		Data: map[string]any{
			"id":       "different-sid",
			"agent_id": "child-agent",
		},
	})
	if err == nil {
		t.Fatal("expected error on SessionID mismatch, got nil")
	}

	// 3. Agent ID conflict: event.AgentID != Data["agent_id"]
	_, err = st.AppendEvent(ctx, Event{
		ID:        "ev-conflict-aid",
		Type:      "session.upsert",
		AgentID:   "child-agent",
		SessionID: "child-sid",
		Data: map[string]any{
			"id":       "child-sid",
			"agent_id": "other-agent",
		},
	})
	if err == nil {
		t.Fatal("expected error on AgentID mismatch, got nil")
	}

	// 4. Missing SessionID completely
	_, err = st.AppendEvent(ctx, Event{
		ID:        "ev-missing-sid",
		Type:      "session.upsert",
		AgentID:   "child-agent",
		SessionID: "",
		Data: map[string]any{
			"agent_id": "child-agent",
			"status":   "active",
		},
	})
	if err == nil {
		t.Fatal("expected error on missing SessionID, got nil")
	}

	// 5. Missing AgentID completely
	_, err = st.AppendEvent(ctx, Event{
		ID:        "ev-missing-aid",
		Type:      "session.upsert",
		AgentID:   "",
		SessionID: "child-sid",
		Data: map[string]any{
			"id":     "child-sid",
			"status": "active",
		},
	})
	if err == nil {
		t.Fatal("expected error on missing AgentID, got nil")
	}
}

func TestDelegationEvents_IdentityIsNeverRepaired(t *testing.T) {
	for _, field := range []string{"id", "session_id", "agent_id"} {
		for _, value := range []any{" child-sid ", 123} {
			event := Event{Type: "session.upsert", AgentID: "child-agent", SessionID: "child-sid", Data: map[string]any{field: value}}
			if err := resolveSessionUpsertIdentity(&event); err == nil {
				t.Fatalf("accepted conflicting or non-string %s=%v", field, value)
			}
		}
	}
	event := Event{Type: "session.upsert", Data: map[string]any{"id": " native-id ", "agent_id": "agent"}}
	if err := resolveSessionUpsertIdentity(&event); err != nil {
		t.Fatal(err)
	}
	if event.SessionID != " native-id " {
		t.Fatalf("native identity was repaired: %q", event.SessionID)
	}
}

func TestDelegationEvents_NonSessionEventsUnaffected(t *testing.T) {
	st := createTestStore(t)
	ctx := context.Background()

	// Non-session event with custom data
	evMsg := Event{
		ID:   "ev-msg-1",
		Type: "message.upsert",
		Data: map[string]any{
			"text":                         "hello world",
			"delegation_parent_agent_id":   "should-not-be-touched",
			"delegation_parent_session_id": "should-not-be-touched-sid",
		},
	}
	inserted, err := st.AppendEvent(ctx, evMsg)
	if err != nil {
		t.Fatalf("AppendEvent message.upsert failed: %v", err)
	}
	if inserted.Data["delegation_parent_agent_id"] != "should-not-be-touched" {
		t.Fatalf("non-session event data was modified: %v", inserted.Data["delegation_parent_agent_id"])
	}
}
