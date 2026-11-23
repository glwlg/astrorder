package sessions

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"astrorder.dev/app-server/internal/store"
	pb "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
)

func TestScheduleCalculationsAndCron(t *testing.T) {
	base := time.Date(2026, 11, 22, 1, 30, 0, 0, time.UTC) // 09:30 Asia/Shanghai (Sunday)

	// 1. Daily in Asia/Shanghai: 09:00 already passed -> next day 09:00 CST = 01:00 UTC on Nov 23.
	next, ok, err := NextScheduleRun(ScheduleSpec{
		ScheduleKind: "daily",
		DailyTime:    "09:00",
		Timezone:     "Asia/Shanghai",
	}, base)
	if err != nil || !ok {
		t.Fatalf("daily calc failed: ok=%v err=%v", ok, err)
	}
	wantDaily := time.Date(2026, 11, 23, 1, 0, 0, 0, time.UTC)
	if !next.Equal(wantDaily) {
		t.Fatalf("daily next=%s want %s", next, wantDaily)
	}

	// 2. Weekly weekdays (Mon-Fri = 62) from Sunday 09:30 CST -> Monday 09:00 CST = Nov 23 01:00 UTC.
	nextW, ok, err := NextScheduleRun(ScheduleSpec{
		ScheduleKind: "weekly",
		DailyTime:    "09:00",
		WeekdayMask:  62, // Mon..Fri
		Timezone:     "Asia/Shanghai",
	}, base)
	if err != nil || !ok {
		t.Fatalf("weekly calc failed: ok=%v err=%v", ok, err)
	}
	if !nextW.Equal(wantDaily) {
		t.Fatalf("weekly next=%s want %s", nextW, wantDaily)
	}

	// 3. Cron: every 15 min during 9-17 on weekdays (Mon-Fri) in UTC.
	runs, err := PreviewScheduleRuns(ScheduleSpec{
		ScheduleKind: "cron",
		CronExpr:     "*/15 9-17 * * 1-5",
		Timezone:     "UTC",
	}, time.Date(2026, 11, 22, 10, 0, 0, 0, time.UTC), 3)
	if err != nil || len(runs) != 3 {
		t.Fatalf("cron preview failed: runs=%v err=%v", runs, err)
	}
	if runs[0] != "2026-11-23T09:00:00Z" || runs[1] != "2026-11-23T09:15:00Z" || runs[2] != "2026-11-23T09:30:00Z" {
		t.Fatalf("unexpected cron preview runs: %v", runs)
	}

	// 4. Cron DOW 7 = Sunday.
	sunRun, ok, err := NextScheduleRun(ScheduleSpec{
		ScheduleKind: "cron",
		CronExpr:     "0 12 * * 7",
		Timezone:     "UTC",
	}, time.Date(2026, 11, 22, 11, 0, 0, 0, time.UTC))
	if err != nil || !ok || !sunRun.Equal(time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)) {
		t.Fatalf("dow=7 Sunday failed: next=%s ok=%v err=%v", sunRun, ok, err)
	}

	// 5. Invalid cron fails validation.
	if _, err := NormalizeScheduleSpec(ScheduleSpec{ScheduleKind: "cron", CronExpr: "99 * * * *"}); err == nil {
		t.Fatal("expected invalid cron minute error")
	}
	if _, err := NormalizeScheduleSpec(ScheduleSpec{ScheduleKind: "interval", IntervalMinutes: 0}); err == nil {
		t.Fatal("expected invalid interval error")
	}
}

