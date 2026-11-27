package learning

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"astrorder.dev/app-server/internal/auth"
	"astrorder.dev/app-server/internal/bots"
	"astrorder.dev/app-server/internal/store"
	"astrorder.dev/app-server/internal/targetfs"
)

type allowBrowserAuth struct{}

func (allowBrowserAuth) AuthorizeBrowser(*http.Request) *auth.Error { return nil }

type recordingPublisher struct {
	mu     sync.Mutex
	events []string
}

func (p *recordingPublisher) Publish(_ context.Context, eventType string, _ map[string]any) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.events = append(p.events, eventType)
	return nil
}

func TestWeeklyScheduleAndMisfireCatchup(t *testing.T) {
	loc := time.FixedZone("CST", 8*3600)
	// Friday 2026-11-27 10:00 CST -> next Sunday is 2026-11-29 20:00 CST
	fri := time.Date(2026, 11, 27, 10, 0, 0, 0, loc)
	cfg := DefaultConfig(fri, loc)
	nextRun, err := time.Parse(time.RFC3339, cfg.NextRunAt)
	if err != nil {
		t.Fatalf("parse next_run_at: %v", err)
	}
	want := time.Date(2026, 11, 29, 20, 0, 0, 0, loc).UTC()
	if !nextRun.Equal(want) {
		t.Fatalf("nextRun = %s, want %s", nextRun, want)
	}

	svc := NewService(Options{Location: loc})
	// 12 hours after scheduled Sunday 20:00 (Monday 08:00) -> within 48h catchup window
	due, missed := svc.isScheduleDue(cfg, want.Add(12*time.Hour))
	if !due || missed {
		t.Fatalf("expected catch-up due=true missed=false, got due=%v missed=%v", due, missed)
	}
	// 60 hours after scheduled Sunday 20:00 -> beyond 48h catchup window
	due, missed = svc.isScheduleDue(cfg, want.Add(60*time.Hour))
	if due || !missed {
		t.Fatalf("expected expired due=false missed=true, got due=%v missed=%v", due, missed)
	}
}

