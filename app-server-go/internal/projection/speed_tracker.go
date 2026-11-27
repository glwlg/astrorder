package projection

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
	"sync"
	"time"

	"astrorder.dev/app-server/internal/store"
)

type speedTrackerKey struct{}

const (
	// SessionSpeedPrefVersion tags persisted session_speed preferences so legacy
	// v1 burst-only inflated readings (e.g. 2000-4700 tok/s) are invalidated
	// automatically without manual database edits.
	SessionSpeedPrefVersion = 2

	speedMinDuration     = 500 * time.Millisecond
	speedMaxModelStep    = 10 * time.Minute
	speedMinDeltaTokens  = 5
	speedMaxPlausibleTPS = 1000.0
	speedStaleTurnReset  = 30 * time.Second
)

type speedPhase int

const (
	phaseIdle speedPhase = iota
	phaseModel
	phaseWait
)

// tokenSpeedTracker measures live model generation throughput (tokens/s) per
// session across all agent runtimes (Antigravity, Codex, Grok, Hermes).
//
// It uses a 3-state phase machine (idle / model / wait):
//   - phaseModel runs whenever a turn is active and not blocked on tool
//     execution or user approval. This captures silent reasoning/thinking and
//     tool-call argument generation in addition to visible streaming deltas.
//   - phaseWait pauses the clock while any tool call or approval request is
//     in flight so external execution and user wait time never depress speed.
//   - phaseIdle stops the clock as soon as the turn finishes so post-turn
//     usage polling lag is excluded.
type tokenSpeedTracker struct {
	mu       sync.Mutex
	sessions map[string]*sessionSpeedState
}

type sessionSpeedState struct {
	phase         speedPhase
	phaseStart    time.Time
	accumDuration time.Duration
	lastActivity  time.Time

	inTurn                bool
	sawTurnStart          bool
	sawLiveOutput         bool
	untrackedToolWait     bool
	waitingApprovalStatus bool
	activeTools           map[string]struct{}
	activeApprovals       map[string]struct{}

	hasBaseline   bool
	lastGenerated int64
	lastSpeed     float64
}

func newTokenSpeedTracker() *tokenSpeedTracker {
	return &tokenSpeedTracker{
		sessions: make(map[string]*sessionSpeedState),
	}
}

func speedTrackerFromContext(ctx context.Context) *tokenSpeedTracker {
	if ctx == nil {
		return nil
	}
	t, _ := ctx.Value(speedTrackerKey{}).(*tokenSpeedTracker)
	return t
}

func activityTimeFromContext(ctx context.Context) time.Time {
	if ctx != nil {
		if ts, ok := ctx.Value(activityTimeKey{}).(time.Time); ok && !ts.IsZero() {
			return ts
		}
	}
	return time.Now().UTC()
}

// ParseSessionSpeedPref validates a persisted session_speed preference value.
// Legacy unversioned (v1) records and implausible values are ignored.
func ParseSessionSpeedPref(raw any) (float64, bool) {
	switch v := raw.(type) {
	case string:
		if strings.TrimSpace(v) == "" {
			return 0, false
		}
		var parsed map[string]any
		if err := json.Unmarshal([]byte(v), &parsed); err != nil {
			return 0, false
		}
		return ParseSessionSpeedPref(parsed)
	case map[string]any:
		ver, ok := parsePrefInt(v["v"])
		if !ok || ver < SessionSpeedPrefVersion {
			return 0, false
		}
		speed, err := strconv.ParseFloat(fmt.Sprint(v["speed"]), 64)
		if err != nil || math.IsNaN(speed) || math.IsInf(speed, 0) || speed <= 0 || speed > speedMaxPlausibleTPS {
			return 0, false
		}
		return math.Round(speed*10) / 10, true
	default:
		return 0, false
	}
}

func encodeSessionSpeedPref(tps float64) map[string]any {
	return map[string]any{
		"v":     SessionSpeedPrefVersion,
		"speed": math.Round(tps*10) / 10,
	}
}

func parsePrefInt(v any) (int, bool) {
	switch n := v.(type) {
	case int:
		return n, true
	case int64:
		return int(n), true
	case float64:
		return int(n), true
	case json.Number:
		parsed, err := n.Int64()
		if err == nil {
			return int(parsed), true
		}
	case string:
		parsed, err := strconv.Atoi(strings.TrimSpace(n))
		if err == nil {
			return parsed, true
		}
	}
	return 0, false
}

