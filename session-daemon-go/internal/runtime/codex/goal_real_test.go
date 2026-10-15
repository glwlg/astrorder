package codex

import (
	core "astrorder.dev/session-daemon/internal/runtime"
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"
)

func TestRealCodexGoalPauseResume(t *testing.T) {
	executable := os.Getenv("ASTRORDER_REAL_CODEX")
	if executable == "" {
		t.Skip("ASTRORDER_REAL_CODEX is not configured")
	}
	workspace, home := t.TempDir(), t.TempDir()
	a := NewAdapter(Config{Executable: executable, Workspace: workspace, Allowed: []string{workspace}, AgentID: "goal-real", Environment: []string{"CODEX_HOME=" + home}}, nil)
	defer a.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	created, err := a.Create(ctx, core.Request{Fields: map[string]any{"cwd": workspace}})
	if err != nil {
		t.Fatal(err)
	}
	a.mu.Lock()
	s := a.sessions[created.SessionID]
	a.mu.Unlock()
	if s == nil {
		t.Fatal("created session not owned")
	}
	if _, err := s.runtime.request(ctx, "thread/goal/set", map[string]any{"threadId": created.SessionID, "objective": "isolated lifecycle validation", "status": "active", "tokenBudget": 500, "origin": "user"}); err != nil {
		t.Fatal(err)
	}
	for _, state := range []string{"paused", "active"} {
		result, err := a.Command(ctx, core.Request{SessionID: created.SessionID, Action: "session.goal.set", Fields: map[string]any{"status": state}})
		if err != nil {
			t.Fatal(state, err)
		}
		goal, _ := result.Payload["goal"].(map[string]any)
		if goal["status"] != state || goal["objective"] != "isolated lifecycle validation" || goal["tokenBudget"] != json.Number("500") {
			t.Fatal("native state lost", goal)
		}
		t.Logf("real Codex goal %s confirmed; objective and budget preserved", state)
	}
	if _, err := a.Command(ctx, core.Request{SessionID: created.SessionID, Action: "session.goal.clear"}); err != nil {
		t.Fatal(err)
	}
}
