package agentapi

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"astrorder.dev/app-server/internal/sessions"
	"astrorder.dev/app-server/internal/store"
)

const (
	maxAgentSchedulePromptLen  = 100000
	maxAgentScheduleDelaySec   = 365 * 24 * 3600
	minAgentScheduleDelaySec   = 5
)

type agentScheduleWire struct {
	store.SessionSchedule
	SessionTitle        string `json:"session_title,omitempty"`
	SessionStatus       string `json:"session_status,omitempty"`
	SessionApprovalMode string `json:"session_approval_mode,omitempty"`
	AgentName           string `json:"agent_name,omitempty"`
	AgentKind           string `json:"agent_kind,omitempty"`
}

func (h *Handler) requireScheduleCaller(payload map[string]any) (string, string, error) {
	if payload == nil {
		return "", "", NewAgentApiError("invalid_caller", "caller identity is required for session schedules")
	}
	if _, hasCaller := payload["caller"]; !hasCaller {
		return "", "", NewAgentApiError("invalid_caller", "verified caller object is required for session schedules")
	}
	aid, _ := payload["caller_agent_id"].(string)
	sid, _ := payload["caller_session_id"].(string)
	aid = strings.TrimSpace(aid)
	sid = strings.TrimSpace(sid)
	if aid == "" || sid == "" {
		return "", "", NewAgentApiError("invalid_caller", "caller agent_id and session_id are required for session schedules")
	}
	if rawKey, ok := payload["session_key"].(string); ok && strings.TrimSpace(rawKey) != "" {
		expected := aid + "::" + sid
		if strings.TrimSpace(rawKey) != expected {
			return "", "", NewAgentApiError("invalid_caller", fmt.Sprintf("session_key %q conflicts with caller %q", rawKey, expected))
		}
	}
	return aid, sid, nil
}

func (h *Handler) enrichAgentSchedules(ctx context.Context, items []store.SessionSchedule) []agentScheduleWire {
	out := make([]agentScheduleWire, 0, len(items))
	agentCache := map[string]map[string]any{}
	sessionCache := map[string]map[string]any{}
	for _, item := range items {
		w := agentScheduleWire{SessionSchedule: item}
		agent, ok := agentCache[item.AgentID]
		if !ok {
			agent, _ = h.options.Store.GetAgent(ctx, item.AgentID)
			agentCache[item.AgentID] = agent
		}
		if agent != nil {
			if name, ok := agent["name"].(string); ok {
				w.AgentName = name
			}
			if kind, ok := agent["kind"].(string); ok {
				w.AgentKind = kind
			}
		}
		skey := item.AgentID + "\x00" + item.SessionID
		sess, ok := sessionCache[skey]
		if !ok {
			sess, _ = h.options.Store.GetSession(ctx, item.AgentID, item.SessionID)
			sessionCache[skey] = sess
		}
		if sess != nil {
			if title, ok := sess["title"].(string); ok {
				w.SessionTitle = title
			}
			if status, ok := sess["status"].(string); ok {
				w.SessionStatus = status
			}
			if mode, ok := sess["selected_approval_mode"].(string); ok {
				w.SessionApprovalMode = mode
			}
		}
		out = append(out, w)
	}
	return out
}

func scheduleWireToMap(item agentScheduleWire) map[string]any {
	raw, _ := json.Marshal(item)
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	return out
}

func (h *Handler) publishAgentScheduleUpsert(ctx context.Context, item store.SessionSchedule) agentScheduleWire {
	enriched := h.enrichAgentSchedules(ctx, []store.SessionSchedule{item})
	wire := enriched[0]
	if h.options.Publisher != nil {
		_ = h.options.Publisher.Publish(ctx, "session_schedule.upsert", scheduleWireToMap(wire))
	}
	return wire
}

func (h *Handler) invokeSchedulesList(ctx context.Context, payload map[string]any) (any, error) {
	aid, sid, err := h.requireScheduleCaller(payload)
	if err != nil {
		return nil, err
	}
	items, err := h.options.Store.ListSessionSchedules(ctx, aid, sid)
	if err != nil {
		return nil, fmt.Errorf("list session schedules: %w", err)
	}
	return map[string]any{
		"ok":          true,
		"session_key": aid + "::" + sid,
		"items":       h.enrichAgentSchedules(ctx, items),
	}, nil
}