func TestSessionSchedulesCRUDPreviewAndSessionCascade(t *testing.T) {
	h, st, _, cleanup := setupTestServer(t, &mockDaemonServer{})
	defer cleanup()
	ctx := context.Background()

	_, _ = st.UpsertAgent(ctx, map[string]any{"id": "agy-1", "kind": "antigravity", "name": "Antigravity", "status": "ready"})
	_, _ = st.UpsertSession(ctx, map[string]any{"id": "sess-1", "agent_id": "agy-1", "title": "Daily Ops", "status": "idle", "control_state": "owned", "updated_at": "2026-11-22T12:00:00Z"})

	// Preview endpoint
	prevReq := httptest.NewRequest(http.MethodPost, "/api/v1/schedules/preview", strings.NewReader(`{"schedule_kind":"daily","daily_time":"09:30","timezone":"Asia/Shanghai","count":3}`))
	prevReq.Header.Set("Authorization", "Bearer browser-sec")
	prevRec := httptest.NewRecorder()
	h.ServeHTTP(prevRec, prevReq)
	if prevRec.Code != http.StatusOK {
		t.Fatalf("preview failed: %d %s", prevRec.Code, prevRec.Body.String())
	}
	var prevResp struct {
		Valid    bool     `json:"valid"`
		NextRuns []string `json:"next_runs"`
	}
	_ = json.Unmarshal(prevRec.Body.Bytes(), &prevResp)
	if !prevResp.Valid || len(prevResp.NextRuns) != 3 {
		t.Fatalf("unexpected preview resp: %+v", prevResp)
	}

	// Create schedule
	createReq := httptest.NewRequest(http.MethodPost, "/api/v1/sessions/sess-1/schedules", strings.NewReader(`{
		"agent_id":"agy-1",
		"name":"晨报巡检",
		"prompt":"请检查今天构建状态",
		"schedule_kind":"daily",
		"daily_time":"09:00",
		"timezone":"Asia/Shanghai",
		"when_busy":"skip"
	}`))
	createReq.Header.Set("Authorization", "Bearer browser-sec")
	createRec := httptest.NewRecorder()
	h.ServeHTTP(createRec, createReq)
	if createRec.Code != http.StatusCreated {
		t.Fatalf("create failed: %d %s", createRec.Code, createRec.Body.String())
	}
	var created scheduleWire
	_ = json.Unmarshal(createRec.Body.Bytes(), &created)
	if created.ID == "" || created.SessionTitle != "Daily Ops" || created.AgentKind != "antigravity" || created.NextRunAt == "" {
		t.Fatalf("unexpected created schedule: %+v", created)
	}

	// List all schedules
	allReq := httptest.NewRequest(http.MethodGet, "/api/v1/schedules", nil)
	allReq.Header.Set("Authorization", "Bearer browser-sec")
	allRec := httptest.NewRecorder()
	h.ServeHTTP(allRec, allReq)
	if allRec.Code != http.StatusOK || !strings.Contains(allRec.Body.String(), created.ID) {
		t.Fatalf("list all failed: %d %s", allRec.Code, allRec.Body.String())
	}

	// Patch schedule (pause then re-enable with interval)
	patchReq := httptest.NewRequest(http.MethodPatch, "/api/v1/schedules/"+created.ID, strings.NewReader(`{"enabled":false}`))
	patchReq.Header.Set("Authorization", "Bearer browser-sec")
	patchRec := httptest.NewRecorder()
	h.ServeHTTP(patchRec, patchReq)
	if patchRec.Code != http.StatusOK {
		t.Fatalf("patch failed: %d %s", patchRec.Code, patchRec.Body.String())
	}
	var patched scheduleWire
	_ = json.Unmarshal(patchRec.Body.Bytes(), &patched)
	if patched.Enabled || patched.NextRunAt != "" {
		t.Fatalf("disabled schedule should clear next_run_at: %+v", patched)
	}

	// Manual run now
	runReq := httptest.NewRequest(http.MethodPost, "/api/v1/schedules/"+created.ID+"/run", nil)
	runReq.Header.Set("Authorization", "Bearer browser-sec")
	runRec := httptest.NewRecorder()
	h.ServeHTTP(runRec, runReq)
	if runRec.Code != http.StatusOK {
		t.Fatalf("run now failed: %d %s", runRec.Code, runRec.Body.String())
	}
	var runResp map[string]any
	_ = json.Unmarshal(runRec.Body.Bytes(), &runResp)
	if runResp["outcome"] != "sent" {
		t.Fatalf("expected outcome=sent, got %+v", runResp)
	}

	// Deleting session cascades and removes schedules
	deleted, err := st.DeleteSession(ctx, "agy-1", "sess-1")
	if err != nil || !deleted {
		t.Fatalf("DeleteSession failed: deleted=%v err=%v", deleted, err)
	}
	remaining, err := st.ListSessionSchedules(ctx, "", "")
	if err != nil || len(remaining) != 0 {
		t.Fatalf("expected schedules cascaded on session delete, got %+v err=%v", remaining, err)
	}
}