func TestBotDrivenLearningLifecycle(t *testing.T) {
	tmp := t.TempDir()
	st, err := store.Open(filepath.Join(tmp, "app.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()
	ctx := context.Background()

	projectDir := filepath.Join(tmp, "demo-project")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatal(err)
	}
	mentorDir := filepath.Join(tmp, "learning-mentor")

	_, _ = st.UpsertAgent(ctx, map[string]any{
		"id": "codex-local", "kind": "codex", "name": "Codex", "status": "ready", "capabilities": []string{"chat"},
	})
	_, _ = st.UpsertSession(ctx, map[string]any{
		"id": "sess-1", "agent_id": "codex-local", "title": "排查构建误报",
		"workspace": projectDir, "project_name": "demo-project", "status": "idle",
		"updated_at": time.Now().UTC().Add(-2 * time.Hour).Format(time.RFC3339),
	})

	resolver := targetfs.NewResolver(targetfs.ResolverOptions{Store: st, DefaultWorkspace: tmp})
	var spawnedReqs []bots.SpawnRequest
	botSvc := bots.NewService(bots.Options{
		Store:    st,
		Resolver: resolver,
		Spawner: func(_ context.Context, req bots.SpawnRequest) (*bots.SpawnResult, error) {
			spawnedReqs = append(spawnedReqs, req)
			sid := "bot-sess-" + randomHex(3)
			return &bots.SpawnResult{
				Session: map[string]any{
					"id":       sid,
					"agent_id": req.AgentID,
					"key":      req.AgentID + "::" + sid,
					"title":    req.Title,
				},
			}, nil
		},
	})

	pub := &recordingPublisher{}
	nowRef := time.Now().UTC()
	svc := NewService(Options{
		Store:            st,
		Authorizer:       allowBrowserAuth{},
		Resolver:         resolver,
		Publisher:        pub,
		Bots:             botSvc,
		DefaultWorkspace: tmp,
		Now:              func() time.Time { return nowRef },
	})

	// 1. Bootstrap official Learning Mentor Bot via HTTP
	bootBody, _ := json.Marshal(BootstrapMentorBotRequest{Workspace: mentorDir})
	req := httptest.NewRequest(http.MethodPost, BasePath+"/mentor-bot/bootstrap", bytes.NewReader(bootBody))
	w := httptest.NewRecorder()
	svc.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("bootstrap status=%d body=%s", w.Code, w.Body.String())
	}
	var bootResp BootstrapMentorBotResponse
	if err := json.Unmarshal(w.Body.Bytes(), &bootResp); err != nil {
		t.Fatalf("decode bootstrap: %v", err)
	}
	if !bootResp.Created || bootResp.Bot.Slug != defaultMentorBotSlug || bootResp.Config.MentorBotID != bootResp.Bot.ID {
		t.Fatalf("unexpected bootstrap response: %+v", bootResp)
	}
	if _, err := os.Stat(filepath.Join(mentorDir, "AGENTS.md")); err != nil {
		t.Fatalf("expected AGENTS.md in mentor workspace: %v", err)
	}
	for _, slug := range []string{defaultMentorSkillSlug, mentorTutoringSkillSlug, mentorVisualSkillSlug} {
		if _, err := os.Stat(filepath.Join(mentorDir, ".agents", "skills", slug, "SKILL.md")); err != nil {
			t.Fatalf("expected mentor skill %s SKILL.md: %v", slug, err)
		}
	}
	for _, dir := range []string{"notes", "visuals"} {
		if info, err := os.Stat(filepath.Join(mentorDir, dir)); err != nil || !info.IsDir() {
			t.Fatalf("expected %s dir in mentor workspace: %v", dir, err)
		}
	}
	if len(bootResp.Bot.Skills) < 3 {
		t.Fatalf("expected at least 3 skills discovered on official Mentor Bot, got %d", len(bootResp.Bot.Skills))
	}

	// Re-bootstrapping with UpgradeTemplates=true succeeds and preserves binding without duplicating Bot
	upgradeBody, _ := json.Marshal(BootstrapMentorBotRequest{UpgradeTemplates: true})
	upReq := httptest.NewRequest(http.MethodPost, BasePath+"/mentor-bot/bootstrap", bytes.NewReader(upgradeBody))
	upW := httptest.NewRecorder()
	svc.ServeHTTP(upW, upReq)
	if upW.Code != http.StatusOK {
		t.Fatalf("upgrade bootstrap status=%d body=%s", upW.Code, upW.Body.String())
	}

	// 2. Trigger manual scan -> stages evidence-pack.json & spawns Mentor Bot session
	scanReq := httptest.NewRequest(http.MethodPost, BasePath+"/scans", bytes.NewReader([]byte(`{"lookback_days":7}`)))
	w = httptest.NewRecorder()
	svc.ServeHTTP(w, scanReq)
	if w.Code != http.StatusOK {
		t.Fatalf("trigger scan status=%d body=%s", w.Code, w.Body.String())
	}
	var scanResp TriggerScanResponse
	if err := json.Unmarshal(w.Body.Bytes(), &scanResp); err != nil {
		t.Fatal(err)
	}
	if scanResp.Run.Status != "running" || scanResp.Run.SessionKey == "" || scanResp.Run.InboxDir == "" {
		t.Fatalf("unexpected scan run: %+v", scanResp.Run)
	}
	if _, err := os.Stat(filepath.Join(scanResp.Run.InboxDir, "evidence-pack.json")); err != nil {
		t.Fatalf("expected evidence-pack.json in bot inbox: %v", err)
	}

	// 3. Mentor Bot submits structured blind spots via IngestFromAgent
	ingestResp, err := svc.IngestFromAgent(ctx, IngestRequest{
		RunID:   scanResp.Run.ID,
		Summary: "发现 2 个核心盲区",
		Topics: []IngestTopicInput{
			{
				Title:            "Go Windows 交叉编译符号表与 Defender 误报",
				Category:         "blackbox_code",
				Priority:         "high",
				EstimatedMinutes: 12,
				ProjectName:      "demo-project",
				Workspace:        projectDir,
				WhyNow:           "近期调整编译参数移除了 -s -w",
				KeyPoints:        []string{"剥离符号表提高 PE 熵值", "保留符号表降低启发式误报"},
				QuizQuestions: []QuizQuestion{
					{Question: "为何保留符号表可降低误报？", Answer: "保留 Go runtime 合法特征"},
				},
				Evidence: []EvidenceItem{
					{Kind: "session", Label: "排查构建误报", AgentID: "codex-local", SessionID: "sess-1"},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("ingest failed: %v", err)
	}
	if ingestResp.NewTopicsCount != 1 || len(ingestResp.Topics) != 1 {
		t.Fatalf("unexpected ingest resp: %+v", ingestResp)
	}
	topicID := ingestResp.Topics[0].ID

	// 4. Duplicate ingest merges instead of duplicating; mastered topic is excluded on next ingest
	dupResp, err := svc.IngestFromAgent(ctx, IngestRequest{
		Topics: []IngestTopicInput{
			{
				Title:       "Go Windows 交叉编译符号表与 Defender 误报",
				Category:    "blackbox_code",
				ProjectName: "demo-project",
				Workspace:   projectDir,
				WhyNow:      "再次出现",
				Evidence:    []EvidenceItem{{Kind: "file", Label: "desktop-wails.mjs", FilePath: "scripts/desktop-wails.mjs"}},
			},
		},
	})
	if err != nil || dupResp.MergedTopicsCount != 1 || dupResp.Topics[0].OccurrenceCount != 2 {
		t.Fatalf("expected merge with occurrence=2, got err=%v resp=%+v", err, dupResp)
	}

	// 5. Start code-grounded study session via HTTP
	studyReq := httptest.NewRequest(http.MethodPost, BasePath+"/topics/"+topicID+"/study", bytes.NewReader([]byte(`{"sync_session_schedule":true}`)))
	w = httptest.NewRecorder()
	svc.ServeHTTP(w, studyReq)
	if w.Code != http.StatusOK {
		t.Fatalf("start study status=%d body=%s", w.Code, w.Body.String())
	}
	var studyResp StartStudyResponse
	if err := json.Unmarshal(w.Body.Bytes(), &studyResp); err != nil {
		t.Fatal(err)
	}
	if studyResp.SessionKey == "" || studyResp.Topic.Status != "learning" || studyResp.Topic.Review.Stage != 1 {
		t.Fatalf("unexpected study resp: %+v", studyResp)
	}

	// 6. Advance 4 spaced-repetition rounds -> auto-graduates to mastered
	for round := 2; round <= 4; round++ {
		updated, _, err := svc.repo.SubmitReviewFeedback(ctx, topicID, ReviewFeedbackRequest{Outcome: "remembered"})
		if err != nil || updated.Review.Stage != round || updated.Status != "learning" {
			t.Fatalf("round %d failed: err=%v topic=%+v", round, err, updated)
		}
	}
	graduated, _, err := svc.repo.SubmitReviewFeedback(ctx, topicID, ReviewFeedbackRequest{Outcome: "remembered"})
	if err != nil || graduated.Status != "mastered" {
		t.Fatalf("expected auto-graduation to mastered, got err=%v topic=%+v", err, graduated)
	}

	// 7. Re-ingesting a mastered topic must be skipped automatically
	skipResp, err := svc.IngestFromAgent(ctx, IngestRequest{
		Topics: []IngestTopicInput{{Title: "Go Windows 交叉编译符号表与 Defender 误报", Workspace: projectDir}},
	})
	if err != nil || skipResp.SkippedCount != 1 || skipResp.NewTopicsCount != 0 {
		t.Fatalf("expected mastered topic skipped, got err=%v resp=%+v", err, skipResp)
	}

	// 8. Scheduled scan during quiet week skips cleanly without spawning Bot
	cfg, _ := svc.repo.GetConfig(ctx)
	cfg.MinActivitySessions = 5
	cfg.MinActivityCommits = 5
	_, _ = svc.repo.SaveConfig(ctx, cfg)
	beforeSpawns := len(spawnedReqs)
	quietResp, err := svc.DispatchScan(ctx, TriggerScanRequest{Trigger: "scheduled", LookbackDays: 7})
	if err != nil || quietResp.Run.Status != "skipped" || len(spawnedReqs) != beforeSpawns {
		t.Fatalf("expected quiet scheduled scan skipped without spawning, err=%v resp=%+v", err, quietResp)
	}
}

type stubFailureSignalProvider struct {
	signals []ExternalFailureSignal
}

func (p *stubFailureSignalProvider) CollectFailureSignals(_ context.Context, _, _, _ string, _ int) []ExternalFailureSignal {
	return p.signals
}

func TestCollectEvidencePackUsesGenericFailureSignals(t *testing.T) {
	tmp := t.TempDir()
	st, err := store.Open(filepath.Join(tmp, "app.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()
	ctx := context.Background()

	projectDir := filepath.Join(tmp, "signal-proj")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatal(err)
	}
	nowRef := time.Date(2026, 11, 27, 12, 0, 0, 0, time.UTC)
	_, _ = st.UpsertSession(ctx, map[string]any{
		"id": "sess-sig", "agent_id": "codex-local", "title": "修复 CI",
		"workspace": projectDir, "project_name": "signal-proj", "status": "idle",
		"updated_at": nowRef.Add(-1 * time.Hour).Format(time.RFC3339),
	})

	svc := NewService(Options{
		Store: st,
		FailureSignals: &stubFailureSignalProvider{
			signals: []ExternalFailureSignal{
				{
					Source:    "gitlab_ci",
					Title:     "GitLab CI Pipeline #88 失败 (main)",
					Detail:    "SHA abcdef12 · http://gitlab.test/p/88",
					Timestamp: nowRef.Add(-30 * time.Minute).Format(time.RFC3339),
				},
				{
					Source:    "custom_ci",
					Title:     "Old failure outside window",
					Detail:    "ignored",
					Timestamp: nowRef.AddDate(0, 0, -20).Format(time.RFC3339),
				},
			},
		},
		Now: func() time.Time { return nowRef },
	})

	cfg := DefaultConfig(nowRef, time.UTC)
	pack, err := svc.CollectEvidencePack(ctx, "run-1", "manual", 7, nil, cfg, "")
	if err != nil {
		t.Fatalf("CollectEvidencePack: %v", err)
	}
	if len(pack.Projects) != 1 || len(pack.Projects[0].Failures) != 1 {
		t.Fatalf("expected 1 failure clue in 1 project, got %+v", pack.Projects)
	}
	f := pack.Projects[0].Failures[0]
	if f.Kind != "gitlab_ci" || f.Title != "GitLab CI Pipeline #88 失败 (main)" {
		t.Fatalf("unexpected failure clue: %+v", f)
	}
}