func (h *Handler) invokeSchedulesCreate(ctx context.Context, payload map[string]any) (any, error) {
	aid, sid, err := h.requireScheduleCaller(payload)
	if err != nil {
		return nil, err
	}
	session, err := h.options.Store.GetSession(ctx, aid, sid)
	if err != nil {
		return nil, fmt.Errorf("load caller session: %w", err)
	}
	if session == nil {
		return nil, NewAgentApiError("not_found", "Caller session was not found")
	}

	prompt := strings.TrimSpace(payloadString(payload, "prompt"))
	if prompt == "" {
		return nil, NewAgentApiError("invalid_input", "prompt is required")
	}
	if len(prompt) > maxAgentSchedulePromptLen {
		return nil, NewAgentApiError("invalid_input", "prompt is too long")
	}

	now := time.Now().UTC()
	spec, err := parseScheduleSpecFromPayload(payload, now)
	if err != nil {
		return nil, NewAgentApiError("invalid_input", err.Error())
	}
	norm, err := sessions.NormalizeScheduleSpec(spec)
	if err != nil {
		return nil, NewAgentApiError("invalid_input", err.Error())
	}

	enabled := true
	if rawEnabled, exists := payload["enabled"]; exists && rawEnabled != nil {
		b, ok := parseLooseBool(rawEnabled)
		if !ok {
			return nil, NewAgentApiError("invalid_input", "enabled must be a boolean")
		}
		enabled = b
	}

	nextRunAt := ""
	if enabled {
		next, ok, nextErr := sessions.NextScheduleRun(norm, now)
		if nextErr != nil {
			return nil, NewAgentApiError("invalid_input", nextErr.Error())
		}
		if !ok {
			return nil, NewAgentApiError("invalid_input", "One-time schedule run_at must be in the future")
		}
		nextRunAt = next.UTC().Format(time.RFC3339)
	}

	item := store.SessionSchedule{
		AgentID:         aid,
		SessionID:       sid,
		Name:            sessions.DefaultScheduleName(payloadString(payload, "name"), prompt),
		Prompt:          prompt,
		Enabled:         enabled,
		ScheduleKind:    norm.ScheduleKind,
		CronExpr:        norm.CronExpr,
		IntervalMinutes: norm.IntervalMinutes,
		DailyTime:       norm.DailyTime,
		WeekdayMask:     norm.WeekdayMask,
		RunAt:           norm.RunAt,
		Timezone:        norm.Timezone,
		WhenBusy:        norm.WhenBusy,
		MisfirePolicy:   norm.MisfirePolicy,
		MisfireGraceSec: norm.MisfireGraceSec,
		NextRunAt:       nextRunAt,
	}

	created, err := h.options.Store.CreateSessionSchedule(ctx, item)
	if err != nil {
		return nil, fmt.Errorf("create session schedule: %w", err)
	}
	wire := h.publishAgentScheduleUpsert(ctx, created)
	if h.options.WakeScheduler != nil {
		h.options.WakeScheduler()
	}
	return map[string]any{
		"ok":          true,
		"session_key": aid + "::" + sid,
		"schedule":    wire,
	}, nil
}

