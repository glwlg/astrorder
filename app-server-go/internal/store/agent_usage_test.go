package store

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestAgentUsageWindowsAndPersistence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.db")
	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := st.UpsertAgent(ctx, map[string]any{"id": "codex-1", "kind": "codex", "name": "Codex", "status": "ready"}); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	items := []AgentInvocation{
		{Capability: "sessions.list", AgentID: "codex-1", Runtime: "codex", Success: true, Duration: 20 * time.Millisecond, OccurredAt: now.Add(-time.Hour)},
		{Capability: "sessions.read", AgentID: "codex-1", Runtime: "codex", Success: false, Duration: 2 * time.Second, OccurredAt: now.Add(-24 * time.Hour)},
		{Capability: "catalog.list", Success: true, OccurredAt: now.Add(-24*time.Hour - time.Millisecond)},
		{Capability: "browser.run", Success: true, OccurredAt: now.Add(-8 * 24 * time.Hour)},
	}
	for _, item := range items {
		if err := st.RecordAgentInvocation(ctx, item); err != nil {
			t.Fatal(err)
		}
	}
	for _, test := range []struct {
		period        string
		calls, failed int64
	}{{"24h", 2, 1}, {"7d", 3, 1}, {"30d", 4, 1}, {"all", 4, 1}} {
		report, err := st.AgentUsage(ctx, test.period, now)
		if err != nil || report.Summary.Calls != test.calls || report.Summary.Failed != test.failed || report.Summary.ActiveAgents != 1 {
			t.Fatalf("range %s: %#v, %v", test.period, report.Summary, err)
		}
	}
	if _, err := st.AgentUsage(ctx, "invalid", now); err == nil {
		t.Fatal("accepted invalid range")
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	st, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	report, err := st.AgentUsage(ctx, "24h", now)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Agents) != 1 || report.Agents[0].Name != "Codex" || report.Agents[0].Calls != 2 || len(report.Recent) != 2 || report.Recent[1].DurationMS != 2000 {
		t.Fatalf("persisted report: %#v", report)
	}
}

func TestAgentUsageSchemaUpgradePreservesData(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.db")
	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := st.SetPreference(ctx, "upgrade-check", "preserved", true); err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB().Exec(`DROP TABLE agent_invocation_usage`); err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB().Exec(`PRAGMA user_version = 5`); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	st, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	pref, err := st.GetPreference(ctx, "upgrade-check")
	if err != nil || pref.Value != "preserved" {
		t.Fatalf("upgrade lost data: %#v %v", pref, err)
	}
	if err := st.RecordAgentInvocation(ctx, AgentInvocation{Capability: "catalog.list", Success: true, OccurredAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
}

func TestAgentUsageConcurrentCallsAreAllRecorded(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	const calls = 80
	failures := make(chan error, calls)
	var wg sync.WaitGroup
	for range calls {
		wg.Go(func() {
			failures <- st.RecordAgentInvocation(context.Background(), AgentInvocation{
				Capability: "sessions.read", AgentID: "agent-a", Runtime: "codex", Success: true, OccurredAt: time.Now(),
			})
		})
	}
	wg.Wait()
	close(failures)
	for err := range failures {
		if err != nil {
			t.Fatal(err)
		}
	}
	report, err := st.AgentUsage(context.Background(), "all", time.Now())
	if err != nil || report.Summary.Calls != calls || report.Summary.Succeeded != calls {
		t.Fatalf("concurrent usage: %#v %v", report.Summary, err)
	}
}