// generatedTokens returns the total generated output tokens (including thinking
// / reasoning tokens). All four integrated providers include reasoning inside
// output_tokens when both are present, so taking max avoids double-counting
// while still counting reasoning-only steps.
func generatedTokens(outputTokens, reasoningTokens int64) int64 {
	if outputTokens >= reasoningTokens {
		return outputTokens
	}
	return reasoningTokens
}

func (t *tokenSpeedTracker) sessionLocked(agentID, sessionID string) *sessionSpeedState {
	key := agentID + "\x00" + sessionID
	st, ok := t.sessions[key]
	if !ok {
		st = &sessionSpeedState{
			activeTools:     make(map[string]struct{}),
			activeApprovals: make(map[string]struct{}),
		}
		t.sessions[key] = st
	}
	return st
}

func (s *sessionSpeedState) flushModel(ts time.Time) {
	if s.phase != phaseModel {
		return
	}
	if !s.phaseStart.IsZero() && ts.After(s.phaseStart) {
		d := ts.Sub(s.phaseStart)
		if d <= speedMaxModelStep {
			s.accumDuration += d
		}
	}
	s.phaseStart = ts
}

func (s *sessionSpeedState) enterModel(ts time.Time) {
	if s.phase == phaseModel {
		s.flushModel(ts)
	} else {
		s.phase = phaseModel
		s.phaseStart = ts
	}
	s.inTurn = true
	if ts.After(s.lastActivity) || s.lastActivity.IsZero() {
		s.lastActivity = ts
	}
}

func (s *sessionSpeedState) enterWait(ts time.Time) {
	s.flushModel(ts)
	s.phase = phaseWait
	s.phaseStart = ts
	if ts.After(s.lastActivity) || s.lastActivity.IsZero() {
		s.lastActivity = ts
	}
}

func (s *sessionSpeedState) enterIdle(ts time.Time) {
	s.flushModel(ts)
	s.phase = phaseIdle
	s.phaseStart = time.Time{}
	s.inTurn = false
	s.untrackedToolWait = false
	s.waitingApprovalStatus = false
	clear(s.activeTools)
	clear(s.activeApprovals)
	if ts.After(s.lastActivity) || s.lastActivity.IsZero() {
		s.lastActivity = ts
	}
}

func (s *sessionSpeedState) isBlocked() bool {
	return len(s.activeTools) > 0 || len(s.activeApprovals) > 0 || s.untrackedToolWait || s.waitingApprovalStatus
}

func (s *sessionSpeedState) startTurn(ts time.Time, resetIfIdle bool) {
	if !s.inTurn {
		if s.accumDuration > 0 && !s.lastActivity.IsZero() && ts.Sub(s.lastActivity) > speedStaleTurnReset {
			s.accumDuration = 0
		}
		if resetIfIdle {
			s.untrackedToolWait = false
			s.waitingApprovalStatus = false
			clear(s.activeTools)
			clear(s.activeApprovals)
		}
		s.sawTurnStart = true
		s.enterModel(ts)
		return
	}
	s.sawTurnStart = true
	if !s.isBlocked() {
		s.enterModel(ts)
	}
}

func (s *sessionSpeedState) recordModelOutput(ts time.Time) {
	s.sawLiveOutput = true
	// Actual assistant output proves the model is actively generating now, so
	// clear any stale/backgrounded tool wait that might still be open.
	s.untrackedToolWait = false
	s.waitingApprovalStatus = false
	clear(s.activeTools)
	s.enterModel(ts)
}

func (s *sessionSpeedState) setToolState(key, status string, ts time.Time) {
	norm := normalizeSpeedToolStatus(status)
	switch norm {
	case "running":
		if key != "" {
			s.activeTools[key] = struct{}{}
		} else {
			s.untrackedToolWait = true
		}
		s.enterWait(ts)
	case "done":
		if key != "" {
			delete(s.activeTools, key)
		}
		s.untrackedToolWait = false
		if s.inTurn && !s.isBlocked() {
			s.enterModel(ts)
		}
	default:
		// Legacy/unstructured tool message without explicit status enters wait
		// until subsequent assistant output or explicit completion arrives.
		s.untrackedToolWait = true
		s.enterWait(ts)
	}
}

func (s *sessionSpeedState) setApprovalState(key, state string, ts time.Time) {
	norm := strings.ToLower(strings.TrimSpace(state))
	switch norm {
	case "", "pending", "requested", "waiting":
		if key != "" {
			s.activeApprovals[key] = struct{}{}
		} else {
			s.waitingApprovalStatus = true
		}
		s.enterWait(ts)
	default:
		if key != "" {
			delete(s.activeApprovals, key)
		}
		s.waitingApprovalStatus = false
		if s.inTurn && !s.isBlocked() {
			s.enterModel(ts)
		}
	}
}

