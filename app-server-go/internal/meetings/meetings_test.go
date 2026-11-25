package meetings

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"astrorder.dev/app-server/internal/aiprovider"
	"astrorder.dev/app-server/internal/auth"
	"astrorder.dev/app-server/internal/llmcompat"
	"astrorder.dev/app-server/internal/store"
)

type allowAllAuth struct{}

func (allowAllAuth) AuthorizeBrowser(*http.Request) *auth.Error { return nil }

func openTestStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func TestMeetingsSummarizeOfflineAndLLM(t *testing.T) {
	st := openTestStore(t)
	h := NewHandler(Options{Store: st, Authorizer: allowAllAuth{}})

	// 1. Offline heuristic fallback when LLM not configured
	reqBody := `{
		"title": "Q4 发布评审会",
		"meeting_date": "2026-11-25",
		"participants": ["张三", "李四"],
		"transcript": "确认周五 18:00 发布 v2.3 版本。\n风险：压测环境尚未完全就绪。\nTODO: 负责人:张三 2026-11-27 完成回归测试并同步报告。"
	}`
	req := httptest.NewRequest(http.MethodPost, BasePath+"/summarize", strings.NewReader(reqBody))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp SummarizeResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode summarize resp: %v", err)
	}
	if resp.Mode != "prompt_only" || len(resp.ActionItems) != 1 || resp.ActionItems[0].Owner != "张三" || resp.ActionItems[0].DueDate != "2026-11-27" {
		t.Fatalf("unexpected offline summarize result: %+v", resp)
	}
	if len(resp.Decisions) != 1 || len(resp.Risks) != 1 {
		t.Fatalf("expected 1 decision and 1 risk, got decisions=%v risks=%v", resp.Decisions, resp.Risks)
	}

	// 2. LLM structured JSON extraction via mock endpoint
	mockLLM := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"model": "mock-gpt",
			"choices": [{
				"message": {
					"content": "{\"title\":\"Q4 发布评审会\",\"summary\":\"确认按期发布。\",\"decisions\":[\"本周五发布\"],\"risks\":[\"压测资源紧张\"],\"action_items\":[{\"text\":\"完成全量压测\",\"owner\":\"李四\",\"due_date\":\"2026-11-26\",\"priority\":\"high\"}],\"minutes_markdown\":\"# Q4 发布评审会\\n\\n## 结论\\n确认按期发布。\"}"
				}
			}]
		}`))
	}))
	defer mockLLM.Close()

	if err := st.SetPreference(context.Background(), "services:llm", map[string]any{
		"base_url": mockLLM.URL,
		"api_key":  "sk-test",
		"model":    "mock-gpt",
	}, true); err != nil {
		t.Fatalf("set llm pref: %v", err)
	}

	req2 := httptest.NewRequest(http.MethodPost, BasePath+"/summarize", strings.NewReader(reqBody))
	rec2 := httptest.NewRecorder()
	h.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusOK {
		t.Fatalf("expected 200 from LLM summarize, got %d: %s", rec2.Code, rec2.Body.String())
	}
	var resp2 SummarizeResponse
	if err := json.Unmarshal(rec2.Body.Bytes(), &resp2); err != nil {
		t.Fatalf("decode llm resp: %v", err)
	}
	if resp2.Mode != "llm" || len(resp2.ActionItems) != 1 || resp2.ActionItems[0].Owner != "李四" || resp2.ActionItems[0].Priority != "high" {
		t.Fatalf("unexpected LLM summarize result: %+v", resp2)
	}
}

func TestMeetingsCRUDAndActionItemPatch(t *testing.T) {
	st := openTestStore(t)
	h := NewHandler(Options{Store: st, Authorizer: allowAllAuth{}})

	savePayload := MeetingRecord{
		Title:           "产品周会",
		MeetingDate:     "2026-11-25",
		Participants:    []string{"Alice", "Bob"},
		MinutesMarkdown: "# 产品周会\n\n确认办公套件三期范围。",
		Summary:         "确认办公套件三期范围。",
		ActionItems: []MeetingActionItem{
			{Text: "输出 PPT 预览设计稿", Owner: "Alice", DueDate: "2026-11-26", Priority: "high", Status: "open"},
			{Text: "同步对外报价单", Owner: "Bob", DueDate: "2026-11-28", Priority: "medium", Status: "open"},
		},
	}
	raw, _ := json.Marshal(savePayload)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, BasePath, bytes.NewReader(raw)))
	if rec.Code != http.StatusOK {
		t.Fatalf("save meeting failed: %d %s", rec.Code, rec.Body.String())
	}
	var saved MeetingRecord
	_ = json.Unmarshal(rec.Body.Bytes(), &saved)
	if saved.ID == "" || len(saved.ActionItems) != 2 || saved.ActionItems[0].ID == "" {
		t.Fatalf("unexpected saved record: %+v", saved)
	}

	// List open action items
	recList := httptest.NewRecorder()
	h.ServeHTTP(recList, httptest.NewRequest(http.MethodGet, BasePath+"/action-items?status=open", nil))
	if recList.Code != http.StatusOK {
		t.Fatalf("list action items failed: %d", recList.Code)
	}
	var listResp struct {
		Items     []MeetingActionItem `json:"items"`
		OpenCount int                 `json:"open_count"`
	}
	_ = json.Unmarshal(recList.Body.Bytes(), &listResp)
	if listResp.OpenCount != 2 || len(listResp.Items) != 2 {
		t.Fatalf("expected 2 open action items, got %+v", listResp)
	}

	// Patch first action item to done
	patchBody := `{"meeting_id":"` + saved.ID + `","item_id":"` + saved.ActionItems[0].ID + `","status":"done"}`
	recPatch := httptest.NewRecorder()
	h.ServeHTTP(recPatch, httptest.NewRequest(http.MethodPatch, BasePath+"/action-items", strings.NewReader(patchBody)))
	if recPatch.Code != http.StatusOK {
		t.Fatalf("patch action item failed: %d %s", recPatch.Code, recPatch.Body.String())
	}

	// Verify open count is now 1
	recList2 := httptest.NewRecorder()
	h.ServeHTTP(recList2, httptest.NewRequest(http.MethodGet, BasePath+"/action-items?status=open", nil))
	var listResp2 struct {
		Items     []MeetingActionItem `json:"items"`
		OpenCount int                 `json:"open_count"`
	}
	_ = json.Unmarshal(recList2.Body.Bytes(), &listResp2)
	if listResp2.OpenCount != 1 || len(listResp2.Items) != 1 {
		t.Fatalf("expected 1 open action item after patch, got %+v", listResp2)
	}

	// Delete meeting
	recDel := httptest.NewRecorder()
	h.ServeHTTP(recDel, httptest.NewRequest(http.MethodDelete, BasePath+"?id="+saved.ID, nil))
	if recDel.Code != http.StatusOK {
		t.Fatalf("delete meeting failed: %d", recDel.Code)
	}
}

func TestMeetingsCustomModelSettingsAndIncrementalSummarize(t *testing.T) {
	st := openTestStore(t)
	h := NewHandler(Options{Store: st, Authorizer: allowAllAuth{}})

	mockCustom := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		// Ping test vs summarize
		if maxTok, ok := body["max_tokens"].(float64); ok && maxTok <= 16 {
			_, _ = w.Write([]byte(`{"model":"qwen-fast","choices":[{"message":{"content":"OK"}}]}`))
			return
		}
		_, _ = w.Write([]byte(`{
			"model": "qwen-fast",
			"choices": [{
				"message": {
					"content": "{\"title\":\"实时评审会\",\"summary\":\"确认采用 R2T2 实时转写与独立模型总结。\",\"topics\":[{\"timestamp\":\"00:12\",\"title\":\"转写引擎\",\"detail\":\"统一使用本地 R2T2。\"}],\"decisions\":[\"采用 R2T2\"],\"risks\":[],\"action_items\":[{\"text\":\"完成模型配置联调\",\"owner\":\"王五\",\"due_date\":\"2026-11-26\",\"priority\":\"high\"}],\"minutes_markdown\":\"# 实时评审会\\n\\n## 核心摘要\\n确认采用 R2T2。\"}"
				}
			}]
		}`))
	}))
	defer mockCustom.Close()

	// Save custom meeting model settings
	putBody := `{
		"model_source": "custom",
		"base_url": "` + mockCustom.URL + `",
		"api_key": "sk-meeting-secret-9999",
		"model": "qwen-fast",
		"auto_summarize": true,
		"auto_summarize_interval_sec": 20,
		"summary_style": "technical"
	}`
	recPut := httptest.NewRecorder()
	h.ServeHTTP(recPut, httptest.NewRequest(http.MethodPut, BasePath+"/settings", strings.NewReader(putBody)))
	if recPut.Code != http.StatusOK {
		t.Fatalf("put settings failed: %d %s", recPut.Code, recPut.Body.String())
	}
	var pubSettings MeetingSettings
	_ = json.Unmarshal(recPut.Body.Bytes(), &pubSettings)
	if !pubSettings.APIKeySet || pubSettings.APIKey != "" || pubSettings.EffectiveModel != "qwen-fast" || pubSettings.AutoSummarizeIntervalSec != 20 {
		t.Fatalf("unexpected public settings: %+v", pubSettings)
	}

	// Update interval without resending api_key -> must preserve secret
	putBody2 := `{
		"model_source": "custom",
		"base_url": "` + mockCustom.URL + `",
		"model": "qwen-fast",
		"auto_summarize": true,
		"auto_summarize_interval_sec": 60,
		"summary_style": "concise"
	}`
	recPut2 := httptest.NewRecorder()
	h.ServeHTTP(recPut2, httptest.NewRequest(http.MethodPut, BasePath+"/settings", strings.NewReader(putBody2)))
	var pubSettings2 MeetingSettings
	_ = json.Unmarshal(recPut2.Body.Bytes(), &pubSettings2)
	if !pubSettings2.APIKeySet || pubSettings2.AutoSummarizeIntervalSec != 60 {
		t.Fatalf("expected api_key preserved on partial settings save: %+v", pubSettings2)
	}

	// Test connectivity
	recTest := httptest.NewRecorder()
	h.ServeHTTP(recTest, httptest.NewRequest(http.MethodPost, BasePath+"/settings/test", nil))
	var testResp SettingsTestResponse
	_ = json.Unmarshal(recTest.Body.Bytes(), &testResp)
	if !testResp.OK || testResp.Model != "qwen-fast" {
		t.Fatalf("expected connectivity test OK, got %+v", testResp)
	}

	// Incremental summarize uses the dedicated custom meeting model (even though global services:llm is empty!)
	incBody := `{
		"title": "实时评审会",
		"incremental": true,
		"previous_summary": "初始摘要",
		"new_transcript": "[00:12] 大家同意采用 R2T2 实时流式转写，王五负责完成模型配置联调。",
		"previous_action_items": [{"id":"act_keep","text":"完成模型配置联调","status":"done"}]
	}`
	recSum := httptest.NewRecorder()
	h.ServeHTTP(recSum, httptest.NewRequest(http.MethodPost, BasePath+"/summarize", strings.NewReader(incBody)))
	if recSum.Code != http.StatusOK {
		t.Fatalf("incremental summarize failed: %d %s", recSum.Code, recSum.Body.String())
	}
	var sumResp SummarizeResponse
	_ = json.Unmarshal(recSum.Body.Bytes(), &sumResp)
	if sumResp.Mode != "llm" || sumResp.Model != "qwen-fast" || len(sumResp.Topics) != 1 {
		t.Fatalf("unexpected incremental summarize resp: %+v", sumResp)
	}
	// Must preserve existing action item's ID & done status on incremental merge
	if len(sumResp.ActionItems) != 1 || sumResp.ActionItems[0].ID != "act_keep" || sumResp.ActionItems[0].Status != "done" {
		t.Fatalf("expected incremental merge to preserve existing action item status: %+v", sumResp.ActionItems)
	}
}

func TestMeetingsReuseAIProviderGemini(t *testing.T) {
	st := openTestStore(t)
	h := NewHandler(Options{Store: st, Authorizer: allowAllAuth{}})

	mockGemini := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-goog-api-key") != "AIza-meeting-key" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"modelVersion": "gemini-2.5-flash",
			"candidates": [{
				"content": {
					"parts": [{
						"text": "{\"title\":\"Gemini 会议总结\",\"summary\":\"复用 AI 供应商成功。\",\"topics\":[],\"decisions\":[\"复用供应商\"],\"risks\":[],\"action_items\":[],\"minutes_markdown\":\"# Gemini 会议总结\\n\\n复用 AI 供应商成功。\"}"
					}]
				}
			}]
		}`))
	}))
	defer mockGemini.Close()

	key := "AIza-meeting-key"
	_, prov, err := aiprovider.UpsertProvider(context.Background(), st, aiprovider.ProviderPatch{
		Name:     "Google Gemini",
		Protocol: llmcompat.ProtocolGemini,
		BaseURL:  mockGemini.URL + "/v1beta",
		APIKey:   &key,
		Models:   []string{"gemini-2.5-flash"},
	})
	if err != nil {
		t.Fatalf("upsert gemini provider: %v", err)
	}

	putBody := `{
		"model_source": "provider",
		"provider_id": "` + prov.ID + `",
		"model": "gemini-2.5-flash",
		"auto_summarize": true,
		"auto_summarize_interval_sec": 30,
		"summary_style": "concise"
	}`
	recPut := httptest.NewRecorder()
	h.ServeHTTP(recPut, httptest.NewRequest(http.MethodPut, BasePath+"/settings", strings.NewReader(putBody)))
	if recPut.Code != http.StatusOK {
		t.Fatalf("put provider settings failed: %d %s", recPut.Code, recPut.Body.String())
	}
	var pub MeetingSettings
	_ = json.Unmarshal(recPut.Body.Bytes(), &pub)
	if pub.ModelSource != "provider" || pub.ProviderName != "Google Gemini" || !pub.EffectiveConfigured || pub.EffectiveModel != "gemini-2.5-flash" {
		t.Fatalf("unexpected provider public settings: %+v", pub)
	}

	recTest := httptest.NewRecorder()
	h.ServeHTTP(recTest, httptest.NewRequest(http.MethodPost, BasePath+"/settings/test", strings.NewReader(putBody)))
	var testResp SettingsTestResponse
	_ = json.Unmarshal(recTest.Body.Bytes(), &testResp)
	if !testResp.OK || testResp.Model != "gemini-2.5-flash" {
		t.Fatalf("expected gemini provider connectivity test OK, got %+v", testResp)
	}

	recSum := httptest.NewRecorder()
	h.ServeHTTP(recSum, httptest.NewRequest(http.MethodPost, BasePath+"/summarize", strings.NewReader(`{"title":"测试会议","transcript":"讨论确认复用 AI 供应商。"}`)))
	if recSum.Code != http.StatusOK {
		t.Fatalf("gemini summarize failed: %d %s", recSum.Code, recSum.Body.String())
	}
	var sumResp SummarizeResponse
	_ = json.Unmarshal(recSum.Body.Bytes(), &sumResp)
	if sumResp.Mode != "llm" || sumResp.Title != "Gemini 会议总结" {
		t.Fatalf("unexpected gemini summarize resp: %+v", sumResp)
	}
}