func (h *Handler) invokeSchedulesUpdate(ctx context.Context, payload map[string]any) (any, error) {
	aid, sid, err := h.requireScheduleCaller(payload)
	if err != nil {
		return nil, err
	}
	scheduleID := strings.TrimSpace(payloadString(payload, "id"))
	if scheduleID == "" {
		scheduleID = strings.TrimSpace(payloadString(payload, "schedule_id"))
	}
	if scheduleID == "" {
		return nil, NewAgentApiError("invalid_input", "id is required")
	}

	existing, err := h.options.Store.GetSessionSchedule(ctx, scheduleID)
	if err != nil {
		return nil, fmt.Errorf("load session schedule: %w", err)
	}
	if existing == nil {
		return nil, NewAgentApiError("not_found", "Schedule was not found")
	}
	if existing.AgentID != aid || existing.SessionID != sid {
		return nil, NewAgentApiError("forbidden", "Schedule belongs to a different session and cannot be modified by this caller")
	}

	updated := *existing
	timingChanged := false
	enabledChanged := false
	now := time.Now().UTC()

	if _, has := payload["prompt"]; has {
		prompt := strings.TrimSpace(payloadString(payload, "prompt"))
		if prompt == "" {
			return nil, NewAgentApiError("invalid_input", "prompt cannot be empty")
		}
		if len(prompt) > maxAgentSchedulePromptLen {
			return nil, NewAgentApiError("invalid_input", "prompt is too long")
		}
		updated.Prompt = prompt
	}
	if _, has := payload["name"]; has {
		updated.Name = sessions.DefaultScheduleName(payloadString(payload, "name"), updated.Prompt)
	}
	if rawEnabled, has := payload["enabled"]; has && rawEnabled != nil {
		b, ok := parseLooseBool(rawEnabled)
		if !ok {
			return nil, NewAgentApiError("invalid_input", "enabled must be a boolean")
		}
		if b != updated.Enabled {
			enabledChanged = true
		}
		updated.Enabled = b
		if updated.Enabled {
			updated.ConsecutiveFailures = 0
			updated.PauseReason = ""
		}
	}

	if _, has := payload["schedule_kind"]; has {
		updated.ScheduleKind = strings.TrimSpace(payloadString(payload, "schedule_kind"))
		timingChanged = true
	}
	if _, has := payload["cron_expr"]; has {
		updated.CronExpr = strings.TrimSpace(payloadString(payload, "cron_expr"))
		if _, hasKind := payload["schedule_kind"]; !hasKind && updated.CronExpr != "" {
			updated.ScheduleKind = "cron"
		}
		timingChanged = true
	}
	if rawInt, has := payload["interval_minutes"]; has && rawInt != nil {
		v, ok := parseLooseInt(rawInt)
		if !ok {
			return nil, NewAgentApiError("invalid_input", "interval_minutes must be an integer")
		}
		updated.IntervalMinutes = v
		if _, hasKind := payload["schedule_kind"]; !hasKind && v > 0 {
			updated.ScheduleKind = "interval"
		}
		timingChanged = true
	}
	if _, has := payload["daily_time"]; has {
		updated.DailyTime = strings.TrimSpace(payloadString(payload, "daily_time"))
		if _, hasKind := payload["schedule_kind"]; !hasKind && updated.DailyTime != "" {
			if _, hasMask := payload["weekday_mask"]; hasMask {
				updated.ScheduleKind = "weekly"
			} else if _, hasDays := payload["weekdays"]; hasDays {
				updated.ScheduleKind = "weekly"
			} else if updated.ScheduleKind != "weekly" {
				updated.ScheduleKind = "daily"
			}
		}
		timingChanged = true
	}
	if rawMask, has := payload["weekday_mask"]; has && rawMask != nil {
		v, ok := parseLooseInt(rawMask)
		if !ok {
			return nil, NewAgentApiError("invalid_input", "weekday_mask must be an integer")
		}
		updated.WeekdayMask = v
		if _, hasKind := payload["schedule_kind"]; !hasKind && v > 0 {
			updated.ScheduleKind = "weekly"
		}
		timingChanged = true
	} else if rawDays, has := payload["weekdays"]; has && rawDays != nil {
		mask, parseErr := parseWeekdaysSpec(rawDays)
		if parseErr != nil {
			return nil, NewAgentApiError("invalid_input", parseErr.Error())
		}
		updated.WeekdayMask = mask
		if _, hasKind := payload["schedule_kind"]; !hasKind && mask > 0 {
			updated.ScheduleKind = "weekly"
		}
		timingChanged = true
	}
	if _, has := payload["run_at"]; has {
		updated.RunAt = strings.TrimSpace(payloadString(payload, "run_at"))
		if _, hasKind := payload["schedule_kind"]; !hasKind && updated.RunAt != "" {
			updated.ScheduleKind = "once"
		}
		timingChanged = true
	}
	if delaySec, hasDelay, delayErr := extractDelaySeconds(payload); delayErr != nil {
		return nil, NewAgentApiError("invalid_input", delayErr.Error())
	} else if hasDelay {
		updated.RunAt = now.Add(time.Duration(delaySec) * time.Second).UTC().Format(time.RFC3339)
		if _, hasKind := payload["schedule_kind"]; !hasKind {
			updated.ScheduleKind = "once"
		}
		timingChanged = true
	}
	if _, has := payload["timezone"]; has {
		updated.Timezone = strings.TrimSpace(payloadString(payload, "timezone"))
		timingChanged = true
	}
	if _, has := payload["when_busy"]; has {
		updated.WhenBusy = strings.TrimSpace(payloadString(payload, "when_busy"))
	}
	if _, has := payload["misfire_policy"]; has {
		updated.MisfirePolicy = strings.TrimSpace(payloadString(payload, "misfire_policy"))
	}
	if rawGrace, has := payload["misfire_grace_sec"]; has && rawGrace != nil {
		v, ok := parseLooseInt(rawGrace)
		if !ok {
			return nil, NewAgentApiError("invalid_input", "misfire_grace_sec must be an integer")
		}
		updated.MisfireGraceSec = v
	}

	norm, err := sessions.NormalizeScheduleSpec(sessions.ScheduleSpec{
		ScheduleKind:    updated.ScheduleKind,
		CronExpr:        updated.CronExpr,
		IntervalMinutes: updated.IntervalMinutes,
		DailyTime:       updated.DailyTime,
		WeekdayMask:     updated.WeekdayMask,
		RunAt:           updated.RunAt,
		Timezone:        updated.Timezone,
		WhenBusy:        updated.WhenBusy,
		MisfirePolicy:   updated.MisfirePolicy,
		MisfireGraceSec: updated.MisfireGraceSec,
	})
	if err != nil {
		return nil, NewAgentApiError("invalid_input", err.Error())
	}
	updated.ScheduleKind = norm.ScheduleKind
	updated.CronExpr = norm.CronExpr
	updated.IntervalMinutes = norm.IntervalMinutes
	updated.DailyTime = norm.DailyTime
	updated.WeekdayMask = norm.WeekdayMask
	updated.RunAt = norm.RunAt
	updated.Timezone = norm.Timezone
	updated.WhenBusy = norm.WhenBusy
	updated.MisfirePolicy = norm.MisfirePolicy
	updated.MisfireGraceSec = norm.MisfireGraceSec

	if !updated.Enabled {
		updated.NextRunAt = ""
	} else if timingChanged || enabledChanged || strings.TrimSpace(updated.NextRunAt) == "" {
		next, ok, nextErr := sessions.NextScheduleRun(norm, now)
		if nextErr != nil {
			return nil, NewAgentApiError("invalid_input", nextErr.Error())
		}
		if !ok {
			return nil, NewAgentApiError("invalid_input", "One-time schedule run_at must be in the future")
		}
		updated.NextRunAt = next.UTC().Format(time.RFC3339)
	}

	saved, err := h.options.Store.UpdateSessionSchedule(ctx, updated)
	if err != nil {
		return nil, fmt.Errorf("update session schedule: %w", err)
	}
	if saved == nil {
		return nil, NewAgentApiError("not_found", "Schedule was not found")
	}
	wire := h.publishAgentScheduleUpsert(ctx, *saved)
	if h.options.WakeScheduler != nil {
		h.options.WakeScheduler()
	}
	return map[string]any{
		"ok":          true,
		"session_key": aid + "::" + sid,
		"schedule":    wire,
	}, nil
}

