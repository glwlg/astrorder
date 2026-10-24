package store

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func createTestStore(t *testing.T) *Store {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	st, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func seedSession(t *testing.T, st *Store, agentID, sessionID string) {
	t.Helper()
	ctx := context.Background()
	_, err := st.UpsertAgent(ctx, map[string]any{
		"id":           agentID,
		"kind":         "hermes",
		"name":         agentID,
		"status":       "ready",
		"capabilities": []string{"chat"},
	})
	if err != nil {
		t.Fatalf("seed agent %s: %v", agentID, err)
	}
	_, err = st.UpsertSession(ctx, map[string]any{
		"id":         sessionID,
		"agent_id":   agentID,
		"title":      "Session " + sessionID,
		"status":     "idle",
		"updated_at": time.Now().UTC().Format(time.RFC3339Nano),
	})
	if err != nil {
		t.Fatalf("seed session %s::%s: %v", agentID, sessionID, err)
	}
}

func TestDelegationEdgeLifecycle(t *testing.T) {
	st := createTestStore(t)
	ctx := context.Background()

	// Seed sessions A, B, C
	seedSession(t, st, "agent-a", "s-a")
	seedSession(t, st, "agent-b", "s-b")
	seedSession(t, st, "agent-c", "s-c")

	// 1. Normal delegation: A -> B
	edge := DelegationEdge{
		ParentAgentID:   "agent-a",
		ParentSessionID: "s-a",
		ChildAgentID:    "agent-b",
		ChildSessionID:  "s-b",
	}
	err := st.CreateDelegationEdge(ctx, edge)
	if err != nil {
		t.Fatalf("CreateDelegationEdge A->B failed: %v", err)
	}

	// 2. Query parent of B
	parent, found, err := st.GetDelegationParent(ctx, "agent-b", "s-b")
	if err != nil || !found {
		t.Fatalf("GetDelegationParent(s-b) found=%v err=%v", found, err)
	}
	if parent.ParentAgentID != "agent-a" || parent.ParentSessionID != "s-a" {
		t.Fatalf("unexpected parent: %+v", parent)
	}

	// 3. Query children of A
	children, err := st.ListDelegationChildren(ctx, "agent-a", "s-a")
	if err != nil {
		t.Fatalf("ListDelegationChildren(s-a) failed: %v", err)
	}
	if len(children) != 1 || children[0].ChildSessionID != "s-b" {
		t.Fatalf("unexpected children of s-a: %+v", children)
	}

	// 4. Normal delegation: B -> C
	err = st.CreateDelegationEdge(ctx, DelegationEdge{
		ParentAgentID:   "agent-b",
		ParentSessionID: "s-b",
		ChildAgentID:    "agent-c",
		ChildSessionID:  "s-c",
	})
	if err != nil {
		t.Fatalf("CreateDelegationEdge B->C failed: %v", err)
	}

	// 5. Multi-tier resolution: root of C should be A
	root, err := st.ResolveDelegationRoot(ctx, "agent-c", "s-c")
	if err != nil {
		t.Fatalf("ResolveDelegationRoot(s-c) failed: %v", err)
	}
	if root.AgentID != "agent-a" || root.SessionID != "s-a" {
		t.Fatalf("unexpected root for s-c: %+v, want agent-a::s-a", root)
	}

	// 6. Multi-tier resolution: root of A (root itself) should be A
	rootA, err := st.ResolveDelegationRoot(ctx, "agent-a", "s-a")
	if err != nil {
		t.Fatalf("ResolveDelegationRoot(s-a) failed: %v", err)
	}
	if rootA.AgentID != "agent-a" || rootA.SessionID != "s-a" {
		t.Fatalf("unexpected root for s-a: %+v", rootA)
	}

	// 7. Descendants of A should include B and C
	descendants, err := st.ListDelegationDescendants(ctx, "agent-a", "s-a")
	if err != nil {
		t.Fatalf("ListDelegationDescendants(s-a) failed: %v", err)
	}
	if len(descendants) != 2 {
		t.Fatalf("expected 2 descendants, got %d: %+v", len(descendants), descendants)
	}

	// 8. Reject self delegation: C -> C
	err = st.CreateDelegationEdge(ctx, DelegationEdge{
		ParentAgentID:   "agent-c",
		ParentSessionID: "s-c",
		ChildAgentID:    "agent-c",
		ChildSessionID:  "s-c",
	})
	if err == nil {
		t.Fatalf("expected self-delegation to fail, got nil")
	}

	// 9. Reject cycle: C -> A (since A -> B -> C already exists)
	err = st.CreateDelegationEdge(ctx, DelegationEdge{
		ParentAgentID:   "agent-c",
		ParentSessionID: "s-c",
		ChildAgentID:    "agent-a",
		ChildSessionID:  "s-a",
	})
	if err == nil {
		t.Fatalf("expected cycle delegation C->A to fail, got nil")
	}

	// 10. Reject multiple parents for same child: try A -> C (C already has parent B)
	err = st.CreateDelegationEdge(ctx, DelegationEdge{
		ParentAgentID:   "agent-a",
		ParentSessionID: "s-a",
		ChildAgentID:    "agent-c",
		ChildSessionID:  "s-c",
	})
	if err == nil {
		t.Fatalf("expected duplicate parent for child C to fail, got nil")
	}

	// 11. Delete edge
	err = st.DeleteDelegationEdge(ctx, "agent-c", "s-c")
	if err != nil {
		t.Fatalf("DeleteDelegationEdge failed: %v", err)
	}
	_, found, err = st.GetDelegationParent(ctx, "agent-c", "s-c")
	if err != nil || found {
		t.Fatalf("expected parent not found after delete, found=%v err=%v", found, err)
	}
}

func TestDeleteSessionCleansDelegationEdges(t *testing.T) {
	st := createTestStore(t)
	ctx := context.Background()

	seedSession(t, st, "agent-x", "s-x")
	seedSession(t, st, "agent-y", "s-y")

	err := st.CreateDelegationEdge(ctx, DelegationEdge{
		ParentAgentID:   "agent-x",
		ParentSessionID: "s-x",
		ChildAgentID:    "agent-y",
		ChildSessionID:  "s-y",
	})
	if err != nil {
		t.Fatalf("CreateDelegationEdge failed: %v", err)
	}

	// Delete parent session x
	deleted, err := st.DeleteSession(ctx, "agent-x", "s-x")
	if err != nil || !deleted {
		t.Fatalf("DeleteSession failed: %v", err)
	}

	// Edge should be cleaned up
	_, found, err := st.GetDelegationParent(ctx, "agent-y", "s-y")
	if err != nil || found {
		t.Fatalf("expected edge to be removed when parent session is deleted, found=%v", found)
	}
}

func TestSessionWirePopulatesDelegationFieldsWithoutHandoffFallback(t *testing.T) {
	st := createTestStore(t)
	ctx := context.Background()

	// Seed session parent and child
	seedSession(t, st, "agent-p", "s-p")

	// Create child session with handoff_from (e.g. from fork or summary)
	now := time.Now().UTC().Format(time.RFC3339Nano)
	childData := map[string]any{
		"id":                      "s-child",
		"agent_id":                "agent-c",
		"title":                   "Forked Session",
		"status":                  "idle",
		"handoff_from_agent_id":   "agent-p",
		"handoff_from_session_id": "s-p",
		"updated_at":              now,
	}
	_, err := st.UpsertAgent(ctx, map[string]any{"id": "agent-c", "kind": "hermes", "name": "c", "status": "ready"})
	if err != nil {
		t.Fatalf("upsert agent: %v", err)
	}
	upserted, err := st.UpsertSession(ctx, childData)
	if err != nil {
		t.Fatalf("upsert child session: %v", err)
	}

	// Without delegation edge, delegation_parent_* MUST be nil, despite handoff_from being set
	if upserted["delegation_parent_agent_id"] != nil || upserted["delegation_parent_session_id"] != nil {
		t.Fatalf("expected delegation_parent to be nil without edge, got agent=%v session=%v", upserted["delegation_parent_agent_id"], upserted["delegation_parent_session_id"])
	}

	// GetSession also returns nil
	got, err := st.GetSession(ctx, "agent-c", "s-child")
	if err != nil || got == nil {
		t.Fatalf("GetSession failed: %v", err)
	}
	if got["delegation_parent_agent_id"] != nil || got["delegation_parent_session_id"] != nil {
		t.Fatalf("GetSession expected nil delegation_parent, got %v::%v", got["delegation_parent_agent_id"], got["delegation_parent_session_id"])
	}

	// Now add an explicit delegation edge: agent-p::s-p -> agent-c::s-child
	err = st.CreateDelegationEdge(ctx, DelegationEdge{
		ParentAgentID:   "agent-p",
		ParentSessionID: "s-p",
		ChildAgentID:    "agent-c",
		ChildSessionID:  "s-child",
	})
	if err != nil {
		t.Fatalf("CreateDelegationEdge failed: %v", err)
	}

	// Now GetSession returns explicit delegation fields!
	gotWithEdge, err := st.GetSession(ctx, "agent-c", "s-child")
	if err != nil || gotWithEdge == nil {
		t.Fatalf("GetSession with edge failed: %v", err)
	}
	if gotWithEdge["delegation_parent_agent_id"] != "agent-p" || gotWithEdge["delegation_parent_session_id"] != "s-p" {
		t.Fatalf("GetSession expected delegation_parent agent-p::s-p, got %v::%v", gotWithEdge["delegation_parent_agent_id"], gotWithEdge["delegation_parent_session_id"])
	}

	// ListSessions also populates delegation fields
	list, err := st.ListSessions(ctx, "agent-c")
	if err != nil || len(list) == 0 {
		t.Fatalf("ListSessions failed: %v", err)
	}
	if list[0]["delegation_parent_agent_id"] != "agent-p" || list[0]["delegation_parent_session_id"] != "s-p" {
		t.Fatalf("ListSessions expected delegation_parent agent-p::s-p, got %v::%v", list[0]["delegation_parent_agent_id"], list[0]["delegation_parent_session_id"])
	}
}

func TestDelegationOperationLifecycleAndIdempotency(t *testing.T) {
	st := createTestStore(t)
	ctx := context.Background()

	opID := "op-test-100"
	callerAid := "agent-caller"
	callerSid := "session-caller"
	targetAid := "agent-target"
	reqHash := "hash-payload-abc-123"

	// 1. Reserve operation
	op := DelegationOperation{
		OperationID:     opID,
		OperationType:   "create_session",
		CallerAgentID:   callerAid,
		CallerSessionID: callerSid,
		TargetAgentID:   targetAid,
		RequestHash:     reqHash,
		Stage:           OpStageReserved,
		RequestPayload:  map[string]any{"title": "Subtask", "model": "test-model"},
	}
	reserved, winner, err := st.ReserveDelegationOperation(ctx, op)
	if err != nil {
		t.Fatalf("ReserveDelegationOperation failed: %v", err)
	}
	if !winner {
		t.Fatalf("expected winner=true on first reserve")
	}
	if reserved.Stage != OpStageReserved || reserved.OperationID != opID {
		t.Fatalf("unexpected reserved op: %+v", reserved)
	}

	// 2. Duplicate reserve with SAME request hash returns existing (winner=false)
	dup, winner, err := st.ReserveDelegationOperation(ctx, op)
	if err != nil {
		t.Fatalf("duplicate ReserveDelegationOperation failed: %v", err)
	}
	if winner {
		t.Fatalf("expected winner=false on duplicate reserve")
	}
	if dup.OperationID != opID || dup.Stage != OpStageReserved {
		t.Fatalf("unexpected duplicate return: %+v", dup)
	}

	// 3. Duplicate reserve with DIFFERENT request hash fails with conflict
	opDifferentPayload := op
	opDifferentPayload.RequestHash = "different-hash-456"
	_, _, err = st.ReserveDelegationOperation(ctx, opDifferentPayload)
	if err == nil {
		t.Fatalf("expected conflict on different request hash with same opID, got nil")
	}

	// 4. Duplicate reserve with DIFFERENT caller fails with conflict
	opDifferentCaller := op
	opDifferentCaller.CallerSessionID = "other-caller-session"
	_, _, err = st.ReserveDelegationOperation(ctx, opDifferentCaller)
	if err == nil {
		t.Fatalf("expected conflict on different caller with same opID, got nil")
	}

	// 5. CAS transition: reserved -> creating
	err = st.TransitionDelegationOperation(ctx, opID, OpStageReserved, OpStageCreating, nil)
	if err != nil {
		t.Fatalf("transition reserved->creating failed: %v", err)
	}

	// 6. Concurrent / invalid CAS transition: try reserved -> created (should fail because current is creating)
	err = st.TransitionDelegationOperation(ctx, opID, OpStageReserved, OpStageCreated, nil)
	if err == nil {
		t.Fatalf("expected CAS mismatch error when transitioning from wrong expected stage, got nil")
	}

	// 7. CAS transition: creating -> created (with target_session_id)
	targetSid := "session-child-200"
	err = st.TransitionDelegationOperation(ctx, opID, OpStageCreating, OpStageCreated, &DelegationOperationUpdate{
		TargetSessionID: &targetSid,
	})
	if err != nil {
		t.Fatalf("transition creating->created failed: %v", err)
	}

	// 8. CAS transition: created -> sending
	err = st.TransitionDelegationOperation(ctx, opID, OpStageCreated, OpStageSending, nil)
	if err != nil {
		t.Fatalf("transition created->sending failed: %v", err)
	}

	// 9. CAS transition: sending -> completed (with result payload)
	resultPayload := map[string]any{"session_id": targetSid, "status": "idle", "sent_command_id": "cmd-1"}
	err = st.TransitionDelegationOperation(ctx, opID, OpStageSending, OpStageCompleted, &DelegationOperationUpdate{
		ResultPayload: resultPayload,
	})
	if err != nil {
		t.Fatalf("transition sending->completed failed: %v", err)
	}

	// 10. Verify final state
	finalOp, found, err := st.GetDelegationOperation(ctx, opID)
	if err != nil || !found {
		t.Fatalf("GetDelegationOperation failed: found=%v err=%v", found, err)
	}
	if finalOp.Stage != OpStageCompleted {
		t.Fatalf("expected stage completed, got %s", finalOp.Stage)
	}
	if finalOp.TargetSessionID != targetSid {
		t.Fatalf("expected target_session_id %s, got %s", targetSid, finalOp.TargetSessionID)
	}
	if finalOp.ResultPayload["status"] != "idle" {
		t.Fatalf("unexpected result payload: %+v", finalOp.ResultPayload)
	}
}

func TestDelegationOperationConcurrentReserveSingleWinner(t *testing.T) {
	st := createTestStore(t)
	ctx := context.Background()

	opID := "op-concurrent-1"
	op := DelegationOperation{
		OperationID:     opID,
		OperationType:   "create_session",
		CallerAgentID:   "agent-a",
		CallerSessionID: "s-a",
		TargetAgentID:   "agent-b",
		RequestHash:     "hash-concurrent-test",
		Stage:           OpStageReserved,
		RequestPayload:  map[string]any{"hello": "world"},
	}

	const concurrency = 10
	var wg sync.WaitGroup
	winners := make([]bool, concurrency)
	errors := make([]error, concurrency)

	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			_, winner, err := st.ReserveDelegationOperation(ctx, op)
			winners[idx] = winner
			errors[idx] = err
		}(i)
	}
	wg.Wait()

	winnerCount := 0
	for i := 0; i < concurrency; i++ {
		if errors[i] != nil {
			t.Fatalf("concurrent reserve goroutine %d failed: %v", i, errors[i])
		}
		if winners[i] {
			winnerCount++
		}
	}
	if winnerCount != 1 {
		t.Fatalf("expected exactly 1 winner across %d concurrent reserves, got %d", concurrency, winnerCount)
	}
}

