package digest

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"astrorder.dev/app-server/internal/aiprovider"
)

func TestCollectOfficeDocsLocalOnlyAndMeetings(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	localDir := t.TempDir()
	now := time.Now().UTC()
	ts := now.Format(time.RFC3339)

	// Create local office documents
	if err := os.WriteFile(filepath.Join(localDir, "产品方案.docx"), []byte("fake-docx"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(localDir, "会议笔记.md"), []byte("# 笔记"), 0o600); err != nil {
		t.Fatal(err)
	}

	// Register 1 local project and 1 remote project
	_, _ = st.UpsertProject(ctx, map[string]any{
		"source_id":     "local",
		"connection_id": "local",
		"project_id":    "proj-local",
		"project_name":  "市场办公空间",
		"workspace":     localDir,
		"updated_at":    ts,
	})
	_, _ = st.UpsertProject(ctx, map[string]any{
		"source_id":     "ssh-prod",
		"connection_id": "ssh-prod",
		"project_id":    "proj-remote",
		"project_name":  "远端研发机",
		"workspace":     "/srv/remote-app",
		"updated_at":    ts,
	})
	_, _ = st.DB().ExecContext(ctx, `
		INSERT INTO sessions (id, agent_id, title, workspace, connection_id, status, project_id, project_name, history_state, control_state, updated_at)
		VALUES ('sess-remote', 'codex', '远端发布', '/srv/remote-app', 'ssh-prod', 'idle', 'proj-remote', '远端研发机', 'local', 'attached', ?)`, ts)

	// Persist a meeting linked to the local workspace
	_ = st.SetPreference(ctx, "meetings:records", []map[string]any{
		{
			"id":           "mtg-1",
			"title":        "Q4 营销复盘会",
			"meeting_date": now.Format("2006-01-02"),
			"workspace":    localDir,
			"summary":      "确认 Q4 投放预算与节奏。",
			"decisions":    []string{"预算提升 15%"},
			"topics":       []map[string]any{{"title": "预算分配", "detail": "重点倾斜华东"}},
			"action_items": []map[string]any{{"text": "输出投放排期表", "owner": "张三", "status": "open"}},
			"updated_at":   ts,
		},
	}, true)

	h := NewHandler(Options{Store: st, Authorizer: allowBrowser{}})
	trueVal := true
	falseVal := false
	body, _ := json.Marshal(CollectRequest{
		From:              now.Add(-24 * time.Hour).Format(time.RFC3339),
		To:                now.Add(24 * time.Hour).Format(time.RFC3339),
		IncludeGit:        &falseVal,
		IncludeCheckpoints: &falseVal,
		IncludeSQLAudit:   &falseVal,
		IncludeOfficeDocs: &trueVal,
		IncludeMeetings:   &trueVal,
		LocalOnly:         &trueVal,
	})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, BasePath+"/collect", bytes.NewReader(body)))
	if rec.Code != http.StatusOK {
		t.Fatalf("collect failed: %d %s", rec.Code, rec.Body.String())
	}
	var bundle DigestBundle
	if err := json.Unmarshal(rec.Body.Bytes(), &bundle); err != nil {
		t.Fatal(err)
	}
	if bundle.Stats.OfficeDocCount != 2 {
		t.Fatalf("expected 2 office docs, got %+v", bundle.Stats)
	}
	if bundle.Stats.MeetingCount != 1 {
		t.Fatalf("expected 1 meeting, got %+v", bundle.Stats)
	}
	for _, opt := range bundle.AvailableProjects {
		if opt.ProjectName == "远端研发机" {
			t.Fatalf("local_only=true must exclude remote project: %+v", bundle.AvailableProjects)
		}
	}
	foundActionDetail := false
	for _, grp := range bundle.Projects {
		for _, it := range grp.Items {
			if it.Kind == "meeting" && strings.Contains(it.Summary, "输出投放排期表") {
				foundActionDetail = true
			}
		}
	}
	if !foundActionDetail {
		t.Fatalf("expected meeting summary to include concrete action items: %+v", bundle.Projects)
	}
}

func TestGenerateWithExplicitProviderAndModel(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()

	var capturedModel string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req map[string]any
		_ = json.NewDecoder(r.Body).Decode(&req)
		capturedModel, _ = req["model"].(string)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"model":   capturedModel,
			"choices": []map[string]any{{"message": map[string]any{"content": "# 办公周报\n\n完成全部任务。"}}},
		})
	}))
	defer srv.Close()

	key := "sk-digest-provider-key"
	_, p, err := aiprovider.UpsertProvider(ctx, st, aiprovider.ProviderPatch{
		Name:     "Digest Provider",
		Protocol: "openai",
		BaseURL:  srv.URL + "/v1",
		APIKey:   &key,
		Models:   []string{"office-writer-1"},
	})
	if err != nil {
		t.Fatal(err)
	}

	h := NewHandler(Options{Store: st, Authorizer: allowBrowser{}, HTTPClient: srv.Client()})
	body, _ := json.Marshal(GenerateRequest{
		From:       "2026-11-20",
		To:         "2026-11-25",
		TemplateID: "office_weekly",
		ProviderID: p.ID,
		Model:      "office-writer-1",
		Items: []DigestItem{
			{ItemID: "1", Kind: "office_doc", ProjectName: "市场空间", Title: "方案.docx", Selected: true},
		},
	})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, BasePath+"/generate", bytes.NewReader(body)))
	if rec.Code != http.StatusOK {
		t.Fatalf("generate failed: %d %s", rec.Code, rec.Body.String())
	}
	if capturedModel != "office-writer-1" {
		t.Fatalf("expected office-writer-1, got %q", capturedModel)
	}
}