func (h *Handler) invokeSchedulesDelete(ctx context.Context, payload map[string]any) (any, error) {
	aid, sid, err := h.requireScheduleCaller(payload)
	if err != nil {
		return nil, err
	}
	scheduleID := strings.TrimSpace(payloadString(payload, "id"))
	if scheduleID == "" {
		scheduleID = strings.TrimSpace(payloadString(payload, "schedule_id"))
	}
	if scheduleID == "" {
		return nil, NewAgentApiError("invalid_input", "id is required")
	}
	existing, err := h.options.Store.GetSessionSchedule(ctx, scheduleID)
	if err != nil {
		return nil, fmt.Errorf("load session schedule: %w", err)
	}
	if existing == nil {
		return nil, NewAgentApiError("not_found", "Schedule was not found")
	}
	if existing.AgentID != aid || existing.SessionID != sid {
		return nil, NewAgentApiError("forbidden", "Schedule belongs to a different session and cannot be deleted by this caller")
	}
	deleted, err := h.options.Store.DeleteSessionSchedule(ctx, scheduleID)
	if err != nil {
		return nil, fmt.Errorf("delete session schedule: %w", err)
	}
	if !deleted {
		return nil, NewAgentApiError("not_found", "Schedule was not found")
	}
	if h.options.Publisher != nil {
		_ = h.options.Publisher.Publish(ctx, "session_schedule.delete", map[string]any{
			"id":         existing.ID,
			"agent_id":   existing.AgentID,
			"session_id": existing.SessionID,
		})
	}
	return map[string]any{
		"ok":          true,
		"deleted":     true,
		"id":          existing.ID,
		"session_key": aid + "::" + sid,
	}, nil
}