func TestStoreReopenPersistsDelegation(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "persist.db")

	ctx := context.Background()
	st1, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open 1 failed: %v", err)
	}

	seedSession(t, st1, "agent-1", "s-1")
	seedSession(t, st1, "agent-2", "s-2")

	err = st1.CreateDelegationEdge(ctx, DelegationEdge{
		ParentAgentID:   "agent-1",
		ParentSessionID: "s-1",
		ChildAgentID:    "agent-2",
		ChildSessionID:  "s-2",
	})
	if err != nil {
		t.Fatalf("CreateDelegationEdge failed: %v", err)
	}

	op := DelegationOperation{
		OperationID:     "op-persist-1",
		OperationType:   "create_session",
		CallerAgentID:   "agent-1",
		CallerSessionID: "s-1",
		TargetAgentID:   "agent-2",
		RequestHash:     "hash-persist-1",
		Stage:           OpStageCompleted,
		TargetSessionID: "s-2",
		RequestPayload:  map[string]any{"k": "v"},
		ResultPayload:   map[string]any{"done": true},
	}
	_, _, err = st1.ReserveDelegationOperation(ctx, op)
	if err != nil {
		t.Fatalf("ReserveDelegationOperation failed: %v", err)
	}

	if err := st1.Close(); err != nil {
		t.Fatalf("Close 1 failed: %v", err)
	}

	// Reopen store
	st2, err := OpenWithOptions(dbPath, Options{SchemaMode: SchemaValidateOnly})
	if err != nil {
		t.Fatalf("Open 2 (validate only) failed: %v", err)
	}
	defer st2.Close()

	// Verify edge recovered
	parent, found, err := st2.GetDelegationParent(ctx, "agent-2", "s-2")
	if err != nil || !found {
		t.Fatalf("edge recovery failed: found=%v err=%v", found, err)
	}
	if parent.ParentSessionID != "s-1" {
		t.Fatalf("unexpected parent session ID: %s", parent.ParentSessionID)
	}

	// Verify operation recovered
	recoveredOp, found, err := st2.GetDelegationOperation(ctx, "op-persist-1")
	if err != nil || !found {
		t.Fatalf("op recovery failed: found=%v err=%v", found, err)
	}
	if recoveredOp.TargetSessionID != "s-2" || recoveredOp.ResultPayload["done"] != true {
		t.Fatalf("unexpected recovered op: %+v", recoveredOp)
	}
}

