package projection

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"path/filepath"
	"testing"
	"time"

	"astrorder.dev/app-server/internal/store"
	sessiondv1 "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
)

func makeTestEnvelope(eventType, agentID, sessionID string, ts time.Time, payload map[string]any) *sessiondv1.EventEnvelope {
	raw, _ := json.Marshal(payload)
	return &sessiondv1.EventEnvelope{
		EventType:           eventType,
		TimestampUnixMicros: ts.UnixMicro(),
		Session: &sessiondv1.SessionRef{
			AgentId:   &agentID,
			SessionId: sessionID,
		},
		NativePayload: &sessiondv1.NativeJson{
			Utf8Json: raw,
		},
	}
}

func newTestSpeedHarness(t *testing.T) (*store.Store, *Projection, func(*sessiondv1.EventEnvelope) []store.Event) {
	t.Helper()
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "speed-test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	p := &Projection{
		store:  st,
		speeds: newTokenSpeedTracker(),
	}
	project := func(env *sessiondv1.EventEnvelope) []store.Event {
		t.Helper()
		var evs []store.Event
		if err := st.WithTx(ctx, func(tx *sql.Tx) error {
			var e error
			evs, e = p.projectEnvelopeTx(ctx, tx, env)
			return e
		}); err != nil {
			t.Fatalf("project %s: %v", env.EventType, err)
		}
		return evs
	}
	return st, p, project
}

func TestTokenSpeedTrackerExcludesToolWaitAndPreservesOnPartialUpdate(t *testing.T) {
	ctx := context.Background()
	st, _, project := newTestSpeedHarness(t)

	aid := "local-antigravity"
	sid := "52794208-5f2e-4120-a6b1-99e5ee29c7f1"
	if _, err := st.UpsertSession(ctx, map[string]any{
		"id":         sid,
		"agent_id":   aid,
		"title":      "Speed Test",
		"status":     "idle",
		"updated_at": "2026-11-26T00:00:00Z",
	}); err != nil {
		t.Fatal(err)
	}

	t0 := time.Date(2026, 11, 26, 2, 0, 0, 0, time.UTC)

	// 1. Historical session baseline arrives cold (no turn/output observed): must NOT fabricate speed.
	evs := project(makeTestEnvelope("token.usage", aid, sid, t0, map[string]any{
		"agent_id": aid, "session_id": sid, "provider": "antigravity", "model": "gemini-4-neon",
		"date": "2026-11-26", "input_tokens": 50000, "output_tokens": 1000, "reasoning_tokens": 300,
		"total_tokens": 51000, "context_window": 1048576, "last_input_tokens": 20000,
	}))
	if len(evs) != 1 || evs[0].Data["speed"] != nil {
		t.Fatalf("historical baseline should not emit speed, got %+v", evs)
	}

	// 2. Live turn:
	// - Phase 1 (thinking + tool call prep): t+10.0s -> t+12.0s (2.0s)
	// - Tool execution wait:                 t+12.0s -> t+42.0s (30.0s — must be excluded!)
	// - Phase 2 (answer):                    t+42.0s -> t+45.0s (3.0s)
	// - Turn idle at t+45.0s; token.usage polled 0.5s later (post-idle lag excluded!)
	// Total pure model time = 5.0s; new output_tokens = +350 => 70.0 tok/s!
	project(makeTestEnvelope("connector.event", aid, sid, t0.Add(10*time.Second), map[string]any{
		"type": "message.upsert", "agent_id": aid, "session_id": sid,
		"data": map[string]any{"id": "m1", "role": "assistant", "kind": "message", "thinking": "thinking..."},
	}))
	project(makeTestEnvelope("connector.event", aid, sid, t0.Add(12*time.Second), map[string]any{
		"type": "message.upsert", "agent_id": aid, "session_id": sid,
		"data": map[string]any{"id": "tool1", "role": "tool", "kind": "tool", "text": "running shell"},
	}))
	project(makeTestEnvelope("connector.event", aid, sid, t0.Add(42*time.Second), map[string]any{
		"type": "message.upsert", "agent_id": aid, "session_id": sid,
		"data": map[string]any{"id": "m2", "role": "assistant", "kind": "message", "text": "Answer part 1"},
	}))
	project(makeTestEnvelope("connector.event", aid, sid, t0.Add(45*time.Second), map[string]any{
		"type": "session.upsert", "agent_id": aid, "session_id": sid,
		"data": map[string]any{"id": sid, "agent_id": aid, "status": "idle"},
	}))

	evs = project(makeTestEnvelope("token.usage", aid, sid, t0.Add(45*time.Second+500*time.Millisecond), map[string]any{
		"agent_id": aid, "session_id": sid, "provider": "antigravity", "model": "gemini-4-neon",
		"date": "2026-11-26", "input_tokens": 70000, "output_tokens": 1350, "reasoning_tokens": 450,
		"total_tokens": 71350, "context_window": 1048576, "last_input_tokens": 22000,
	}))
	if len(evs) != 1 || evs[0].Data["speed"] != 70.0 {
		t.Fatalf("expected 70.0 tok/s (excluding 30s tool wait and 0.5s post-idle lag), got %+v", evs[0].Data["speed"])
	}

	// 3. Partial context-only ACP update must preserve persisted v2 last_speed.
	evs = project(makeTestEnvelope("token.usage", aid, sid, t0.Add(46*time.Second), map[string]any{
		"agent_id": aid, "session_id": sid, "provider": "antigravity", "model": "gemini-4-neon",
		"date": "2026-11-26", "source": "acp_usage_update",
		"context_window": 1048576, "last_input_tokens": 25000,
	}))
	if len(evs) != 1 || evs[0].Data["speed"] != 70.0 {
		t.Fatalf("partial usage update lost speed: %+v", evs[0].Data["speed"])
	}

	pref, err := st.GetPreference(ctx, "session_speed:"+aid+"\x00"+sid)
	if err != nil || !pref.Exists {
		t.Fatalf("GetPreference speed: %+v err=%v", pref, err)
	}
	prefMap, _ := pref.Value.(map[string]any)
	if fmt.Sprint(prefMap["v"]) != "2" || fmt.Sprint(prefMap["speed"]) != "70" {
		t.Fatalf("persisted session_speed unexpected: %+v", pref.Value)
	}
}

