package learning

import (
	"context"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"astrorder.dev/app-server/internal/bots"
	"astrorder.dev/app-server/internal/store"
	"astrorder.dev/app-server/internal/targetfs"
)

func TestSchedulerSkipsWhenPluginDisabledAndResumesWhenReenabled(t *testing.T) {
	tmp := t.TempDir()
	st, err := store.Open(filepath.Join(tmp, "app.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()
	ctx := context.Background()

	// Friday 2026-11-27 10:00 UTC -> NextRunAt defaults to Sunday 2026-11-29 20:00 UTC.
	nowRef := time.Date(2026, 11, 27, 10, 0, 0, 0, time.UTC)

	_, _ = st.UpsertAgent(ctx, map[string]any{
		"id": "codex-local", "kind": "codex", "name": "Codex", "status": "ready", "capabilities": []string{"chat"},
	})

	resolver := targetfs.NewResolver(targetfs.ResolverOptions{Store: st, DefaultWorkspace: tmp})
	var spawns atomic.Int32
	botSvc := bots.NewService(bots.Options{
		Store:    st,
		Resolver: resolver,
		Spawner: func(_ context.Context, req bots.SpawnRequest) (*bots.SpawnResult, error) {
			spawns.Add(1)
			return &bots.SpawnResult{
				Session: map[string]any{
					"id":       "bot-sess-1",
					"agent_id": req.AgentID,
					"key":      req.AgentID + "::bot-sess-1",
					"title":    req.Title,
				},
			}, nil
		},
	})

	svc := NewService(Options{
		Store:            st,
		Resolver:         resolver,
		Bots:             botSvc,
		DefaultWorkspace: tmp,
		Location:         time.UTC,
		Now:              func() time.Time { return nowRef },
	})

	// Bootstrap Mentor Bot and save enabled weekly schedule.
	boot, err := svc.BootstrapMentorBot(ctx, BootstrapMentorBotRequest{Workspace: filepath.Join(tmp, "mentor")})
	if err != nil {
		t.Fatalf("BootstrapMentorBot: %v", err)
	}
	cfg := boot.Config
	cfg.Enabled = true
	cfg.Timezone = "UTC"
	cfg.MinActivitySessions = 1
	cfg.MinActivityCommits = 0
	saved, err := svc.repo.SaveConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}
	nextRun, err := time.Parse(time.RFC3339, saved.NextRunAt)
	if err != nil {
		t.Fatalf("parse NextRunAt %q: %v", saved.NextRunAt, err)
	}

	// Advance clock to 1 minute after scheduled NextRunAt and seed recent activity.
	nowRef = nextRun.Add(1 * time.Minute)
	_, _ = st.UpsertSession(ctx, map[string]any{
		"id": "sess-1", "agent_id": "codex-local", "title": "Recent session",
		"workspace": tmp, "project_name": "demo", "status": "idle",
		"updated_at": nowRef.Add(-1 * time.Hour).Format(time.RFC3339),
	})

	// 1. Disable workspace-learning-radar plugin -> tick MUST skip cleanly!
	if err := st.SetPreference(ctx, store.PrefPluginsEnabled, map[string]any{PluginID: false}, true); err != nil {
		t.Fatalf("disable plugin: %v", err)
	}
	svc.tick(ctx)
	svc.Wait()
	if got := spawns.Load(); got != 0 {
		t.Fatalf("expected 0 scheduled spawns while %s is disabled, got %d", PluginID, got)
	}
	runs, err := svc.repo.ListRuns(ctx)
	if err != nil {
		t.Fatalf("ListRuns: %v", err)
	}
	if len(runs) != 0 {
		t.Fatalf("expected 0 scan runs while disabled, got %d", len(runs))
	}

	// 2. Re-enable workspace-learning-radar plugin -> next tick resumes automatically!
	if err := st.SetPreference(ctx, store.PrefPluginsEnabled, map[string]any{PluginID: true}, true); err != nil {
		t.Fatalf("re-enable plugin: %v", err)
	}
	svc.tick(ctx)
	svc.Wait()
	if got := spawns.Load(); got != 1 {
		t.Fatalf("expected scheduled scan to resume after re-enabling %s, spawns=%d", PluginID, got)
	}
}