func TestSchemaMigrationFromVersion3To4(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "migrate_v3_to_v4.db")

	// Step 1: Open store to create fresh DB
	st, err := Open(dbPath)
	if err != nil {
		t.Fatalf("initial open failed: %v", err)
	}
	ctx := context.Background()
	seedSession(t, st, "agent-mig", "s-mig")

	// Set version back to 3 and drop the v4 tables to simulate an exact v3 database
	if _, err := st.DB().Exec("PRAGMA user_version = 3"); err != nil {
		t.Fatalf("set user_version 3: %v", err)
	}
	if _, err := st.DB().Exec("DROP TABLE IF EXISTS delegation_edges"); err != nil {
		t.Fatalf("drop delegation_edges: %v", err)
	}
	if _, err := st.DB().Exec("DROP TABLE IF EXISTS delegation_operations"); err != nil {
		t.Fatalf("drop delegation_operations: %v", err)
	}
	_ = st.Close()

	// Step 2: Open with SchemaMigrate to trigger migration 3 -> 4
	migratedSt, err := OpenWithOptions(dbPath, Options{SchemaMode: SchemaMigrate})
	if err != nil {
		t.Fatalf("reopen with migrate failed: %v", err)
	}
	defer migratedSt.Close()

	// Verify schema version is now CurrentSchemaVersion (4)
	var ver int
	err = migratedSt.DB().QueryRow("PRAGMA user_version").Scan(&ver)
	if err != nil || ver != CurrentSchemaVersion {
		t.Fatalf("expected version %d, got %d, err=%v", CurrentSchemaVersion, ver, err)
	}

	// Verify existing session survived
	sess, err := migratedSt.GetSession(ctx, "agent-mig", "s-mig")
	if err != nil || sess == nil {
		t.Fatalf("session lost during migration: %v", err)
	}

	// Verify new tables are usable: self delegation rejected
	err = migratedSt.CreateDelegationEdge(ctx, DelegationEdge{
		ParentAgentID:   "agent-mig",
		ParentSessionID: "s-mig",
		ChildAgentID:    "agent-mig",
		ChildSessionID:  "s-mig",
	})
	if err == nil {
		t.Fatalf("expected self-delegation to fail")
	}

	// Verify valid delegation edge works
	seedSession(t, migratedSt, "agent-mig", "s-child")
	err = migratedSt.CreateDelegationEdge(ctx, DelegationEdge{
		ParentAgentID:   "agent-mig",
		ParentSessionID: "s-mig",
		ChildAgentID:    "agent-mig",
		ChildSessionID:  "s-child",
	})
	if err != nil {
		t.Fatalf("expected valid delegation edge to succeed: %v", err)
	}
}
