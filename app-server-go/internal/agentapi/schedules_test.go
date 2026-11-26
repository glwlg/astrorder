package agentapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"astrorder.dev/app-server/internal/store"
)

func TestAgentSelfSchedulesLifecycleAndSessionIsolation(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "agent_schedules.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	ctx := context.Background()
	_, err = st.UpsertAgent(ctx, map[string]any{
		"id":            "ssh-agy-1",
		"kind":          "antigravity-ssh",
		"name":          "WSL · Antigravity",
		"connection_id": "ssh-wsl-1",
		"profile_name":  "default",
		"status":        "ready",
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = st.UpsertSession(ctx, map[string]any{
		"id":            "sess-self",
		"agent_id":      "ssh-agy-1",
		"title":         "常规会话",
		"workspace":     "/home/luwei/workspace/demo",
		"connection_id": "ssh-wsl-1",
		"status":        "idle",
		"updated_at":    time.Now().UTC().Format(time.RFC3339),
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = st.UpsertSession(ctx, map[string]any{
		"id":            "sess-other",
		"agent_id":      "ssh-agy-1",
		"title":         "其他会话",
		"workspace":     "/home/luwei/workspace/other",
		"connection_id": "ssh-wsl-1",
		"status":        "idle",
		"updated_at":    time.Now().UTC().Format(time.RFC3339),
	})
	if err != nil {
		t.Fatal(err)
	}

	pub := &testPublisher{}
	var woke atomic.Int32
	rawHandler, err := NewHandler(Options{
		Store:         st,
		Authorizer:    &mockTestAuthorizer{},
		Publisher:     pub,
		WakeScheduler: func() { woke.Add(1) },
	})
	if err != nil {
		t.Fatal(err)
	}
	h := rawHandler.(*Handler)

	selfCaller := map[string]any{
		"agent_id":      "ssh-agy-1",
		"connection_id": "ssh-wsl-1",
		"profile_name":  "default",
		"runtime":       "antigravity",
		"session_id":    "sess-self",
	}
	otherCaller := map[string]any{
		"agent_id":      "ssh-agy-1",
		"connection_id": "ssh-wsl-1",
		"profile_name":  "default",
		"runtime":       "antigravity",
		"session_id":    "sess-other",
	}

	invokeHTTP := func(capability string, input map[string]any) *httptest.ResponseRecorder {
		body, _ := json.Marshal(map[string]any{"capability": capability, "input": input})
		req := httptest.NewRequest(http.MethodPost, "/api/v1/agent/invoke", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		return w
	}

	// 1. Missing caller must be rejected.
	if w := invokeHTTP("schedules.create", map[string]any{"prompt": "hi", "delay_seconds": 60}); w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 without caller, got %d %s", w.Code, w.Body.String())
	}

	// 2. Conflicting session_key must be rejected.
	if w := invokeHTTP("schedules.create", map[string]any{
		"caller":        selfCaller,
		"session_key":   "ssh-agy-1::sess-other",
		"prompt":        "hijack attempt",
		"delay_seconds": 60,
	}); w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for conflicting session_key, got %d %s", w.Code, w.Body.String())
	}

	// 3. Create one-shot timer using relative delay_seconds.
	wCreate := invokeHTTP("schedules.create", map[string]any{
		"caller":        selfCaller,
		"name":          "检查构建结果",
		"prompt":        "检查流水线是否完成并汇报",
		"delay_seconds": 600,
		"when_busy":     "queue",
	})
	if wCreate.Code != http.StatusOK {
		t.Fatalf("create failed: %d %s", wCreate.Code, wCreate.Body.String())
	}
	var createdResp struct {
		OK         bool              `json:"ok"`
		SessionKey string            `json:"session_key"`
		Schedule   agentScheduleWire `json:"schedule"`
	}
	if err := json.Unmarshal(wCreate.Body.Bytes(), &createdResp); err != nil {
		t.Fatal(err)
	}
	if !createdResp.OK || createdResp.Schedule.ID == "" || createdResp.Schedule.AgentID != "ssh-agy-1" || createdResp.Schedule.SessionID != "sess-self" {
		t.Fatalf("unexpected created schedule: %+v", createdResp)
	}
	if createdResp.Schedule.ScheduleKind != "once" || createdResp.Schedule.WhenBusy != "queue" || createdResp.Schedule.NextRunAt == "" {
		t.Fatalf("unexpected schedule spec: %+v", createdResp.Schedule)
	}
	if woke.Load() < 1 {
		t.Fatalf("expected WakeScheduler to be called on create")
	}

	// 4. Create weekly recurring schedule using friendly weekdays spec.
	wWeekly := invokeHTTP("schedules.create", map[string]any{
		"caller":     selfCaller,
		"name":       "工作日晨报",
		"prompt":     "汇总昨日告警与待办",
		"daily_time": "09:00",
		"weekdays":   "mon-fri",
		"timezone":   "Asia/Shanghai",
	})
	if wWeekly.Code != http.StatusOK {
		t.Fatalf("weekly create failed: %d %s", wWeekly.Code, wWeekly.Body.String())
	}
	var weeklyResp struct {
		Schedule agentScheduleWire `json:"schedule"`
	}
	_ = json.Unmarshal(wWeekly.Body.Bytes(), &weeklyResp)
	if weeklyResp.Schedule.ScheduleKind != "weekly" || weeklyResp.Schedule.WeekdayMask != 62 {
		t.Fatalf("expected weekly mask=62, got %+v", weeklyResp.Schedule)
	}

	// 5. List returns only caller's own schedules; other session sees 0.
	wListSelf := invokeHTTP("schedules.list", map[string]any{"caller": selfCaller})
	if wListSelf.Code != http.StatusOK {
		t.Fatalf("list self failed: %d %s", wListSelf.Code, wListSelf.Body.String())
	}
	var listSelf struct {
		Items []agentScheduleWire `json:"items"`
	}
	_ = json.Unmarshal(wListSelf.Body.Bytes(), &listSelf)
	if len(listSelf.Items) != 2 {
		t.Fatalf("expected 2 schedules for self, got %d", len(listSelf.Items))
	}

	wListOther := invokeHTTP("schedules.list", map[string]any{"caller": otherCaller})
	var listOther struct {
		Items []agentScheduleWire `json:"items"`
	}
	_ = json.Unmarshal(wListOther.Body.Bytes(), &listOther)
	if len(listOther.Items) != 0 {
		t.Fatalf("expected 0 schedules for other session, got %d", len(listOther.Items))
	}

	// 6. Other session cannot update or delete self's schedule.
	if w := invokeHTTP("schedules.update", map[string]any{
		"caller":  otherCaller,
		"id":      createdResp.Schedule.ID,
		"enabled": false,
	}); w.Code != http.StatusBadRequest {
		t.Fatalf("expected cross-session update rejection, got %d %s", w.Code, w.Body.String())
	}
	if w := invokeHTTP("schedules.delete", map[string]any{
		"caller": otherCaller,
		"id":     createdResp.Schedule.ID,
	}); w.Code != http.StatusBadRequest {
		t.Fatalf("expected cross-session delete rejection, got %d %s", w.Code, w.Body.String())
	}

	// 7. Self pauses, then switches to interval, then deletes.
	wPause := invokeHTTP("schedules.update", map[string]any{
		"caller":  selfCaller,
		"id":      createdResp.Schedule.ID,
		"enabled": false,
	})
	if wPause.Code != http.StatusOK {
		t.Fatalf("pause failed: %d %s", wPause.Code, wPause.Body.String())
	}
	var paused struct {
		Schedule agentScheduleWire `json:"schedule"`
	}
	_ = json.Unmarshal(wPause.Body.Bytes(), &paused)
	if paused.Schedule.Enabled || paused.Schedule.NextRunAt != "" {
		t.Fatalf("expected paused schedule with empty next_run_at, got %+v", paused.Schedule)
	}

	wResume := invokeHTTP("schedules.update", map[string]any{
		"caller":           selfCaller,
		"id":               createdResp.Schedule.ID,
		"enabled":          true,
		"interval_minutes": 15,
	})
	if wResume.Code != http.StatusOK {
		t.Fatalf("resume failed: %d %s", wResume.Code, wResume.Body.String())
	}
	var resumed struct {
		Schedule agentScheduleWire `json:"schedule"`
	}
	_ = json.Unmarshal(wResume.Body.Bytes(), &resumed)
	if !resumed.Schedule.Enabled || resumed.Schedule.ScheduleKind != "interval" || resumed.Schedule.IntervalMinutes != 15 || resumed.Schedule.NextRunAt == "" {
		t.Fatalf("unexpected resumed schedule: %+v", resumed.Schedule)
	}

	wDel := invokeHTTP("schedules.delete", map[string]any{
		"caller": selfCaller,
		"id":     createdResp.Schedule.ID,
	})
	if wDel.Code != http.StatusOK {
		t.Fatalf("delete failed: %d %s", wDel.Code, wDel.Body.String())
	}

	remaining, err := st.ListSessionSchedules(ctx, "ssh-agy-1", "sess-self")
	if err != nil || len(remaining) != 1 || remaining[0].ID != weeklyResp.Schedule.ID {
		t.Fatalf("unexpected remaining schedules: %+v err=%v", remaining, err)
	}
}
