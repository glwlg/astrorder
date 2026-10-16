package projection

import (
	"astrorder.dev/app-server/internal/store"
	pb "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
	"context"
	"database/sql"
	"path/filepath"
	"testing"
)

func TestProjectGrok_RequestPermissionAndResolution(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "grok_approval.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	aid, sid := "grok-agent", "session-grok-1"
	if _, err := st.UpsertSession(ctx, map[string]any{
		"id": sid, "agent_id": aid, "title": "Grok Test Session", "status": "running", "updated_at": "2026-10-16T00:00:00Z",
	}); err != nil {
		t.Fatal(err)
	}

	// 1. Emulate grok.notification with session/request_permission
	permissionPayload := map[string]any{
		"agent_id":   aid,
		"session_id": sid,
		"frame": map[string]any{
			"jsonrpc": "2.0",
			"id":      101,
			"method":  "session/request_permission",
			"params": map[string]any{
				"sessionId": sid,
				"toolCall": map[string]any{
					"toolCallId": "call-perm-1",
					"title":      "Bash execution",
				},
				"options": []any{
					map[string]any{"optionId": "allow_once", "name": "Allow Once", "kind": "allow_once"},
					map[string]any{"optionId": "reject_once", "name": "Reject Once", "kind": "reject_once"},
				},
			},
		},
	}

	var events []store.Event
	err = st.WithTx(ctx, func(tx *sql.Tx) error {
		var e error
		events, e = projectGrokNotificationTx(ctx, tx, sid, permissionPayload)
		return e
	})
	if err != nil {
		t.Fatalf("projectGrokNotificationTx failed: %v", err)
	}
	for _, ev := range events {
		if _, err := st.AppendEvent(ctx, ev); err != nil {
			t.Fatalf("append event: %v", err)
		}
	}

	// Session status should now be waiting_approval
	session, err := st.GetSession(ctx, aid, sid)
	if err != nil || session["status"] != "waiting_approval" {
		t.Fatalf("expected session status waiting_approval, got %v", session["status"])
	}

	// Approvals table / preferences must have the pending approval with preserved options
	pending, err := st.PendingApprovals(ctx)
	if err != nil {
		t.Fatalf("PendingApprovals: %v", err)
	}
	if len(pending) != 1 {
		t.Fatalf("expected 1 pending approval, got %d", len(pending))
	}
	app := pending[0]
	if app["id"] != "grok:101" {
		t.Fatalf("expected approval id grok:101, got %v", app["id"])
	}
	if app["state"] != "pending" {
		t.Fatalf("expected state pending, got %v", app["state"])
	}
	opts, ok := app["options"].([]any)
	if !ok || len(opts) != 2 {
		t.Fatalf("expected 2 options preserved, got %#v", app["options"])
	}
	firstOpt, _ := opts[0].(map[string]any)
	if firstOpt["kind"] != "allow_once" {
		t.Fatalf("expected option kind allow_once, got %v", firstOpt["kind"])
	}

	// 2. Emulate grok.approval_resolved
	resolveEnv := &pb.EventEnvelope{
		Session:   &pb.SessionRef{SessionId: sid},
		EventType: "grok.approval_resolved",
		Status:    pb.SessionStatus_SESSION_STATUS_RUNNING,
		NativePayload: &pb.NativeJson{
			Utf8Json: []byte(`{"agent_id":"grok-agent","session_id":"session-grok-1","approval_id":"grok:101","decision":"accept"}`),
		},
	}
	var resolveEvents []store.Event
	err = st.WithTx(ctx, func(tx *sql.Tx) error {
		var e error
		resolveEvents, e = (&Projection{}).projectEnvelopeTx(ctx, tx, resolveEnv)
		return e
	})
	if err != nil {
		t.Fatalf("projectEnvelopeTx grok.approval_resolved failed: %v", err)
	}
	for _, ev := range resolveEvents {
		if _, err := st.AppendEvent(ctx, ev); err != nil {
			t.Fatalf("append resolve event: %v", err)
		}
	}

	// Pending approvals should now be 0
	pendingAfter, err := st.PendingApprovals(ctx)
	if err != nil || len(pendingAfter) != 0 {
		t.Fatalf("expected 0 pending approvals after resolve, got %v", pendingAfter)
	}

	// Session status should now be running
	sessionAfter, err := st.GetSession(ctx, aid, sid)
	if err != nil || sessionAfter["status"] != "running" {
		t.Fatalf("expected session status running after resolve, got %v", sessionAfter["status"])
	}
}