func parseScheduleSpecFromPayload(payload map[string]any, now time.Time) (sessions.ScheduleSpec, error) {
	spec := sessions.ScheduleSpec{
		ScheduleKind:  strings.TrimSpace(payloadString(payload, "schedule_kind")),
		CronExpr:      strings.TrimSpace(payloadString(payload, "cron_expr")),
		DailyTime:     strings.TrimSpace(payloadString(payload, "daily_time")),
		RunAt:         strings.TrimSpace(payloadString(payload, "run_at")),
		Timezone:      strings.TrimSpace(payloadString(payload, "timezone")),
		WhenBusy:      strings.TrimSpace(payloadString(payload, "when_busy")),
		MisfirePolicy: strings.TrimSpace(payloadString(payload, "misfire_policy")),
	}
	if rawInt, ok := payload["interval_minutes"]; ok && rawInt != nil {
		v, valid := parseLooseInt(rawInt)
		if !valid {
			return spec, fmt.Errorf("interval_minutes must be an integer")
		}
		spec.IntervalMinutes = v
	}
	if rawMask, ok := payload["weekday_mask"]; ok && rawMask != nil {
		v, valid := parseLooseInt(rawMask)
		if !valid {
			return spec, fmt.Errorf("weekday_mask must be an integer")
		}
		spec.WeekdayMask = v
	} else if rawDays, ok := payload["weekdays"]; ok && rawDays != nil {
		mask, err := parseWeekdaysSpec(rawDays)
		if err != nil {
			return spec, err
		}
		spec.WeekdayMask = mask
	}
	if rawGrace, ok := payload["misfire_grace_sec"]; ok && rawGrace != nil {
		v, valid := parseLooseInt(rawGrace)
		if !valid {
			return spec, fmt.Errorf("misfire_grace_sec must be an integer")
		}
		spec.MisfireGraceSec = v
	}
	if delaySec, hasDelay, err := extractDelaySeconds(payload); err != nil {
		return spec, err
	} else if hasDelay {
		if spec.RunAt == "" {
			spec.RunAt = now.Add(time.Duration(delaySec) * time.Second).UTC().Format(time.RFC3339)
		}
		if spec.ScheduleKind == "" {
			spec.ScheduleKind = "once"
		}
	}

	if spec.ScheduleKind == "" {
		switch {
		case spec.RunAt != "":
			spec.ScheduleKind = "once"
		case spec.CronExpr != "":
			spec.ScheduleKind = "cron"
		case spec.IntervalMinutes > 0:
			spec.ScheduleKind = "interval"
		case spec.WeekdayMask > 0 && spec.DailyTime != "":
			spec.ScheduleKind = "weekly"
		case spec.DailyTime != "":
			spec.ScheduleKind = "daily"
		default:
			return spec, fmt.Errorf("schedule timing is required (provide delay_seconds/run_at, interval_minutes, daily_time, weekday_mask+daily_time, or cron_expr)")
		}
	}
	return spec, nil
}

func extractDelaySeconds(payload map[string]any) (int, bool, error) {
	if rawSec, ok := payload["delay_seconds"]; ok && rawSec != nil {
		sec, valid := parseLooseInt(rawSec)
		if !valid {
			return 0, false, fmt.Errorf("delay_seconds must be an integer")
		}
		if sec < minAgentScheduleDelaySec || sec > maxAgentScheduleDelaySec {
			return 0, false, fmt.Errorf("delay_seconds must be between %d and %d", minAgentScheduleDelaySec, maxAgentScheduleDelaySec)
		}
		return sec, true, nil
	}
	if rawMin, ok := payload["delay_minutes"]; ok && rawMin != nil {
		mins, valid := parseLooseInt(rawMin)
		if !valid {
			return 0, false, fmt.Errorf("delay_minutes must be an integer")
		}
		sec := mins * 60
		if sec < minAgentScheduleDelaySec || sec > maxAgentScheduleDelaySec {
			return 0, false, fmt.Errorf("delay_minutes must be between 1 and %d", maxAgentScheduleDelaySec/60)
		}
		return sec, true, nil
	}
	return 0, false, nil
}

