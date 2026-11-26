package sessions

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"astrorder.dev/app-server/internal/store"
)

const (
	scheduleTickInterval      = 15 * time.Second
	scheduleCircuitBreakerMax = 3
	maxSchedulePromptLen      = 100000
	maxScheduleNameLen        = 200
)

type scheduleWire struct {
	store.SessionSchedule
	SessionTitle        string `json:"session_title,omitempty"`
	SessionStatus       string `json:"session_status,omitempty"`
	SessionApprovalMode string `json:"session_approval_mode,omitempty"`
	AgentName           string `json:"agent_name,omitempty"`
	AgentKind           string `json:"agent_kind,omitempty"`
}

func scheduleToMap(item scheduleWire) map[string]any {
	raw, _ := json.Marshal(item)
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	return out
}

func scheduleSpecFromStore(item store.SessionSchedule) ScheduleSpec {
	return ScheduleSpec{
		ScheduleKind:    item.ScheduleKind,
		CronExpr:        item.CronExpr,
		IntervalMinutes: item.IntervalMinutes,
		DailyTime:       item.DailyTime,
		WeekdayMask:     item.WeekdayMask,
		RunAt:           item.RunAt,
		Timezone:        item.Timezone,
		WhenBusy:        item.WhenBusy,
		MisfirePolicy:   item.MisfirePolicy,
		MisfireGraceSec: item.MisfireGraceSec,
	}
}

// DefaultScheduleName returns a bounded schedule title, falling back to the first line of prompt.
func DefaultScheduleName(name, prompt string) string {
	trimmed := strings.TrimSpace(name)
	if trimmed != "" {
		runes := []rune(trimmed)
		if len(runes) > maxScheduleNameLen {
			return string(runes[:maxScheduleNameLen])
		}
		return trimmed
	}
	firstLine := strings.TrimSpace(prompt)
	if idx := strings.IndexAny(firstLine, "\r\n"); idx >= 0 {
		firstLine = strings.TrimSpace(firstLine[:idx])
	}
	runes := []rune(firstLine)
	if len(runes) > 36 {
		return string(runes[:36]) + "…"
	}
	if len(runes) == 0 {
		return "定时任务"
	}
	return firstLine
}

func defaultScheduleName(name, prompt string) string {
	return DefaultScheduleName(name, prompt)
}

// WakeSchedules nudges the background schedule loop to re-evaluate due tasks immediately.
func (h *Handler) WakeSchedules() {
	h.wakeScheduler()
}

func (h *Handler) wakeScheduler() {
	if h.scheduleWake == nil {
		return
	}
	select {
	case h.scheduleWake <- struct{}{}:
	default:
	}
}

func (h *Handler) enrichSchedules(ctx context.Context, items []store.SessionSchedule) []scheduleWire {
	out := make([]scheduleWire, 0, len(items))
	agentCache := map[string]map[string]any{}
	sessionCache := map[string]map[string]any{}
	for _, item := range items {
		w := scheduleWire{SessionSchedule: item}
		agent, ok := agentCache[item.AgentID]
		if !ok {
			agent, _ = h.store.GetAgent(ctx, item.AgentID)
			agentCache[item.AgentID] = agent
		}
		if agent != nil {
			w.AgentName = stringValue(agent, "name")
			w.AgentKind = stringValue(agent, "kind")
		}
		skey := item.AgentID + "\x00" + item.SessionID
		sess, ok := sessionCache[skey]
		if !ok {
			sess, _ = h.store.GetSession(ctx, item.AgentID, item.SessionID)
			sessionCache[skey] = sess
		}
		if sess != nil {
			w.SessionTitle = stringValue(sess, "title")
			w.SessionStatus = stringValue(sess, "status")
			w.SessionApprovalMode = stringValue(sess, "selected_approval_mode")
		}
		out = append(out, w)
	}
	return out
}

func (h *Handler) publishScheduleUpsert(ctx context.Context, item store.SessionSchedule) {
	enriched := h.enrichSchedules(ctx, []store.SessionSchedule{item})
	if len(enriched) > 0 {
		h.publishEvent(ctx, "session_schedule.upsert", scheduleToMap(enriched[0]))
	}
}

func (h *Handler) handleListAllSchedules(w http.ResponseWriter, r *http.Request) {
	items, err := h.store.ListSessionSchedules(r.Context(), "", "")
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": h.enrichSchedules(r.Context(), items)})
}