func TestProjectGrok_ToolCallAndToolCallUpdate(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "grok_tools.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	aid, sid := "grok-agent", "session-grok-tools"
	if _, err := st.UpsertSession(ctx, map[string]any{
		"id": sid, "agent_id": aid, "title": "Grok Tool Session", "status": "running", "updated_at": "2026-10-16T00:00:00Z",
	}); err != nil {
		t.Fatal(err)
	}

	// 1. Standard ACP tool_call
	toolCallPayload := map[string]any{
		"agent_id":   aid,
		"session_id": sid,
		"frame": map[string]any{
			"jsonrpc": "2.0",
			"method":  "session/update",
			"params": map[string]any{
				"sessionId": sid,
				"update": map[string]any{
					"sessionUpdate": "tool_call",
					"toolCallId":    "call_tool_01",
					"title":         "Apply Patch to foo.go",
					"kind":          "edit",
					"status":        "in_progress",
					"rawInput": map[string]any{
						"path":  "foo.go",
						"patch": "patch content",
					},
					"locations": []any{
						map[string]any{"path": "foo.go", "line": 15},
					},
				},
			},
		},
	}

	var events1 []store.Event
	err = st.WithTx(ctx, func(tx *sql.Tx) error {
		var e error
		events1, e = projectGrokNotificationTx(ctx, tx, sid, toolCallPayload)
		return e
	})
	if err != nil {
		t.Fatalf("tool_call notification failed: %v", err)
	}

	foundTaskUpsert := false
	foundToolMessage := false
	for _, ev := range events1 {
		if ev.Type == "task.upsert" && ev.Data["kind"] == "tool" {
			foundTaskUpsert = true
			if ev.Data["id"] != "grok:tool:call_tool_01" {
				t.Fatalf("expected task id grok:tool:call_tool_01, got %v", ev.Data["id"])
			}
			if ev.Data["status"] != "running" {
				t.Fatalf("expected task status running, got %v", ev.Data["status"])
			}
		}
		if ev.Type == "message.upsert" && ev.Data["role"] == "tool" {
			foundToolMessage = true
		}
	}
	if !foundTaskUpsert {
		t.Fatalf("expected task.upsert for tool_call")
	}
	if !foundToolMessage {
		t.Fatalf("expected message.upsert for tool_call")
	}

	// 2. Standard ACP tool_call_update
	toolUpdatePayload := map[string]any{
		"agent_id":   aid,
		"session_id": sid,
		"frame": map[string]any{
			"jsonrpc": "2.0",
			"method":  "session/update",
			"params": map[string]any{
				"sessionId": sid,
				"update": map[string]any{
					"sessionUpdate": "tool_call_update",
					"toolCallId":    "call_tool_01",
					"status":        "completed",
					"content": []any{
						map[string]any{"type": "diff", "path": "foo.go", "oldText": "hello", "newText": "world"},
						map[string]any{"type": "text", "text": "Successfully patched foo.go"},
					},
					"locations": []any{
						map[string]any{"path": "foo.go", "line": 15},
					},
					"rawOutput": map[string]any{
						"exitCode": 0,
					},
				},
			},
		},
	}

	var events2 []store.Event
	err = st.WithTx(ctx, func(tx *sql.Tx) error {
		var e error
		events2, e = projectGrokNotificationTx(ctx, tx, sid, toolUpdatePayload)
		return e
	})
	if err != nil {
		t.Fatalf("tool_call_update notification failed: %v", err)
	}

	foundCompletedTask := false
	for _, ev := range events2 {
		if ev.Type == "task.upsert" && ev.Data["kind"] == "tool" {
			foundCompletedTask = true
			if ev.Data["status"] != "completed" {
				t.Fatalf("expected task status completed, got %v", ev.Data["status"])
			}
		}
		if ev.Type == "task.live" {
			prog, _ := ev.Data["progress"].(map[string]any)
			if prog["output"] != "Successfully patched foo.go" {
				t.Fatalf("expected progress output 'Successfully patched foo.go', got %v", prog["output"])
			}
			diffs, _ := prog["diff"].([]any)
			if len(diffs) == 0 {
				t.Fatalf("expected diff preserved in progress, got %#v", prog["diff"])
			}
			locs, _ := prog["locations"].([]any)
			if len(locs) == 0 {
				t.Fatalf("expected locations in progress, got %#v", prog["locations"])
			}
		}
	}
	if !foundCompletedTask {
		t.Fatalf("expected task.upsert for tool_call_update")
	}
}

