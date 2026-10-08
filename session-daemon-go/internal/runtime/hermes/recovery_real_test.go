package hermes

import (
	core "astrorder.dev/session-daemon/internal/runtime"
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Seed through the real gateway's documented create-time history path. This
// creates a persisted disposable fixture without any model inference.
func TestRealHermesPersistedSessionRecoversThroughRegistry(t *testing.T) {
	executable, source := os.Getenv("ASTRORDER_REAL_HERMES_PYTHON"), os.Getenv("ASTRORDER_REAL_HERMES_SOURCE")
	if executable == "" || source == "" {
		t.Skip("explicit native Hermes executable/source required")
	}
	home, workspace := t.TempDir(), t.TempDir()
	cfg := Config{Executable: executable, Arguments: []string{"-m", "tui_gateway.entry"}, Environment: []string{"HERMES_HOME=" + home, "PYTHONPATH=" + source, "HERMES_DISABLE_AUTO_UPDATE=1", "HERMES_DISABLE_LAZY_INSTALLS=1"}, Workspace: workspace, Allowed: []string{workspace}, StateDBPath: filepath.Join(home, "state.db")}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	seeder := New(cfg)
	defer seeder.Close()
	if err := seeder.startProcess(ctx); err != nil {
		t.Fatal(err)
	}
	seeded, err := seeder.rpc(ctx, "session.create", map[string]any{"source": "local", "cwd": workspace, "title": "Go persisted recovery fixture", "messages": []any{map[string]any{"role": "user", "content": "Disposable Go native recovery fixture"}}})
	if err != nil {
		t.Fatal(err)
	}
	id, ok := seeded["stored_session_id"].(string)
	if !ok || id == "" {
		t.Fatal("native seed did not return durable identity")
	}
	if err := seeder.Close(); err != nil {
		t.Fatal(err)
	}
	pool := NewIsolated(cfg)
	defer pool.Close()
	registry := core.NewRegistry()
	registry.Register("hermes", pool)
	req := core.Request{AgentType: "hermes", SessionID: id}
	if _, err = registry.Spawn(ctx, req); err != nil {
		t.Fatal(err)
	}
	first := pool.workers[id]
	pid := first.cmd.Process.Pid
	if err = first.cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-first.processDone:
	case <-ctx.Done():
		t.Fatal("old native owner did not exit")
	}
	select {
	case <-first.eventDone:
	case <-ctx.Done():
		t.Fatal("old native projection did not retire")
	}
	res, err := registry.Spawn(ctx, req)
	if err != nil {
		t.Fatal("persisted native recovery failed", err)
	}
	if res.Status != "idle" || pool.workers[id].cmd.Process.Pid == pid {
		t.Fatal("recovery returned stale process or status")
	}
	history, err := registry.Command(ctx, core.Request{SessionID: id, Action: "session.history_page", Fields: map[string]any{"limit": 200}})
	if err != nil {
		t.Fatal(err)
	}
	items, ok := history.Payload["items"].([]map[string]any)
	if !ok || len(items) != 1 {
		t.Fatal("persisted native history missing", history.Payload)
	}
	if items[0]["role"] != "user" || items[0]["content"] != "Disposable Go native recovery fixture" {
		t.Fatal("native transcript changed on recovery")
	}
	if _, err = registry.Command(ctx, core.Request{SessionID: id, Action: "session.rename", Fields: map[string]any{"title": "Recovered persisted fixture"}}); err != nil {
		t.Fatal(err)
	}
}