func (h *Handler) handleListSessionSchedules(w http.ResponseWriter, r *http.Request, sid string) {
	ctx := r.Context()
	aid := strings.TrimSpace(r.URL.Query().Get("agent_id"))
	if aid == "" {
		writeJSONError(w, http.StatusUnprocessableEntity, "agent_id is required")
		return
	}
	session, err := h.store.GetSession(ctx, aid, sid)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if session == nil {
		writeJSONError(w, http.StatusNotFound, "Session was not found")
		return
	}
	items, err := h.store.ListSessionSchedules(ctx, aid, sid)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": h.enrichSchedules(ctx, items)})
}

type previewScheduleRequest struct {
	ScheduleKind    string `json:"schedule_kind"`
	CronExpr        string `json:"cron_expr"`
	IntervalMinutes int    `json:"interval_minutes"`
	DailyTime       string `json:"daily_time"`
	WeekdayMask     int    `json:"weekday_mask"`
	RunAt           string `json:"run_at"`
	Timezone        string `json:"timezone"`
	Count           int    `json:"count"`
}

func (h *Handler) handlePreviewSchedule(w http.ResponseWriter, r *http.Request) {
	var req previewScheduleRequest
	if err := decodeJSON(r.Body, &req); err != nil {
		writeJSONError(w, http.StatusUnprocessableEntity, "Invalid schedule preview payload")
		return
	}
	spec := ScheduleSpec{
		ScheduleKind:    req.ScheduleKind,
		CronExpr:        req.CronExpr,
		IntervalMinutes: req.IntervalMinutes,
		DailyTime:       req.DailyTime,
		WeekdayMask:     req.WeekdayMask,
		RunAt:           req.RunAt,
		Timezone:        req.Timezone,
	}
	norm, err := NormalizeScheduleSpec(spec)
	if err != nil {
		writeJSONError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	count := req.Count
	if count <= 0 {
		count = 3
	}
	nextRuns, err := PreviewScheduleRuns(norm, time.Now().UTC(), count)
	if err != nil {
		writeJSONError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"valid":     true,
		"timezone":  norm.Timezone,
		"next_runs": nextRuns,
	})
}

type createScheduleRequest struct {
	AgentID         string `json:"agent_id"`
	Name            string `json:"name"`
	Prompt          string `json:"prompt"`
	Enabled         *bool  `json:"enabled"`
	ScheduleKind    string `json:"schedule_kind"`
	CronExpr        string `json:"cron_expr"`
	IntervalMinutes int    `json:"interval_minutes"`
	DailyTime       string `json:"daily_time"`
	WeekdayMask     int    `json:"weekday_mask"`
	RunAt           string `json:"run_at"`
	Timezone        string `json:"timezone"`
	WhenBusy        string `json:"when_busy"`
	MisfirePolicy   string `json:"misfire_policy"`
	MisfireGraceSec int    `json:"misfire_grace_sec"`
}