func TestProjectGrok_Plan(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "grok_plan.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	aid, sid := "grok-agent", "session-grok-plan"
	if _, err := st.UpsertSession(ctx, map[string]any{
		"id": sid, "agent_id": aid, "title": "Grok Plan Session", "status": "running", "updated_at": "2026-10-16T00:00:00Z",
	}); err != nil {
		t.Fatal(err)
	}

	planPayload := map[string]any{
		"agent_id":   aid,
		"session_id": sid,
		"frame": map[string]any{
			"jsonrpc": "2.0",
			"method":  "session/update",
			"params": map[string]any{
				"sessionId": sid,
				"update": map[string]any{
					"sessionUpdate": "plan",
					"entries": []any{
						map[string]any{"content": "Step 1: Check prerequisites", "status": "completed"},
						map[string]any{"content": "Step 2: Implement changes", "status": "in_progress"},
						map[string]any{"content": "Step 3: Run verification tests", "status": "pending"},
					},
				},
			},
		},
	}

	var events []store.Event
	err = st.WithTx(ctx, func(tx *sql.Tx) error {
		var e error
		events, e = projectGrokNotificationTx(ctx, tx, sid, planPayload)
		return e
	})
	if err != nil {
		t.Fatalf("plan notification failed: %v", err)
	}

	planTasks := 0
	for _, ev := range events {
		if ev.Type == "task.upsert" && ev.Data["kind"] == "todo" {
			planTasks++
		}
	}
	if planTasks != 3 {
		t.Fatalf("expected 3 plan tasks upserted, got %d", planTasks)
	}

	rows, err := st.QueryRows(ctx, "SELECT id, status, title FROM tasks WHERE agent_id=? AND session_id=? AND kind='todo' ORDER BY id", aid, sid)
	if err != nil || len(rows) != 3 {
		t.Fatalf("expected 3 tasks in db, got %d (err: %v)", len(rows), err)
	}
	if rows[0]["status"] != "completed" || rows[1]["status"] != "running" || rows[2]["status"] != "pending" {
		t.Fatalf("unexpected task statuses: %+v", rows)
	}
}

func TestProjectGrok_UsageUpdate(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "grok_usage.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	aid, sid := "grok-agent", "session-grok-usage"
	if _, err := st.UpsertSession(ctx, map[string]any{
		"id": sid, "agent_id": aid, "title": "Grok Usage Session", "status": "running", "updated_at": "2026-10-16T00:00:00Z",
	}); err != nil {
		t.Fatal(err)
	}

	// 1. Structured usage
	usagePayload := map[string]any{
		"agent_id":   aid,
		"session_id": sid,
		"frame": map[string]any{
			"jsonrpc": "2.0",
			"method":  "session/update",
			"params": map[string]any{
				"sessionId": sid,
				"update": map[string]any{
					"sessionUpdate": "usage_update",
					"model":         "grok-2",
					"inputTokens":   1200,
					"outputTokens":  350,
					"totalTokens":   1550,
				},
			},
		},
	}

	var events []store.Event
	err = st.WithTx(ctx, func(tx *sql.Tx) error {
		var e error
		events, e = projectGrokNotificationTx(ctx, tx, sid, usagePayload)
		return e
	})
	if err != nil {
		t.Fatalf("usage update failed: %v", err)
	}

	foundUsage := false
	for _, ev := range events {
		if ev.Type == "token.usage" {
			foundUsage = true
			if ev.Data["total_tokens"] != int64(1550) && ev.Data["total_tokens"] != 1550 {
				t.Fatalf("expected total_tokens 1550, got %v", ev.Data["total_tokens"])
			}
		}
	}
	if !foundUsage {
		t.Fatalf("expected token.usage event")
	}

	// 2. Unstructured or empty usage must be ignored
	emptyUsagePayload := map[string]any{
		"agent_id":   aid,
		"session_id": sid,
		"frame": map[string]any{
			"jsonrpc": "2.0",
			"method":  "session/update",
			"params": map[string]any{
				"sessionId": sid,
				"update": map[string]any{
					"sessionUpdate": "usage_update",
				},
			},
		},
	}
	var emptyEvents []store.Event
	err = st.WithTx(ctx, func(tx *sql.Tx) error {
		var e error
		emptyEvents, e = projectGrokNotificationTx(ctx, tx, sid, emptyUsagePayload)
		return e
	})
	if err != nil {
		t.Fatalf("empty usage failed: %v", err)
	}
	for _, ev := range emptyEvents {
		if ev.Type == "token.usage" {
			t.Fatalf("empty usage must not produce token.usage event")
		}
	}
}