// ObserveEvents inspects projected events from one daemon envelope and updates
// generation phase windows using the envelope's authoritative daemon timestamp.
func (t *tokenSpeedTracker) ObserveEvents(events []store.Event, ts time.Time) {
	if t == nil || len(events) == 0 {
		return
	}
	if ts.IsZero() {
		ts = time.Now().UTC()
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	for _, ev := range events {
		if ev.AgentID == "" || ev.SessionID == "" {
			continue
		}
		st := t.sessionLocked(ev.AgentID, ev.SessionID)

		switch ev.Type {
		case "message.delta":
			if isAssistantOutputEvent(ev.Data) {
				st.recordModelOutput(ts)
			}

		case "message.upsert":
			role, _ := ev.Data["role"].(string)
			kind, _ := ev.Data["kind"].(string)
			if role == "tool" || kind == "tool" {
				toolKey, toolStatus := extractToolMessageState(ev.Data)
				st.setToolState(toolKey, toolStatus, ts)
			} else if role == "user" {
				st.startTurn(ts, true)
			} else if isAssistantOutputEvent(ev.Data) {
				st.recordModelOutput(ts)
			}

		case "task.upsert":
			if isBlockingToolTask(ev.Data) {
				taskID, _ := ev.Data["id"].(string)
				status, _ := ev.Data["status"].(string)
				key := ""
				if taskID != "" {
					key = "task:" + taskID
				}
				st.setToolState(key, status, ts)
			}

		case "approval.upsert":
			appID, _ := ev.Data["id"].(string)
			state, _ := ev.Data["state"].(string)
			key := ""
			if appID != "" {
				key = "approval:" + appID
			}
			st.setApprovalState(key, state, ts)

		case "session.upsert":
			status := strings.ToLower(strings.TrimSpace(stringField(ev.Data, "status")))
			switch status {
			case "running":
				st.waitingApprovalStatus = false
				st.startTurn(ts, true)
			case "waiting_approval":
				st.waitingApprovalStatus = true
				st.enterWait(ts)
			case "idle", "error":
				st.enterIdle(ts)
			}

		case "session.active":
			st.waitingApprovalStatus = false
			st.startTurn(ts, true)

		case "session.idle", "session.error", "grok.completed", "hermes.command_complete":
			st.enterIdle(ts)

		case "command.upsert":
			state := strings.ToLower(strings.TrimSpace(stringField(ev.Data, "state")))
			switch state {
			case "running":
				st.waitingApprovalStatus = false
				st.startTurn(ts, true)
			case "completed", "failed", "cancelled":
				st.enterIdle(ts)
			}

		case "codex.notification":
			method := codexNotificationMethod(ev.Data)
			switch method {
			case "turn/started":
				st.waitingApprovalStatus = false
				st.startTurn(ts, true)
			case "turn/completed":
				st.enterIdle(ts)
			case "item/agentMessage/delta", "item/reasoning/delta", "item/reasoning/summaryTextDelta", "item/reasoning/textDelta", "item/plan/delta":
				st.recordModelOutput(ts)
			}

		case "grok.notification":
			switch grokSessionUpdateKind(ev.Data) {
			case "agent_message_chunk", "agent_thought_chunk":
				st.recordModelOutput(ts)
			case "turn_completed":
				st.enterIdle(ts)
			}
		}
	}
}

func stringField(data map[string]any, key string) string {
	if data == nil {
		return ""
	}
	v, _ := data[key].(string)
	return v
}

func isAssistantOutputEvent(data map[string]any) bool {
	if data == nil {
		return false
	}
	role, _ := data["role"].(string)
	if role != "assistant" {
		return false
	}
	kind, _ := data["kind"].(string)
	if kind != "" && kind != "message" && kind != "thinking" && kind != "plan" {
		return false
	}
	if text, _ := data["text"].(string); text != "" {
		return true
	}
	if thinking, _ := data["thinking"].(string); thinking != "" {
		return true
	}
	return false
}

func isBlockingToolTask(data map[string]any) bool {
	if data == nil {
		return false
	}
	kind := strings.ToLower(strings.TrimSpace(stringField(data, "kind")))
	if kind == "tool" {
		return true
	}
	if kind != "" {
		// Plan/todo/subagent rows are not synchronous single-tool waits.
		return false
	}
	id := stringField(data, "id")
	return strings.Contains(id, ":tool:")
}

func extractToolMessageState(data map[string]any) (string, string) {
	if data == nil {
		return "", ""
	}
	var toolID, status string
	if tool, ok := data["tool"].(map[string]any); ok {
		toolID = stringField(tool, "id")
		status = stringField(tool, "status")
	}
	if toolID == "" {
		toolID = stringField(data, "id")
	}
	if status == "" {
		status = stringField(data, "status")
	}
	if toolID == "" {
		return "", status
	}
	return "msgtool:" + toolID, status
}

func normalizeSpeedToolStatus(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "running", "in_progress", "pending", "queued", "started":
		return "running"
	case "completed", "done", "failed", "error", "cancelled", "canceled", "rejected", "declined", "aborted":
		return "done"
	default:
		return ""
	}
}

