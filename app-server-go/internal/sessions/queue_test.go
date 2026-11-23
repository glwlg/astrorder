package sessions

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestQueueOperations(t *testing.T) {
	srv := &mockDaemonServer{}
	h, st, _, cleanup := setupTestServer(t, srv)
	defer cleanup()

	ctx := context.Background()

	// 1. Setup seed agent and session
	_, err := st.UpsertAgent(ctx, map[string]any{
		"id":     "agent-q",
		"name":   "Queue Agent",
		"kind":   "codex",
		"status": "ready",
	})
	if err != nil {
		t.Fatalf("upsert agent: %v", err)
	}

	_, err = st.UpsertSession(ctx, map[string]any{
		"id":         "sess-q",
		"agent_id":   "agent-q",
		"title":      "Queue Session",
		"workspace":  "C:/work",
		"status":     "idle",
		"created_at": "2026-10-09T00:00:00Z",
		"updated_at": "2026-10-09T00:00:00Z",
	})
	if err != nil {
		t.Fatalf("upsert session: %v", err)
	}

	// 2. Create queued command via store directly
	cmdPayload := map[string]any{
		"id":         "cmd-1",
		"agent_id":   "agent-q",
		"session_id": "sess-q",
		"action":     "enqueue",
		"text":       "initial text",
	}
	_, _, err = st.CreateCommand(ctx, cmdPayload, "queued")
	if err != nil {
		t.Fatalf("create queued command: %v", err)
	}

	// 3. Test handleListCommands
	{
		req := httptest.NewRequest(http.MethodGet, "/api/v1/sessions/sess-q/commands?agent_id=agent-q", nil)
		w := httptest.NewRecorder()
		h.handleListCommands(w, req, "sess-q")
		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
		}
		var resp map[string]any
		_ = json.Unmarshal(w.Body.Bytes(), &resp)
		items, ok := resp["items"].([]any)
		if !ok || len(items) != 1 {
			t.Fatalf("expected 1 item, got %v", resp)
		}
	}

	// 4. Test handleListTasks
	{
		req := httptest.NewRequest(http.MethodGet, "/api/v1/sessions/sess-q/tasks?agent_id=agent-q", nil)
		w := httptest.NewRecorder()
		h.handleListTasks(w, req, "sess-q")
		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
		}
		var resp map[string]any
		_ = json.Unmarshal(w.Body.Bytes(), &resp)
		items, ok := resp["items"].([]any)
		if !ok || len(items) != 0 {
			t.Fatalf("expected 0 tasks, got %v", resp)
		}
	}

	// 5. Test handleEditQueuedCommand
	{
		editBody, _ := json.Marshal(map[string]any{
			"agent_id":   "agent-q",
			"session_id": "sess-q",
			"text":       "edited text",
		})
		req := httptest.NewRequest(http.MethodPatch, "/api/v1/commands/cmd-1", bytes.NewReader(editBody))
		w := httptest.NewRecorder()
		h.handleEditQueuedCommand(w, req, "cmd-1")
		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
		}
		var updated map[string]any
		_ = json.Unmarshal(w.Body.Bytes(), &updated)
		if updated["text"] != "edited text" {
			t.Fatalf("expected edited text, got %v", updated["text"])
		}
	}

	// 6. Test handleDeleteQueuedCommand
	{
		req := httptest.NewRequest(http.MethodDelete, "/api/v1/commands/cmd-1?agent_id=agent-q&session_id=sess-q", nil)
		w := httptest.NewRecorder()
		h.handleDeleteQueuedCommand(w, req, "cmd-1")
		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
		}
		var updated map[string]any
		_ = json.Unmarshal(w.Body.Bytes(), &updated)
		if updated["state"] != "cancelled" {
			t.Fatalf("expected state cancelled, got %v", updated["state"])
		}

		// Repeating a removal returns the existing terminal receipt.
		wAgain := httptest.NewRecorder()
		h.handleDeleteQueuedCommand(wAgain, req, "cmd-1")
		if wAgain.Code != http.StatusOK {
			t.Fatalf("expected idempotent removal, got %d: %s", wAgain.Code, wAgain.Body.String())
		}
	}
}

func TestRemoveUnknownAndAlreadyCompletedQueueRecords(t *testing.T) {
	h, st, _, cleanup := setupTestServer(t, &mockDaemonServer{})
	defer cleanup()
	ctx := context.Background()
	if _, err := st.UpsertSession(ctx, map[string]any{"id": "s", "agent_id": "a", "status": "idle", "updated_at": "2026-10-28T00:00:00Z"}); err != nil {
		t.Fatal(err)
	}
	for _, state := range []string{"unknown", "completed", "received"} {
		if _, _, err := st.CreateCommand(ctx, map[string]any{"id": state, "agent_id": "a", "session_id": "s", "action": "enqueue", "text": "message"}, state); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := st.SetCommandState(ctx, "a", "s", "unknown", "unknown", "delivery unconfirmed", false); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		id, state string
		code      int
	}{{"unknown", "cancelled", 200}, {"completed", "completed", 200}, {"received", "", 409}} {
		req := httptest.NewRequest(http.MethodDelete, "/api/v1/commands/"+tc.id+"?agent_id=a&session_id=s", nil)
		w := httptest.NewRecorder()
		h.handleDeleteQueuedCommand(w, req, tc.id)
		if w.Code != tc.code {
			t.Fatalf("%s: got %d %s", tc.id, w.Code, w.Body.String())
		}
		if tc.state != "" {
			var row map[string]any
			if err := json.Unmarshal(w.Body.Bytes(), &row); err != nil || row["state"] != tc.state {
				t.Fatalf("%s: unexpected receipt %v %v", tc.id, row, err)
			}
			if tc.id == "unknown" && row["error"] != "delivery unconfirmed" {
				t.Fatalf("unknown delivery evidence was lost: %v", row)
			}
		}
	}
}