func TestProjectGrok_CompletionCleansPendingApprovalsAndRunningTools(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "grok_cleanup.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	aid, sid := "grok-agent", "session-grok-clean"
	if _, err := st.UpsertSession(ctx, map[string]any{
		"id": sid, "agent_id": aid, "title": "Grok Clean Session", "status": "waiting_approval", "updated_at": "2026-10-16T00:00:00Z",
	}); err != nil {
		t.Fatal(err)
	}
	// Seed pending approval
	if _, err := st.AppendEvent(ctx, store.Event{
		ID: "appr-1", Type: "approval.upsert", AgentID: aid, SessionID: sid, Data: map[string]any{"id": "grok:999", "state": "pending", "agent_id": aid, "session_id": sid},
	}); err != nil {
		t.Fatal(err)
	}
	// Seed running tool task
	_, err = st.UpsertTask(ctx, map[string]any{
		"id": "grok:tool:call-running", "agent_id": aid, "session_id": sid, "kind": "tool", "title": "Active tool", "status": "running", "logs": []any{}, "created_at": "2026-10-16T00:00:00Z", "updated_at": "2026-10-16T00:00:00Z",
	})
	if err != nil {
		t.Fatal(err)
	}
	// Seed pending native plan task (must NOT be marked completed on turn_completed!)
	_, err = st.UpsertTask(ctx, map[string]any{
		"id": "grok:todo:2", "agent_id": aid, "session_id": sid, "kind": "todo", "title": "Future plan step", "status": "pending", "logs": []any{}, "created_at": "2026-10-16T00:00:00Z", "updated_at": "2026-10-16T00:00:00Z",
	})
	if err != nil {
		t.Fatal(err)
	}

	// Turn completed notification
	completedPayload := map[string]any{
		"agent_id":   aid,
		"session_id": sid,
		"frame": map[string]any{
			"jsonrpc": "2.0",
			"method":  "session/update",
			"params": map[string]any{
				"sessionId": sid,
				"update": map[string]any{
					"sessionUpdate": "turn_completed",
				},
			},
		},
	}

	var completionEvents []store.Event
	err = st.WithTx(ctx, func(tx *sql.Tx) error {
		var e error
		completionEvents, e = projectGrokNotificationTx(ctx, tx, sid, completedPayload)
		return e
	})
	for _, ev := range completionEvents {
		if _, appendErr := st.AppendEvent(ctx, ev); appendErr != nil {
			t.Fatal(appendErr)
		}
	}
	if err != nil {
		t.Fatalf("turn_completed failed: %v", err)
	}

	// Pending approvals should be cleaned up
	pending, err := st.PendingApprovals(ctx)
	if err != nil || len(pending) != 0 {
		t.Fatalf("expected 0 pending approvals after turn_completed, got %v", pending)
	}

	// Tool task should be marked completed
	tasks, err := st.QueryRows(ctx, "SELECT id, status FROM tasks WHERE agent_id=? AND session_id=? AND id='grok:tool:call-running'", aid, sid)
	if err != nil || len(tasks) != 1 || tasks[0]["status"] != "completed" {
		t.Fatalf("expected task to be completed, got %+v", tasks)
	}

	// Plan task must remain pending (not marked completed!)
	planTasks, err := st.QueryRows(ctx, "SELECT id, status FROM tasks WHERE agent_id=? AND session_id=? AND id='grok:todo:2'", aid, sid)
	if err != nil || len(planTasks) != 1 || planTasks[0]["status"] != "pending" {
		t.Fatalf("plan task should remain pending, got %+v", planTasks)
	}
}