func codexNotificationMethod(data map[string]any) string {
	if data == nil {
		return ""
	}
	if m, _ := data["method"].(string); m != "" {
		return m
	}
	if frame, ok := data["frame"].(map[string]any); ok {
		if m, _ := frame["method"].(string); m != "" {
			return m
		}
	}
	return ""
}

func grokSessionUpdateKind(data map[string]any) string {
	if data == nil {
		return ""
	}
	frame := data
	if f, ok := data["frame"].(map[string]any); ok && f != nil {
		frame = f
	}
	params, _ := frame["params"].(map[string]any)
	if params == nil {
		return ""
	}
	update, _ := params["update"].(map[string]any)
	if update == nil {
		return ""
	}
	kind, _ := update["sessionUpdate"].(string)
	return kind
}

// ObserveUsage pairs accumulated active model-generation time with newly
// produced output tokens (including thinking/reasoning tokens) and returns
// tokens/sec.
func (t *tokenSpeedTracker) ObserveUsage(
	agentID, sessionID string,
	curGenerated, explicitDelta, dbPrevGenerated int64,
	hasCum bool,
	provider string,
	ts time.Time,
) (float64, bool) {
	if t == nil || agentID == "" || sessionID == "" {
		return 0, false
	}
	if !hasCum && explicitDelta <= 0 {
		return 0, false
	}
	if ts.IsZero() {
		ts = time.Now().UTC()
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	st := t.sessionLocked(agentID, sessionID)

	// Cold-start mid-turn guard: if app-server restarted after a session already
	// had persisted tokens in SQLite and we never witnessed the start of this
	// turn, establishing a clean baseline avoids dividing multi-minute pre-restart
	// token accumulation by a tiny post-restart observation window.
	if !st.hasBaseline && explicitDelta <= 0 && dbPrevGenerated > 0 && !st.sawTurnStart {
		if curGenerated > 0 {
			st.hasBaseline = true
			st.lastGenerated = curGenerated
		}
		st.flushModel(ts)
		st.accumDuration = 0
		return 0, false
	}

	observedLive := st.accumDuration >= speedMinDuration || st.sawTurnStart || st.sawLiveOutput || st.phase == phaseModel

	var delta int64
	switch {
	case explicitDelta > 0:
		delta = explicitDelta
	default:
		baseline := int64(0)
		if st.hasBaseline {
			baseline = st.lastGenerated
		} else if dbPrevGenerated > 0 {
			baseline = dbPrevGenerated
		}

		switch {
		case baseline > 0 && curGenerated > baseline:
			delta = curGenerated - baseline
		case baseline > 0 && strings.EqualFold(provider, "xai") && curGenerated > 0 && observedLive:
			// Grok ACP _meta.usage reports per-turn counts, which may be <= previous turn.
			delta = curGenerated
		case baseline == 0 && curGenerated > 0 && observedLive:
			// First reply in a newly active session with observed model activity.
			delta = curGenerated
		}
	}

	if curGenerated > 0 {
		st.hasBaseline = true
		st.lastGenerated = curGenerated
	}

	if delta <= 0 {
		return 0, false
	}

	// Flush active model generation right up to the usage timestamp, then
	// consume the accumulated duration for this token delta.
	st.flushModel(ts)
	genDuration := st.accumDuration
	st.accumDuration = 0

	if delta < speedMinDeltaTokens || genDuration < speedMinDuration {
		return 0, false
	}

	tps := math.Round((float64(delta)/genDuration.Seconds())*10) / 10
	if math.IsNaN(tps) || math.IsInf(tps, 0) || tps <= 0 || tps > speedMaxPlausibleTPS {
		return 0, false
	}
	st.lastSpeed = tps
	return tps, true
}
