package projection

import (
	"context"
	"math"
	"strings"
	"sync"
	"time"

	"astrorder.dev/app-server/internal/store"
)

type speedTrackerKey struct{}

const (
	speedBurstGapThreshold = 4 * time.Second
	speedMinDuration       = 100 * time.Millisecond
	speedMaxFallbackWindow = 10 * time.Minute
	speedMinDeltaTokens    = 5
	speedMaxPlausibleTPS   = 5000.0
	speedStaleTurnReset    = 30 * time.Second
)

// tokenSpeedTracker measures live model generation throughput (tokens/s) per
// session across all agent runtimes (Antigravity, Codex, Grok, Hermes).
//
// It accumulates only active streaming generation bursts (assistant text,
// thinking, and plan deltas) and closes the burst whenever a tool execution,
// approval pause, or inter-chunk gap (>4s) occurs so tool/approval wait time
// never depresses measured throughput.
type tokenSpeedTracker struct {
	mu       sync.Mutex
	sessions map[string]*sessionSpeedState
}

type sessionSpeedState struct {
	inBurst       bool
	burstStart    time.Time
	burstLast     time.Time
	burstFrames   int
	accumDuration time.Duration
	turnStart     time.Time
	lastActivity  time.Time

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

func (s *sessionSpeedState) closeBurst() {
	if !s.inBurst {
		return
	}
	if d := s.burstLast.Sub(s.burstStart); d > 0 {
		s.accumDuration += d
	}
	s.inBurst = false
}

func (t *tokenSpeedTracker) sessionLocked(agentID, sessionID string) *sessionSpeedState {
	key := agentID + "\x00" + sessionID
	st, ok := t.sessions[key]
	if !ok {
		st = &sessionSpeedState{}
		t.sessions[key] = st
	}
	return st
}

// ObserveEvents inspects projected events from one daemon envelope and updates
// generation burst windows using the envelope's authoritative daemon timestamp.
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
		switch ev.Type {
		case "message.delta":
			if isAssistantOutputEvent(ev.Data) {
				t.recordChunkLocked(ev.AgentID, ev.SessionID, ts)
			}

		case "message.upsert":
			role, _ := ev.Data["role"].(string)
			kind, _ := ev.Data["kind"].(string)
			if role == "tool" || kind == "tool" {
				t.sessionLocked(ev.AgentID, ev.SessionID).closeBurst()
			} else if role == "user" {
				t.recordTurnStartLocked(ev.AgentID, ev.SessionID, ts)
			} else if isAssistantOutputEvent(ev.Data) {
				t.recordChunkLocked(ev.AgentID, ev.SessionID, ts)
			}

		case "task.upsert", "approval.upsert", "session.idle", "grok.completed", "hermes.command_complete":
			t.sessionLocked(ev.AgentID, ev.SessionID).closeBurst()

		case "session.active":
			t.recordTurnStartLocked(ev.AgentID, ev.SessionID, ts)

		case "command.upsert":
			state, _ := ev.Data["state"].(string)
			switch state {
			case "running":
				t.recordTurnStartLocked(ev.AgentID, ev.SessionID, ts)
			case "completed", "failed", "cancelled":
				t.sessionLocked(ev.AgentID, ev.SessionID).closeBurst()
			}

		case "codex.notification":
			method := codexNotificationMethod(ev.Data)
			switch method {
			case "turn/started":
				t.recordTurnStartLocked(ev.AgentID, ev.SessionID, ts)
			case "turn/completed":
				t.sessionLocked(ev.AgentID, ev.SessionID).closeBurst()
			}
		}
	}
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

func (t *tokenSpeedTracker) recordTurnStartLocked(agentID, sessionID string, ts time.Time) {
	st := t.sessionLocked(agentID, sessionID)
	st.closeBurst()
	if st.accumDuration > 0 && !st.lastActivity.IsZero() && ts.Sub(st.lastActivity) > speedStaleTurnReset {
		st.accumDuration = 0
		st.burstFrames = 0
	}
	st.turnStart = ts
	st.lastActivity = ts
}

func (t *tokenSpeedTracker) recordChunkLocked(agentID, sessionID string, ts time.Time) {
	st := t.sessionLocked(agentID, sessionID)
	if st.inBurst {
		if ts.After(st.burstLast) && ts.Sub(st.burstLast) > speedBurstGapThreshold {
			st.closeBurst()
			st.inBurst = true
			st.burstStart = ts
			st.burstLast = ts
		} else if ts.After(st.burstLast) {
			st.burstLast = ts
		}
		st.burstFrames++
	} else {
		st.inBurst = true
		st.burstStart = ts
		st.burstLast = ts
		st.burstFrames++
	}
	if st.turnStart.IsZero() {
		st.turnStart = ts
	}
	st.lastActivity = ts
}

// ObserveUsage pairs accumulated streaming generation time with newly produced
// output tokens (including thinking/reasoning tokens) and returns tokens/sec.
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
	st.closeBurst()

	observedLive := st.accumDuration >= speedMinDuration || st.burstFrames > 0 || !st.turnStart.IsZero()

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
			// First reply in a newly active session with observed streaming output.
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

	genDuration := st.accumDuration
	if genDuration < speedMinDuration && !st.turnStart.IsZero() && ts.After(st.turnStart) {
		fallback := ts.Sub(st.turnStart)
		if fallback >= speedMinDuration && fallback <= speedMaxFallbackWindow && st.burstFrames > 0 {
			genDuration = fallback
		}
	}

	// Consume the generation window now that we have observed the corresponding token delta.
	st.accumDuration = 0
	st.burstFrames = 0
	st.turnStart = time.Time{}

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