func TestTokenSpeedTrackerAntigravitySilentThinkingAndToolArgGeneration(t *testing.T) {
	ctx := context.Background()
	st, _, project := newTestSpeedHarness(t)

	aid := "local-antigravity"
	sid := "34139e95-test"
	_, _ = st.UpsertSession(ctx, map[string]any{
		"id": sid, "agent_id": aid, "title": "AGY Silent Thinking", "status": "idle", "updated_at": "2026-11-26T00:00:00Z",
	})

	t0 := time.Date(2026, 11, 26, 4, 0, 0, 0, time.UTC)

	// Baseline from previous turn.
	project(makeTestEnvelope("token.usage", aid, sid, t0, map[string]any{
		"agent_id": aid, "session_id": sid, "provider": "antigravity", "model": "gemini-4-neon",
		"date": "2026-11-26", "input_tokens": 100000, "output_tokens": 10000, "reasoning_tokens": 4000, "total_tokens": 110000,
	}))

	// Reproduces real Antigravity turn:
	// - T+10s: user sends message -> session running (model starts thinking silently)
	// - T+25s: single agent_thought_chunk summary emitted (15s silent thinking!)
	// - T+35s: model emits 2 parallel tool calls after 10s of tool-arg generation!
	// - T+35s..T+55s: tool-1 finishes at T+45s, tool-2 finishes at T+55s (20s tool wait excluded!)
	// - T+55s..T+72s: model thinks silently (13s) + streams final answer over 4s (T+68s..T+72s)
	// - T+72s: session idle
	// Total model time = (35-10) + (72-55) = 25s + 17s = 42.0s.
	// Total output delta = +4200 tokens (reasoning + tool JSON + answer).
	// Expected = 4200 / 42.0 = 100.0 tok/s (old burst tracker gave 4200 / 4.0 = 1050.0 tok/s!).
	project(makeTestEnvelope("connector.event", aid, sid, t0.Add(10*time.Second), map[string]any{
		"type": "session.upsert", "agent_id": aid, "session_id": sid,
		"data": map[string]any{"id": sid, "agent_id": aid, "status": "running"},
	}))
	project(makeTestEnvelope("connector.event", aid, sid, t0.Add(25*time.Second), map[string]any{
		"type": "message.upsert", "agent_id": aid, "session_id": sid,
		"data": map[string]any{"id": "th-1", "role": "assistant", "kind": "message", "thinking": "Planning file edits..."},
	}))
	project(makeTestEnvelope("connector.event", aid, sid, t0.Add(35*time.Second), map[string]any{
		"type": "task.upsert", "agent_id": aid, "session_id": sid,
		"data": map[string]any{"id": "agy:tool:call-1", "kind": "tool", "title": "write_file", "status": "running"},
	}))
	project(makeTestEnvelope("connector.event", aid, sid, t0.Add(35*time.Second), map[string]any{
		"type": "task.upsert", "agent_id": aid, "session_id": sid,
		"data": map[string]any{"id": "agy:tool:call-2", "kind": "tool", "title": "run_command", "status": "running"},
	}))
	// Heartbeat session.upsert running while tools are active must NOT resume model timer!
	project(makeTestEnvelope("connector.event", aid, sid, t0.Add(40*time.Second), map[string]any{
		"type": "session.upsert", "agent_id": aid, "session_id": sid,
		"data": map[string]any{"id": sid, "agent_id": aid, "status": "running"},
	}))
	// First parallel tool finishes; second is still running -> stay in wait!
	project(makeTestEnvelope("connector.event", aid, sid, t0.Add(45*time.Second), map[string]any{
		"type": "task.upsert", "agent_id": aid, "session_id": sid,
		"data": map[string]any{"id": "agy:tool:call-1", "kind": "tool", "title": "write_file", "status": "completed"},
	}))
	// Second parallel tool finishes -> model resumes at T+55s!
	project(makeTestEnvelope("connector.event", aid, sid, t0.Add(55*time.Second), map[string]any{
		"type": "task.upsert", "agent_id": aid, "session_id": sid,
		"data": map[string]any{"id": "agy:tool:call-2", "kind": "tool", "title": "run_command", "status": "completed"},
	}))
	// Short visible final answer streams only during T+68s..T+72s.
	project(makeTestEnvelope("connector.event", aid, sid, t0.Add(68*time.Second), map[string]any{
		"type": "message.upsert", "agent_id": aid, "session_id": sid,
		"data": map[string]any{"id": "ans-1", "role": "assistant", "kind": "message", "text": "Done."},
	}))
	project(makeTestEnvelope("connector.event", aid, sid, t0.Add(72*time.Second), map[string]any{
		"type": "message.upsert", "agent_id": aid, "session_id": sid,
		"data": map[string]any{"id": "ans-1", "role": "assistant", "kind": "message", "text": "Done. All tests passed."},
	}))
	project(makeTestEnvelope("connector.event", aid, sid, t0.Add(72*time.Second), map[string]any{
		"type": "session.upsert", "agent_id": aid, "session_id": sid,
		"data": map[string]any{"id": sid, "agent_id": aid, "status": "idle"},
	}))

	evs := project(makeTestEnvelope("token.usage", aid, sid, t0.Add(73*time.Second), map[string]any{
		"agent_id": aid, "session_id": sid, "provider": "antigravity", "model": "gemini-4-neon",
		"date": "2026-11-26", "input_tokens": 140000, "output_tokens": 14200, "reasoning_tokens": 7000, "total_tokens": 154200,
	}))
	if len(evs) != 1 || evs[0].Data["speed"] != 100.0 {
		t.Fatalf("expected realistic 100.0 tok/s across silent thinking + tool arg generation, got %+v", evs)
	}
}