func parseWeekdaysSpec(raw any) (int, error) {
	if mask, ok := parseLooseInt(raw); ok {
		if mask < 1 || mask > 127 {
			return 0, fmt.Errorf("weekdays bitmask must be between 1 and 127")
		}
		return mask, nil
	}
	var tokens []string
	switch v := raw.(type) {
	case string:
		trimmed := strings.ToLower(strings.TrimSpace(v))
		switch trimmed {
		case "weekdays", "workdays", "mon-fri":
			return 62, nil
		case "weekend", "weekends":
			return 65, nil
		case "daily", "everyday", "all":
			return 127, nil
		}
		for _, part := range strings.Split(trimmed, ",") {
			if p := strings.TrimSpace(part); p != "" {
				tokens = append(tokens, p)
			}
		}
	case []any:
		for _, item := range v {
			tokens = append(tokens, strings.ToLower(strings.TrimSpace(fmt.Sprint(item))))
		}
	case []string:
		for _, item := range v {
			tokens = append(tokens, strings.ToLower(strings.TrimSpace(item)))
		}
	default:
		return 0, fmt.Errorf("invalid weekdays value")
	}
	if len(tokens) == 0 {
		return 0, fmt.Errorf("weekdays cannot be empty")
	}
	nameToDay := map[string]int{
		"0": 0, "7": 0, "sun": 0, "sunday": 0, "周日": 0, "日": 0,
		"1": 1, "mon": 1, "monday": 1, "周一": 1, "一": 1,
		"2": 2, "tue": 2, "tues": 2, "tuesday": 2, "周二": 2, "二": 2,
		"3": 3, "wed": 3, "wednesday": 3, "周三": 3, "三": 3,
		"4": 4, "thu": 4, "thur": 4, "thurs": 4, "thursday": 4, "周四": 4, "四": 4,
		"5": 5, "fri": 5, "friday": 5, "周五": 5, "五": 5,
		"6": 6, "sat": 6, "saturday": 6, "周六": 6, "六": 6,
	}
	mask := 0
	for _, tok := range tokens {
		if strings.Contains(tok, "-") {
			parts := strings.Split(tok, "-")
			if len(parts) != 2 {
				return 0, fmt.Errorf("invalid weekday range %q", tok)
			}
			s, ok1 := nameToDay[strings.TrimSpace(parts[0])]
			e, ok2 := nameToDay[strings.TrimSpace(parts[1])]
			if !ok1 || !ok2 || s > e {
				return 0, fmt.Errorf("invalid weekday range %q", tok)
			}
			for d := s; d <= e; d++ {
				mask |= 1 << uint(d)
			}
			continue
		}
		day, ok := nameToDay[tok]
		if !ok {
			return 0, fmt.Errorf("invalid weekday %q", tok)
		}
		mask |= 1 << uint(day)
	}
	if mask == 0 {
		return 0, fmt.Errorf("weekdays matched no days")
	}
	return mask, nil
}

func payloadString(payload map[string]any, key string) string {
	v, ok := payload[key]
	if !ok || v == nil {
		return ""
	}
	s, ok := v.(string)
	if !ok {
		return ""
	}
	return s
}

func parseLooseInt(raw any) (int, bool) {
	switch v := raw.(type) {
	case int:
		return v, true
	case int64:
		return int(v), true
	case float64:
		if v != float64(int(v)) {
			return 0, false
		}
		return int(v), true
	case json.Number:
		i, err := strconv.Atoi(v.String())
		return i, err == nil
	case string:
		i, err := strconv.Atoi(strings.TrimSpace(v))
		return i, err == nil
	default:
		return 0, false
	}
}

func parseLooseBool(raw any) (bool, bool) {
	switch v := raw.(type) {
	case bool:
		return v, true
	case string:
		b, err := strconv.ParseBool(strings.TrimSpace(v))
		return b, err == nil
	default:
		return false, false
	}
}