func TestLegacyCustomMeetingProviderPersistsWhenSwitchedToProviderMode(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)
	h := NewHandler(Options{Store: st, Authorizer: allowAllAuth{}})

	// Seed legacy custom meeting model (no services:ai_providers persisted yet)
	if err := st.SetPreference(ctx, PrefMeetingsSettings, map[string]any{
		"model_source": "custom",
		"base_url":     "http://10.0.0.5:4000/v1",
		"api_key":      "sk-legacy-meeting-1234",
		"model":        "guolian-llm-max",
	}, true); err != nil {
		t.Fatalf("seed legacy meeting settings: %v", err)
	}

	// Switch meeting settings directly to provider mode pointing at prv_meeting_legacy
	putBody := `{
		"model_source": "provider",
		"provider_id": "prv_meeting_legacy",
		"model": "guolian-llm-max",
		"auto_summarize": true,
		"auto_summarize_interval_sec": 30,
		"summary_style": "concise"
	}`
	recPut := httptest.NewRecorder()
	h.ServeHTTP(recPut, httptest.NewRequest(http.MethodPut, BasePath+"/settings", strings.NewReader(putBody)))
	if recPut.Code != http.StatusOK {
		t.Fatalf("put provider settings failed: %d %s", recPut.Code, recPut.Body.String())
	}
	var pub MeetingSettings
	_ = json.Unmarshal(recPut.Body.Bytes(), &pub)
	if !pub.EffectiveConfigured || pub.ProviderID != "prv_meeting_legacy" || pub.EffectiveModel != "guolian-llm-max" {
		t.Fatalf("expected migrated provider to stay configured after switch: %+v", pub)
	}

	// Reload AI providers: prv_meeting_legacy must still exist durably!
	cfg, err := aiprovider.LoadConfig(ctx, st)
	if err != nil {
		t.Fatalf("reload providers: %v", err)
	}
	found := false
	for _, p := range cfg.Providers {
		if p.ID == "prv_meeting_legacy" && p.BaseURL == "http://10.0.0.5:4000/v1" && p.APIKey == "sk-legacy-meeting-1234" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("legacy meeting provider disappeared after switching meeting model: %+v", cfg.Providers)
	}
}