func TestSchedulerTickPoliciesBusyMisfireOnceAndCircuitBreaker(t *testing.T) {
	var sentTexts []string
	var failSend error
	daemon := &mockDaemonServer{
		sendCb: func(r *pb.SendRequest) (*pb.CommandReceipt, error) {
			if failSend != nil {
				return nil, failSend
			}
			for _, p := range r.GetInput() {
				if p.GetText() != nil {
					sentTexts = append(sentTexts, p.GetText().GetText())
				}
			}
			return &pb.CommandReceipt{
				OperationState: pb.OperationState_OPERATION_STATE_COMPLETED,
				Accepted:       true,
				Session:        r.Session,
				Status:         pb.SessionStatus_SESSION_STATUS_IDLE,
				Outcome:        &pb.CommandReceipt_Completed{Completed: &pb.CommandResult{Accepted: true}},
			}, nil
		},
	}
	h, st, _, cleanup := setupTestServer(t, daemon)
	defer cleanup()
	ctx := context.Background()

	_, _ = st.UpsertAgent(ctx, map[string]any{"id": "agy-1", "kind": "antigravity", "name": "Antigravity", "status": "ready"})
	_, _ = st.UpsertSession(ctx, map[string]any{"id": "sess-idle", "agent_id": "agy-1", "title": "Idle", "status": "idle", "control_state": "owned", "updated_at": "2026-11-22T12:00:00Z"})
	_, _ = st.UpsertSession(ctx, map[string]any{"id": "sess-busy", "agent_id": "agy-1", "title": "Busy", "status": "running", "control_state": "owned", "updated_at": "2026-11-22T12:00:00Z"})

	now := time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)

	// 1. One-shot due schedule -> fires once and auto-disables with pause_reason=completed.
	onceSched, err := st.CreateSessionSchedule(ctx, store.SessionSchedule{
		AgentID: "agy-1", SessionID: "sess-idle", Name: "Once", Prompt: "ping once",
		Enabled: true, ScheduleKind: "once", RunAt: now.Add(-10 * time.Second).Format(time.RFC3339),
		Timezone: "UTC", WhenBusy: "skip", MisfirePolicy: "grace_once", MisfireGraceSec: 900,
		NextRunAt: now.Add(-10 * time.Second).Format(time.RFC3339),
	})
	if err != nil {
		t.Fatal(err)
	}

	// 2. Busy session with when_busy=skip -> skipped_busy, no command created.
	skipSched, err := st.CreateSessionSchedule(ctx, store.SessionSchedule{
		AgentID: "agy-1", SessionID: "sess-busy", Name: "BusySkip", Prompt: "skip me",
		Enabled: true, ScheduleKind: "interval", IntervalMinutes: 10,
		Timezone: "UTC", WhenBusy: "skip", MisfirePolicy: "grace_once", MisfireGraceSec: 900,
		NextRunAt: now.Add(-5 * time.Second).Format(time.RFC3339),
	})
	if err != nil {
		t.Fatal(err)
	}

	// 3. Busy session with when_busy=queue -> enqueues command, last_status=queued.
	queueSched, err := st.CreateSessionSchedule(ctx, store.SessionSchedule{
		AgentID: "agy-1", SessionID: "sess-busy", Name: "BusyQueue", Prompt: "queue me",
		Enabled: true, ScheduleKind: "interval", IntervalMinutes: 10,
		Timezone: "UTC", WhenBusy: "queue", MisfirePolicy: "grace_once", MisfireGraceSec: 900,
		NextRunAt: now.Add(-5 * time.Second).Format(time.RFC3339),
	})
	if err != nil {
		t.Fatal(err)
	}

	// 4. Misfired schedule (lag 2h > 900s grace) -> skipped_misfire, advances next_run_at without firing.
	misfireSched, err := st.CreateSessionSchedule(ctx, store.SessionSchedule{
		AgentID: "agy-1", SessionID: "sess-idle", Name: "Misfire", Prompt: "missed me",
		Enabled: true, ScheduleKind: "interval", IntervalMinutes: 30,
		Timezone: "UTC", WhenBusy: "skip", MisfirePolicy: "grace_once", MisfireGraceSec: 900,
		NextRunAt: now.Add(-2 * time.Hour).Format(time.RFC3339),
	})
	if err != nil {
		t.Fatal(err)
	}

	if err := h.TickSchedules(ctx, now); err != nil {
		t.Fatalf("TickSchedules failed: %v", err)
	}

	if len(sentTexts) != 1 || sentTexts[0] != "ping once" {
		t.Fatalf("expected only 'ping once' sent directly, got %v", sentTexts)
	}
	gotOnce, _ := st.GetSessionSchedule(ctx, onceSched.ID)
	if gotOnce.Enabled || gotOnce.LastStatus != "sent" || gotOnce.PauseReason != "completed" {
		t.Fatalf("unexpected once schedule after fire: %+v", gotOnce)
	}
	gotSkip, _ := st.GetSessionSchedule(ctx, skipSched.ID)
	if !gotSkip.Enabled || gotSkip.LastStatus != "skipped_busy" || gotSkip.ConsecutiveFailures != 0 {
		t.Fatalf("unexpected skip schedule: %+v", gotSkip)
	}
	gotQueue, _ := st.GetSessionSchedule(ctx, queueSched.ID)
	if !gotQueue.Enabled || gotQueue.LastStatus != "queued" || gotQueue.LastCommandID == "" {
		t.Fatalf("unexpected queue schedule: %+v", gotQueue)
	}
	queuedCmd, _ := st.GetCommand(ctx, "agy-1", "sess-busy", gotQueue.LastCommandID)
	if queuedCmd == nil || queuedCmd["state"] != "queued" || queuedCmd["action"] != "enqueue" {
		t.Fatalf("expected queued command persisted, got %+v", queuedCmd)
	}
	gotMisfire, _ := st.GetSessionSchedule(ctx, misfireSched.ID)
	if !gotMisfire.Enabled || gotMisfire.LastStatus != "skipped_misfire" || gotMisfire.NextRunAt <= now.Format(time.RFC3339) {
		t.Fatalf("unexpected misfire schedule: %+v", gotMisfire)
	}

	// 5. Circuit breaker: 3 consecutive failures auto-pause.
	failSend = errors.New("simulated daemon error")
	cbSched, err := st.CreateSessionSchedule(ctx, store.SessionSchedule{
		AgentID: "agy-1", SessionID: "sess-idle", Name: "Flaky", Prompt: "fail me",
		Enabled: true, ScheduleKind: "interval", IntervalMinutes: 5,
		Timezone: "UTC", WhenBusy: "skip", MisfirePolicy: "run_once", MisfireGraceSec: 900,
		NextRunAt: now.Add(-1 * time.Second).Format(time.RFC3339),
	})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		tickTime := now.Add(time.Duration(i*10) * time.Minute)
		if err := h.TickSchedules(ctx, tickTime); err != nil {
			t.Fatalf("cb tick %d failed: %v", i, err)
		}
	}
	gotCB, _ := st.GetSessionSchedule(ctx, cbSched.ID)
	if gotCB.Enabled || gotCB.ConsecutiveFailures != 3 || gotCB.LastStatus != "paused_circuit_breaker" || gotCB.PauseReason != "circuit_breaker" {
		t.Fatalf("expected circuit breaker pause after 3 failures, got %+v", gotCB)
	}
}