func TestListTasksBoundsTerminalHistoryAndPreservesActiveTasks(t *testing.T) {
	h, st, _, cleanup := setupTestServer(t, &mockDaemonServer{})
	defer cleanup()
	ctx := context.Background()
	if _, err := st.UpsertSession(ctx, map[string]any{
		"id": "sess-many", "agent_id": "agent-m", "status": "running", "updated_at": "2026-11-23T00:00:00Z",
	}); err != nil {
		t.Fatal(err)
	}
	longLogs := make([]any, 80)
	for i := range longLogs {
		longLogs[i] = fmt.Sprintf("line-%03d", i)
	}
	// Seed 1 running task, 1 completed subagent task, and 180 completed tool tasks.
	if _, err := st.UpsertTask(ctx, map[string]any{
		"id": "active-bg", "agent_id": "agent-m", "session_id": "sess-many",
		"kind": "background", "title": "running server", "status": "running",
		"logs": longLogs, "created_at": "2026-11-23T00:00:01Z", "updated_at": "2026-11-23T00:00:01Z",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.UpsertTask(ctx, map[string]any{
		"id": "sub-1", "agent_id": "agent-m", "session_id": "sess-many",
		"kind": "subagent", "title": "researcher", "status": "completed",
		"logs": []any{}, "created_at": "2026-11-23T00:00:02Z", "updated_at": "2026-11-23T00:00:02Z",
	}); err != nil {
		t.Fatal(err)
	}
	for i := range 180 {
		ts := fmt.Sprintf("2026-11-23T01:%02d:%02dZ", i/60, i%60)
		logs := []any{}
		if i == 179 {
			logs = longLogs
		}
		if _, err := st.UpsertTask(ctx, map[string]any{
			"id": fmt.Sprintf("tool-%03d", i), "agent_id": "agent-m", "session_id": "sess-many",
			"kind": "tool", "title": fmt.Sprintf("tool %d", i), "status": "completed",
			"logs": logs, "created_at": ts, "updated_at": ts,
		}); err != nil {
			t.Fatal(err)
		}
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/sessions/sess-many/tasks?agent_id=agent-m", nil)
	w := httptest.NewRecorder()
	h.handleListTasks(w, req, "sess-many")
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	// 1 active + 1 subagent + 120 newest terminal tool tasks = 122.
	if len(resp.Items) != 122 {
		t.Fatalf("expected 122 bounded tasks, got %d", len(resp.Items))
	}
	byID := map[string]map[string]any{}
	for _, item := range resp.Items {
		byID[fmt.Sprint(item["id"])] = item
	}
	if byID["active-bg"] == nil {
		t.Fatal("active running task was dropped")
	}
	if logs, _ := byID["active-bg"]["logs"].([]any); len(logs) != 80 {
		t.Fatalf("active task logs should remain untouched, got %d", len(logs))
	}
	if byID["sub-1"] == nil {
		t.Fatal("completed subagent summary task was dropped")
	}
	if byID["tool-000"] != nil {
		t.Fatal("oldest completed tool task should have been pruned")
	}
	if byID["tool-179"] == nil {
		t.Fatal("newest completed tool task missing")
	}
	if logs, _ := byID["tool-179"]["logs"].([]any); len(logs) != maxTerminalTaskLogLines {
		t.Fatalf("expected terminal task logs trimmed to %d, got %d", maxTerminalTaskLogLines, len(logs))
	}
}

func TestWriteMessagesJSONETagNotModified(t *testing.T) {
	payload := map[string]any{"items": []map[string]any{{"id": "m-1", "text": "hello"}}, "next_cursor": nil}
	w1 := httptest.NewRecorder()
	writeMessagesJSON(w1, httptest.NewRequest(http.MethodGet, "/api/v1/sessions/s/messages?agent_id=a", nil), payload)
	if w1.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w1.Code)
	}
	etag := w1.Header().Get("ETag")
	if etag == "" || w1.Header().Get("Cache-Control") != "private, no-cache" {
		t.Fatalf("unexpected cache headers: ETag=%q Cache-Control=%q", etag, w1.Header().Get("Cache-Control"))
	}

	req2 := httptest.NewRequest(http.MethodGet, "/api/v1/sessions/s/messages?agent_id=a", nil)
	req2.Header.Set("If-None-Match", etag)
	w2 := httptest.NewRecorder()
	writeMessagesJSON(w2, req2, payload)
	if w2.Code != http.StatusNotModified || w2.Body.Len() != 0 {
		t.Fatalf("expected 304 empty body on ETag match, got %d len=%d", w2.Code, w2.Body.Len())
	}
}