func (h *Handler) handleCreateSessionSchedule(w http.ResponseWriter, r *http.Request, sid string) {
	ctx := r.Context()
	var req createScheduleRequest
	if err := decodeJSON(r.Body, &req); err != nil {
		writeJSONError(w, http.StatusUnprocessableEntity, "Invalid schedule payload")
		return
	}
	aid := strings.TrimSpace(req.AgentID)
	if aid == "" {
		aid = strings.TrimSpace(r.URL.Query().Get("agent_id"))
	}
	if aid == "" {
		writeJSONError(w, http.StatusUnprocessableEntity, "agent_id is required")
		return
	}
	session, err := h.store.GetSession(ctx, aid, sid)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if session == nil {
		writeJSONError(w, http.StatusNotFound, "Session was not found")
		return
	}
	prompt := strings.TrimSpace(req.Prompt)
	if prompt == "" {
		writeJSONError(w, http.StatusUnprocessableEntity, "prompt is required")
		return
	}
	if len(prompt) > maxSchedulePromptLen {
		writeJSONError(w, http.StatusUnprocessableEntity, "prompt is too long")
		return
	}
	spec, err := NormalizeScheduleSpec(ScheduleSpec{
		ScheduleKind:    req.ScheduleKind,
		CronExpr:        req.CronExpr,
		IntervalMinutes: req.IntervalMinutes,
		DailyTime:       req.DailyTime,
		WeekdayMask:     req.WeekdayMask,
		RunAt:           req.RunAt,
		Timezone:        req.Timezone,
		WhenBusy:        req.WhenBusy,
		MisfirePolicy:   req.MisfirePolicy,
		MisfireGraceSec: req.MisfireGraceSec,
	})
	if err != nil {
		writeJSONError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	now := time.Now().UTC()
	nextRunAt := ""
	if enabled {
		next, ok, err := NextScheduleRun(spec, now)
		if err != nil {
			writeJSONError(w, http.StatusUnprocessableEntity, err.Error())
			return
		}
		if !ok {
			writeJSONError(w, http.StatusUnprocessableEntity, "One-time schedule run_at must be in the future")
			return
		}
		nextRunAt = next.UTC().Format(time.RFC3339)
	}
	item := store.SessionSchedule{
		AgentID:         aid,
		SessionID:       sid,
		Name:            defaultScheduleName(req.Name, prompt),
		Prompt:          prompt,
		Enabled:         enabled,
		ScheduleKind:    spec.ScheduleKind,
		CronExpr:        spec.CronExpr,
		IntervalMinutes: spec.IntervalMinutes,
		DailyTime:       spec.DailyTime,
		WeekdayMask:     spec.WeekdayMask,
		RunAt:           spec.RunAt,
		Timezone:        spec.Timezone,
		WhenBusy:        spec.WhenBusy,
		MisfirePolicy:   spec.MisfirePolicy,
		MisfireGraceSec: spec.MisfireGraceSec,
		NextRunAt:       nextRunAt,
	}
	created, err := h.store.CreateSessionSchedule(ctx, item)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	h.publishScheduleUpsert(ctx, created)
	h.wakeScheduler()
	enriched := h.enrichSchedules(ctx, []store.SessionSchedule{created})
	writeJSON(w, http.StatusCreated, enriched[0])
}

type updateScheduleRequest struct {
	Name            *string `json:"name"`
	Prompt          *string `json:"prompt"`
	Enabled         *bool   `json:"enabled"`
	ScheduleKind    *string `json:"schedule_kind"`
	CronExpr        *string `json:"cron_expr"`
	IntervalMinutes *int    `json:"interval_minutes"`
	DailyTime       *string `json:"daily_time"`
	WeekdayMask     *int    `json:"weekday_mask"`
	RunAt           *string `json:"run_at"`
	Timezone        *string `json:"timezone"`
	WhenBusy        *string `json:"when_busy"`
	MisfirePolicy   *string `json:"misfire_policy"`
	MisfireGraceSec *int    `json:"misfire_grace_sec"`
}

func (h *Handler) handleUpdateSchedule(w http.ResponseWriter, r *http.Request, scheduleID string) {
	ctx := r.Context()
	existing, err := h.store.GetSessionSchedule(ctx, scheduleID)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if existing == nil {
		writeJSONError(w, http.StatusNotFound, "Schedule was not found")
		return
	}
	var req updateScheduleRequest
	if err := decodeJSON(r.Body, &req); err != nil {
		writeJSONError(w, http.StatusUnprocessableEntity, "Invalid schedule update payload")
		return
	}
	updated := *existing
	timingChanged := false
	if req.Prompt != nil {
		prompt := strings.TrimSpace(*req.Prompt)
		if prompt == "" {
			writeJSONError(w, http.StatusUnprocessableEntity, "prompt is required")
			return
		}
		if len(prompt) > maxSchedulePromptLen {
			writeJSONError(w, http.StatusUnprocessableEntity, "prompt is too long")
			return
		}
		updated.Prompt = prompt
	}
	if req.Name != nil {
		updated.Name = defaultScheduleName(*req.Name, updated.Prompt)
	}
	if req.ScheduleKind != nil {
		updated.ScheduleKind = *req.ScheduleKind
		timingChanged = true
	}
	if req.CronExpr != nil {
		updated.CronExpr = *req.CronExpr
		timingChanged = true
	}
	if req.IntervalMinutes != nil {
		updated.IntervalMinutes = *req.IntervalMinutes
		timingChanged = true
	}
	if req.DailyTime != nil {
		updated.DailyTime = *req.DailyTime
		timingChanged = true
	}
	if req.WeekdayMask != nil {
		updated.WeekdayMask = *req.WeekdayMask
		timingChanged = true
	}
	if req.RunAt != nil {
		updated.RunAt = *req.RunAt
		timingChanged = true
	}
	if req.Timezone != nil {
		updated.Timezone = *req.Timezone
		timingChanged = true
	}
	if req.WhenBusy != nil {
		updated.WhenBusy = *req.WhenBusy
	}
	if req.MisfirePolicy != nil {
		updated.MisfirePolicy = *req.MisfirePolicy
	}
	if req.MisfireGraceSec != nil {
		updated.MisfireGraceSec = *req.MisfireGraceSec
	}
	enabledChanged := false
	if req.Enabled != nil {
		if *req.Enabled != updated.Enabled {
			enabledChanged = true
		}
		updated.Enabled = *req.Enabled
		if updated.Enabled {
			updated.ConsecutiveFailures = 0
			updated.PauseReason = ""
		}
	}

	spec, err := NormalizeScheduleSpec(scheduleSpecFromStore(updated))
	if err != nil {
		writeJSONError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	updated.ScheduleKind = spec.ScheduleKind
	updated.CronExpr = spec.CronExpr
	updated.IntervalMinutes = spec.IntervalMinutes
	updated.DailyTime = spec.DailyTime
	updated.WeekdayMask = spec.WeekdayMask
	updated.RunAt = spec.RunAt
	updated.Timezone = spec.Timezone
	updated.WhenBusy = spec.WhenBusy
	updated.MisfirePolicy = spec.MisfirePolicy
	updated.MisfireGraceSec = spec.MisfireGraceSec

	if !updated.Enabled {
		updated.NextRunAt = ""
	} else if timingChanged || enabledChanged || strings.TrimSpace(updated.NextRunAt) == "" {
		next, ok, err := NextScheduleRun(spec, time.Now().UTC())
		if err != nil {
			writeJSONError(w, http.StatusUnprocessableEntity, err.Error())
			return
		}
		if !ok {
			writeJSONError(w, http.StatusUnprocessableEntity, "One-time schedule run_at must be in the future")
			return
		}
		updated.NextRunAt = next.UTC().Format(time.RFC3339)
	}

	saved, err := h.store.UpdateSessionSchedule(ctx, updated)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if saved == nil {
		writeJSONError(w, http.StatusNotFound, "Schedule was not found")
		return
	}
	h.publishScheduleUpsert(ctx, *saved)
	h.wakeScheduler()
	enriched := h.enrichSchedules(ctx, []store.SessionSchedule{*saved})
	writeJSON(w, http.StatusOK, enriched[0])
}

func (h *Handler) handleDeleteSchedule(w http.ResponseWriter, r *http.Request, scheduleID string) {
	ctx := r.Context()
	existing, err := h.store.GetSessionSchedule(ctx, scheduleID)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if existing == nil {
		writeJSONError(w, http.StatusNotFound, "Schedule was not found")
		return
	}
	deleted, err := h.store.DeleteSessionSchedule(ctx, scheduleID)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !deleted {
		writeJSONError(w, http.StatusNotFound, "Schedule was not found")
		return
	}
	h.publishEvent(ctx, "session_schedule.delete", map[string]any{
		"id":         existing.ID,
		"agent_id":   existing.AgentID,
		"session_id": existing.SessionID,
	})
	writeJSON(w, http.StatusOK, map[string]any{"deleted": true, "id": existing.ID})
}

func (h *Handler) handleRunScheduleNow(w http.ResponseWriter, r *http.Request, scheduleID string) {
	ctx := r.Context()
	existing, err := h.store.GetSessionSchedule(ctx, scheduleID)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if existing == nil {
		writeJSONError(w, http.StatusNotFound, "Schedule was not found")
		return
	}
	if _, busy := h.scheduleBusy.LoadOrStore(scheduleID, struct{}{}); busy {
		writeJSONError(w, http.StatusConflict, "Schedule is already dispatching")
		return
	}
	defer h.scheduleBusy.Delete(scheduleID)

	updated, outcome, cmdID, dispatchErr := h.dispatchSchedulePrompt(ctx, *existing, time.Now().UTC(), true)
	enriched := h.enrichSchedules(ctx, []store.SessionSchedule{updated})
	resp := map[string]any{
		"schedule":   enriched[0],
		"outcome":    outcome,
		"command_id": cmdID,
	}
	if dispatchErr != nil {
		resp["detail"] = dispatchErr.Error()
	}
	writeJSON(w, http.StatusOK, resp)
}

// RunSchedules drives scheduled session prompt dispatches on a wall-clock ticker.
func (h *Handler) RunSchedules(ctx context.Context) {
	// Wait briefly for startup initialization before the first sweep.
	timer := time.NewTimer(2 * time.Second)
	select {
	case <-ctx.Done():
		timer.Stop()
		return
	case <-timer.C:
	}

	ticker := time.NewTicker(scheduleTickInterval)
	defer ticker.Stop()

	for {
		if ctx.Err() != nil {
			return
		}
		attempt, cancel := context.WithTimeout(ctx, 45*time.Second)
		if err := h.TickSchedules(attempt, time.Now().UTC()); err != nil && ctx.Err() == nil {
			slog.Warn("session schedule tick failed", "error", err)
		}
		cancel()
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		case <-h.scheduleWake:
		}
	}
}

// TickSchedules evaluates all due schedules as of `now` and dispatches eligible prompts.
func (h *Handler) TickSchedules(ctx context.Context, now time.Time) error {
	nowUTC := now.UTC()
	due, err := h.store.ListDueSessionSchedules(ctx, nowUTC)
	if err != nil {
		return err
	}
	for _, item := range due {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if _, busy := h.scheduleBusy.LoadOrStore(item.ID, struct{}{}); busy {
			continue
		}
		func(sched store.SessionSchedule) {
			defer h.scheduleBusy.Delete(sched.ID)
			h.executeDueSchedule(ctx, sched, nowUTC)
		}(item)
	}
	return nil
}

func (h *Handler) executeDueSchedule(ctx context.Context, sched store.SessionSchedule, nowUTC time.Time) {
	fresh, err := h.store.GetSessionSchedule(ctx, sched.ID)
	if err != nil || fresh == nil || !fresh.Enabled || strings.TrimSpace(fresh.NextRunAt) == "" {
		return
	}
	dueAt, err := parseRFC3339Time(fresh.NextRunAt)
	if err != nil || dueAt.After(nowUTC) {
		return
	}
	lag := nowUTC.Sub(dueAt)

	// Advance next_run_at atomically BEFORE dispatching to prevent double-fires across ticks/restarts.
	claimed := *fresh
	spec := scheduleSpecFromStore(claimed)
	if claimed.ScheduleKind == scheduleKindOnce {
		claimed.Enabled = false
		claimed.NextRunAt = ""
		claimed.PauseReason = "completed"
	} else {
		next, ok, nextErr := NextScheduleRun(spec, nowUTC)
		if nextErr != nil || !ok {
			claimed.Enabled = false
			claimed.NextRunAt = ""
			claimed.PauseReason = "invalid_schedule"
			claimed.LastStatus = "failed"
			if nextErr != nil {
				claimed.LastError = nextErr.Error()
			}
			if saved, saveErr := h.store.UpdateSessionSchedule(ctx, claimed); saveErr == nil && saved != nil {
				h.publishScheduleUpsert(ctx, *saved)
			}
			return
		}
		claimed.NextRunAt = next.UTC().Format(time.RFC3339)
	}

	// Check misfire policy before sending.
	misfired := false
	switch claimed.MisfirePolicy {
	case misfireSkip:
		misfired = lag > 60*time.Second
	case misfireGraceOnce, "":
		grace := claimed.MisfireGraceSec
		if grace <= 0 {
			grace = defaultMisfireGraceSec
		}
		misfired = lag > time.Duration(grace)*time.Second
	case misfireRunOnce:
		misfired = false
	}

	if misfired {
		claimed.LastRunAt = nowUTC.Format(time.RFC3339)
		claimed.LastStatus = "skipped_misfire"
		claimed.LastError = fmt.Sprintf("missed window by %s", lag.Truncate(time.Second))
		if saved, saveErr := h.store.UpdateSessionSchedule(ctx, claimed); saveErr == nil && saved != nil {
			h.publishScheduleUpsert(ctx, *saved)
		}
		return
	}

	savedClaim, err := h.store.UpdateSessionSchedule(ctx, claimed)
	if err != nil || savedClaim == nil {
		return
	}

	_, _, _, _ = h.dispatchSchedulePrompt(ctx, *savedClaim, nowUTC, false)
}

func (h *Handler) isSessionBusyForSchedule(ctx context.Context, agent, session map[string]any) bool {
	statusStr := stringValue(session, "status")
	if statusStr == "running" || statusStr == "waiting_approval" {
		return true
	}
	if h.client != nil && h.client.Daemon != nil {
		ref := buildSessionRef(stringValue(agent, "id"), stringValue(session, "id"), stringValue(session, "connection_id"))
		if busy, err := h.runningGuidance(ctx, ref); err == nil && busy {
			return true
		}
	}
	return false
}

func (h *Handler) dispatchSchedulePrompt(ctx context.Context, sched store.SessionSchedule, nowUTC time.Time, manual bool) (store.SessionSchedule, string, string, error) {
	updated := sched
	saveAndPublish := func() store.SessionSchedule {
		if saved, err := h.store.UpdateSessionSchedule(ctx, updated); err == nil && saved != nil {
			h.publishScheduleUpsert(ctx, *saved)
			return *saved
		}
		return updated
	}

	session, err := h.store.GetSession(ctx, sched.AgentID, sched.SessionID)
	if err != nil {
		updated.LastRunAt = nowUTC.Format(time.RFC3339)
		updated.LastStatus = "failed"
		updated.LastError = err.Error()
		return saveAndPublish(), "failed", "", err
	}
	if session == nil {
		updated.Enabled = false
		updated.NextRunAt = ""
		updated.PauseReason = "session_deleted"
		updated.LastRunAt = nowUTC.Format(time.RFC3339)
		updated.LastStatus = "failed"
		updated.LastError = "Session no longer exists"
		return saveAndPublish(), "failed", "", fmt.Errorf("session not found")
	}

	agent, err := h.store.GetAgent(ctx, sched.AgentID)
	if err != nil || agent == nil {
		updated.LastRunAt = nowUTC.Format(time.RFC3339)
		updated.LastStatus = "skipped_offline"
		updated.LastError = "Agent was not found"
		return saveAndPublish(), "skipped_offline", "", fmt.Errorf("agent not found")
	}
	if connErr := h.requireAgentConnection(ctx, agent); connErr != nil {
		updated.LastRunAt = nowUTC.Format(time.RFC3339)
		updated.LastStatus = "skipped_offline"
		updated.LastError = connErr.Error()
		return saveAndPublish(), "skipped_offline", "", connErr
	}

	busy := h.isSessionBusyForSchedule(ctx, agent, session)
	action := "send"
	successStatus := "sent"
	if busy {
		if sched.WhenBusy == whenBusyQueue {
			action = "enqueue"
			successStatus = "queued"
		} else {
			updated.LastRunAt = nowUTC.Format(time.RFC3339)
			updated.LastStatus = "skipped_busy"
			updated.LastError = ""
			return saveAndPublish(), "skipped_busy", "", nil
		}
	}

	cmdID := fmt.Sprintf("sched-%s-%d", sched.ID, nowUTC.UnixNano())
	if len(cmdID) > 128 {
		cmdID = cmdID[:128]
	}
	_, submitErr := h.Submit(ctx, map[string]any{
		"id":         cmdID,
		"agent_id":   sched.AgentID,
		"session_id": sched.SessionID,
		"action":     action,
		"text":       sched.Prompt,
	})
	updated.LastRunAt = nowUTC.Format(time.RFC3339)
	updated.LastCommandID = cmdID
	if submitErr != nil {
		updated.LastError = submitErr.Error()
		if !manual {
			updated.ConsecutiveFailures++
			if updated.ConsecutiveFailures >= scheduleCircuitBreakerMax {
				updated.Enabled = false
				updated.NextRunAt = ""
				updated.LastStatus = "paused_circuit_breaker"
				updated.PauseReason = "circuit_breaker"
				return saveAndPublish(), "paused_circuit_breaker", cmdID, submitErr
			}
		}
		updated.LastStatus = "failed"
		return saveAndPublish(), "failed", cmdID, submitErr
	}

	updated.LastStatus = successStatus
	updated.LastError = ""
	updated.ConsecutiveFailures = 0
	if updated.PauseReason != "completed" {
		updated.PauseReason = ""
	}
	return saveAndPublish(), successStatus, cmdID, nil
}
