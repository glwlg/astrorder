package projection

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
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

func TestTokenSpeedTrackerExcludesToolWaitAndPreservesOnPartialUpdate(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "speed-test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

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

	t0 := time.Date(2026, 11, 26, 2, 0, 0, 0, time.UTC)

	// 1. Historical session baseline arrives cold (no streaming chunks observed): must NOT fabricate speed.
	evs := project(makeTestEnvelope("token.usage", aid, sid, t0, map[string]any{
		"agent_id": aid, "session_id": sid, "provider": "antigravity", "model": "gemini-4-neon",
		"date": "2026-11-26", "input_tokens": 50000, "output_tokens": 1000, "reasoning_tokens": 300,
		"total_tokens": 51000, "context_window": 1048576, "last_input_tokens": 20000,
	}))
	if len(evs) != 1 || evs[0].Data["speed"] != nil {
		t.Fatalf("historical baseline should not emit speed, got %+v", evs)
	}

	// 2. Live turn:
	// - Burst 1 (thinking): t+10s -> t+12s (2.0s)
	// - Tool execution gap: t+12s -> t+42s (30.0s — must be excluded!)
	// - Burst 2 (answer):   t+42s -> t+45s (3.0s)
	// Total pure generation time = 5.0s; new output_tokens = +350 (includes reasoning) => 70.0 tok/s!
	project(makeTestEnvelope("connector.event", aid, sid, t0.Add(10*time.Second), map[string]any{
		"type": "message.upsert", "agent_id": aid, "session_id": sid,
		"data": map[string]any{"id": "m1", "role": "assistant", "kind": "message", "thinking": "thinking..."},
	}))
	project(makeTestEnvelope("connector.event", aid, sid, t0.Add(12*time.Second), map[string]any{
		"type": "message.upsert", "agent_id": aid, "session_id": sid,
		"data": map[string]any{"id": "m1", "role": "assistant", "kind": "message", "thinking": "thinking done"},
	}))
	project(makeTestEnvelope("connector.event", aid, sid, t0.Add(12*time.Second+100*time.Millisecond), map[string]any{
		"type": "message.upsert", "agent_id": aid, "session_id": sid,
		"data": map[string]any{"id": "tool1", "role": "tool", "kind": "tool", "text": "running shell"},
	}))
	project(makeTestEnvelope("connector.event", aid, sid, t0.Add(42*time.Second), map[string]any{
		"type": "message.upsert", "agent_id": aid, "session_id": sid,
		"data": map[string]any{"id": "m2", "role": "assistant", "kind": "message", "text": "Answer part 1"},
	}))
	project(makeTestEnvelope("connector.event", aid, sid, t0.Add(45*time.Second), map[string]any{
		"type": "message.upsert", "agent_id": aid, "session_id": sid,
		"data": map[string]any{"id": "m2", "role": "assistant", "kind": "message", "text": "Answer part 1 + part 2"},
	}))

	evs = project(makeTestEnvelope("token.usage", aid, sid, t0.Add(45*time.Second+50*time.Millisecond), map[string]any{
		"agent_id": aid, "session_id": sid, "provider": "antigravity", "model": "gemini-4-neon",
		"date": "2026-11-26", "input_tokens": 70000, "output_tokens": 1350, "reasoning_tokens": 450,
		"total_tokens": 71350, "context_window": 1048576, "last_input_tokens": 22000,
	}))
	if len(evs) != 1 || evs[0].Data["speed"] != 70.0 {
		t.Fatalf("expected 70.0 tok/s (excluding 30s tool wait), got %+v", evs[0].Data["speed"])
	}

	// 3. Partial context-only ACP update must preserve persisted last_speed.
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
	if fmt.Sprint(prefMap["speed"]) != "70" {
		t.Fatalf("persisted session_speed unexpected: %+v", pref.Value)
	}
}

func TestTokenSpeedTrackerCodexAndGrok(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "speed-codex-grok.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

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

	t0 := time.Date(2026, 11, 26, 3, 0, 0, 0, time.UTC)

	// Codex streaming deltas over 4.0s with last.outputTokens = 400 -> 100.0 tok/s.
	codexAid, codexSid := "local-codex", "codex-sess-1"
	_, _ = st.UpsertSession(ctx, map[string]any{"id": codexSid, "agent_id": codexAid, "title": "Codex", "status": "running", "updated_at": "2026-11-26T00:00:00Z"})
	project(makeTestEnvelope("codex.notification", codexAid, codexSid, t0, map[string]any{
		"agent_id": codexAid, "session_id": codexSid, "method": "item/agentMessage/delta",
		"params": map[string]any{"itemId": "i1", "delta": "hello "},
	}))
	project(makeTestEnvelope("codex.notification", codexAid, codexSid, t0.Add(4*time.Second), map[string]any{
		"agent_id": codexAid, "session_id": codexSid, "method": "item/agentMessage/delta",
		"params": map[string]any{"itemId": "i1", "delta": "world"},
	}))
	codexEvs := project(makeTestEnvelope("codex.notification", codexAid, codexSid, t0.Add(4*time.Second+50*time.Millisecond), map[string]any{
		"agent_id": codexAid, "session_id": codexSid, "method": "thread/tokenUsage/updated",
		"params": map[string]any{
			"tokenUsage": map[string]any{
				"modelContextWindow": 200000,
				"last":               map[string]any{"inputTokens": 12000, "outputTokens": 400, "reasoningOutputTokens": 150},
				"total":              map[string]any{"inputTokens": 12000, "outputTokens": 400, "reasoningOutputTokens": 150, "totalTokens": 12400},
			},
		},
	}))
	if len(codexEvs) != 1 || codexEvs[0].Data["speed"] != 100.0 {
		t.Fatalf("expected Codex speed 100.0 tok/s, got %+v", codexEvs)
	}

	// Grok streaming chunks over 2.5s with per-turn outputTokens = 200 -> 80.0 tok/s.
	grokAid, grokSid := "local-grok", "grok-sess-1"
	_, _ = st.UpsertSession(ctx, map[string]any{"id": grokSid, "agent_id": grokAid, "title": "Grok", "status": "running", "updated_at": "2026-11-26T00:00:00Z"})
	project(makeTestEnvelope("grok.notification", grokAid, grokSid, t0, map[string]any{
		"agent_id": grokAid, "session_id": grokSid,
		"params": map[string]any{"update": map[string]any{"sessionUpdate": "agent_message_chunk", "content": "hi"}},
	}))
	project(makeTestEnvelope("grok.notification", grokAid, grokSid, t0.Add(2500*time.Millisecond), map[string]any{
		"agent_id": grokAid, "session_id": grokSid,
		"params": map[string]any{"update": map[string]any{"sessionUpdate": "agent_message_chunk", "content": " there"}},
	}))
	grokEvs := project(makeTestEnvelope("grok.completed", grokAid, grokSid, t0.Add(2600*time.Millisecond), map[string]any{
		"agent_id": grokAid, "session_id": grokSid, "model": "grok-4",
		"result": map[string]any{"_meta": map[string]any{"usage": map[string]any{"inputTokens": 5000, "outputTokens": 200, "totalTokens": 5200}}},
	}))
	var foundGrokSpeed any
	for _, ev := range grokEvs {
		if ev.Type == "token.usage" {
			foundGrokSpeed = ev.Data["speed"]
		}
	}
	if foundGrokSpeed != 80.0 {
		t.Fatalf("expected Grok speed 80.0 tok/s, got %+v", foundGrokSpeed)
	}
}