func TestTokenSpeedTrackerColdRestartMidTurnAndLegacyV1Ignored(t *testing.T) {
	ctx := context.Background()
	st, _, project := newTestSpeedHarness(t)

	aid := "local-antigravity"
	sid := "restart-sess"
	_, _ = st.UpsertSession(ctx, map[string]any{
		"id": sid, "agent_id": aid, "title": "Restart Test", "status": "running", "updated_at": "2026-11-26T00:00:00Z",
	})

	// Seed pre-restart token_metrics AND legacy unversioned inflated v1 session_speed preference.
	if _, err := st.UpsertTokenMetric(ctx, map[string]any{
		"agent_id": aid, "session_id": sid, "model": "gemini-4-neon", "provider": "antigravity",
		"date": "2026-11-26", "input_tokens": int64(80000), "output_tokens": int64(5000), "total_tokens": int64(85000),
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.SetPreference(ctx, "session_speed:"+aid+"\x00"+sid, map[string]any{"speed": 2086.6}, false); err != nil {
		t.Fatal(err)
	}

	t0 := time.Date(2026, 11, 26, 5, 0, 0, 0, time.UTC)

	// App restarted mid-turn: we only see the final 2s of output (NO turn start observed).
	project(makeTestEnvelope("connector.event", aid, sid, t0, map[string]any{
		"type": "message.upsert", "agent_id": aid, "session_id": sid,
		"data": map[string]any{"id": "tail-1", "role": "assistant", "kind": "message", "text": "final line"},
	}))
	project(makeTestEnvelope("connector.event", aid, sid, t0.Add(2*time.Second), map[string]any{
		"type": "session.upsert", "agent_id": aid, "session_id": sid,
		"data": map[string]any{"id": sid, "agent_id": aid, "status": "idle"},
	}))

	// Cumulative output jumped by +5000 (mostly pre-restart work): must establish baseline without emitting fake 2500 tok/s
	// AND must NOT fall back to legacy unversioned v1 2086.6!
	evs := project(makeTestEnvelope("token.usage", aid, sid, t0.Add(2*time.Second+100*time.Millisecond), map[string]any{
		"agent_id": aid, "session_id": sid, "provider": "antigravity", "model": "gemini-4-neon",
		"date": "2026-11-26", "input_tokens": 100000, "output_tokens": 10000, "total_tokens": 110000,
	}))
	if len(evs) != 1 || evs[0].Data["speed"] != nil {
		t.Fatalf("mid-turn restart should not emit inflated or legacy v1 speed, got %+v", evs[0].Data["speed"])
	}

	// Next full turn with approval pause:
	// - T+10s..T+15s (5s model)
	// - T+15s..T+45s (30s waiting_approval — excluded!)
	// - T+45s..T+50s (5s model)
	// - T+50s idle; +800 tokens => 80.0 tok/s!
	project(makeTestEnvelope("connector.event", aid, sid, t0.Add(10*time.Second), map[string]any{
		"type": "session.upsert", "agent_id": aid, "session_id": sid,
		"data": map[string]any{"id": sid, "agent_id": aid, "status": "running"},
	}))
	project(makeTestEnvelope("connector.event", aid, sid, t0.Add(15*time.Second), map[string]any{
		"type": "session.upsert", "agent_id": aid, "session_id": sid,
		"data": map[string]any{"id": sid, "agent_id": aid, "status": "waiting_approval"},
	}))
	project(makeTestEnvelope("connector.event", aid, sid, t0.Add(45*time.Second), map[string]any{
		"type": "session.upsert", "agent_id": aid, "session_id": sid,
		"data": map[string]any{"id": sid, "agent_id": aid, "status": "running"},
	}))
	project(makeTestEnvelope("connector.event", aid, sid, t0.Add(50*time.Second), map[string]any{
		"type": "session.upsert", "agent_id": aid, "session_id": sid,
		"data": map[string]any{"id": sid, "agent_id": aid, "status": "idle"},
	}))
	evs = project(makeTestEnvelope("token.usage", aid, sid, t0.Add(51*time.Second), map[string]any{
		"agent_id": aid, "session_id": sid, "provider": "antigravity", "model": "gemini-4-neon",
		"date": "2026-11-26", "input_tokens": 110000, "output_tokens": 10800, "total_tokens": 120800,
	}))
	if len(evs) != 1 || evs[0].Data["speed"] != 80.0 {
		t.Fatalf("expected 80.0 tok/s excluding 30s approval wait, got %+v", evs[0].Data["speed"])
	}
}

func TestTokenSpeedTrackerCodexAndGrok(t *testing.T) {
	ctx := context.Background()
	st, _, project := newTestSpeedHarness(t)

	t0 := time.Date(2026, 11, 26, 3, 0, 0, 0, time.UTC)

	// Codex turn with silent reasoning before summaryTextDelta + tool execution exclusion:
	// - T+0s: turn/started
	// - T+0s..T+6s: silent reasoning before summaryTextDelta arrives at T+6s (6s included!)
	// - T+6s..T+8s: reasoning summary + tool call emitted (2s included!)
	// - T+8s..T+28s: commandExecution tool runs (20s excluded!)
	// - T+28s..T+30s: final answer streams (2s included!)
	// Total model time = 8s + 2s = 10.0s; outputTokens = 800 -> 80.0 tok/s!
	codexAid, codexSid := "local-codex", "codex-sess-1"
	_, _ = st.UpsertSession(ctx, map[string]any{"id": codexSid, "agent_id": codexAid, "title": "Codex", "status": "idle", "updated_at": "2026-11-26T00:00:00Z"})
	project(makeTestEnvelope("codex.notification", codexAid, codexSid, t0, map[string]any{
		"agent_id": codexAid, "session_id": codexSid, "method": "turn/started",
		"params": map[string]any{"turnId": "turn-1"},
	}))
	project(makeTestEnvelope("codex.notification", codexAid, codexSid, t0.Add(6*time.Second), map[string]any{
		"agent_id": codexAid, "session_id": codexSid, "method": "item/reasoning/summaryTextDelta",
		"params": map[string]any{"itemId": "r1", "delta": "Checking files"},
	}))
	project(makeTestEnvelope("codex.notification", codexAid, codexSid, t0.Add(8*time.Second), map[string]any{
		"agent_id": codexAid, "session_id": codexSid, "method": "item/started",
		"params": map[string]any{"item": map[string]any{"id": "cmd-1", "type": "commandExecution", "status": "inProgress"}},
	}))
	project(makeTestEnvelope("codex.notification", codexAid, codexSid, t0.Add(28*time.Second), map[string]any{
		"agent_id": codexAid, "session_id": codexSid, "method": "item/completed",
		"params": map[string]any{"item": map[string]any{"id": "cmd-1", "type": "commandExecution", "status": "completed"}},
	}))
	project(makeTestEnvelope("codex.notification", codexAid, codexSid, t0.Add(29*time.Second), map[string]any{
		"agent_id": codexAid, "session_id": codexSid, "method": "item/agentMessage/delta",
		"params": map[string]any{"itemId": "i1", "delta": "Done."},
	}))
	codexEvs := project(makeTestEnvelope("codex.notification", codexAid, codexSid, t0.Add(30*time.Second), map[string]any{
		"agent_id": codexAid, "session_id": codexSid, "method": "thread/tokenUsage/updated",
		"params": map[string]any{
			"tokenUsage": map[string]any{
				"modelContextWindow": 200000,
				"last":               map[string]any{"inputTokens": 12000, "outputTokens": 800, "reasoningOutputTokens": 500},
				"total":              map[string]any{"inputTokens": 12000, "outputTokens": 800, "reasoningOutputTokens": 500, "totalTokens": 12800},
			},
		},
	}))
	if len(codexEvs) != 1 || codexEvs[0].Data["speed"] != 80.0 {
		t.Fatalf("expected Codex speed 80.0 tok/s, got %+v", codexEvs)
	}

	// Grok streaming chunks over 2.5s until grok.completed with per-turn outputTokens = 200 -> 80.0 tok/s.
	grokAid, grokSid := "local-grok", "grok-sess-1"
	_, _ = st.UpsertSession(ctx, map[string]any{"id": grokSid, "agent_id": grokAid, "title": "Grok", "status": "running", "updated_at": "2026-11-26T00:00:00Z"})
	project(makeTestEnvelope("grok.notification", grokAid, grokSid, t0, map[string]any{
		"agent_id": grokAid, "session_id": grokSid,
		"params": map[string]any{"update": map[string]any{"sessionUpdate": "agent_message_chunk", "content": "hi"}},
	}))
	project(makeTestEnvelope("grok.notification", grokAid, grokSid, t0.Add(2400*time.Millisecond), map[string]any{
		"agent_id": grokAid, "session_id": grokSid,
		"params": map[string]any{"update": map[string]any{"sessionUpdate": "agent_message_chunk", "content": " there"}},
	}))
	grokEvs := project(makeTestEnvelope("grok.completed", grokAid, grokSid, t0.Add(2500*time.Millisecond), map[string]any{
		"agent_id": grokAid, "session_id": grokSid, "model": "grok-4",
		"result": map[string]any{"_meta": map[string]any{"usage": map[string]any{"inputTokens": 5000, "outputTokens": 200, "totalTokens": 5200}}},
	}))
	var foundGrokSpeed float64
	for _, ev := range grokEvs {
		if ev.Type == "token.usage" {
			foundGrokSpeed, _ = ev.Data["speed"].(float64)
		}
	}
	if math.Abs(foundGrokSpeed-80.0) > 0.1 {
		t.Fatalf("expected Grok speed 80.0 tok/s, got %v", foundGrokSpeed)
	}
}
