package sessions

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

type titlePublisher struct {
	mu     sync.Mutex
	titles []string
}

func (p *titlePublisher) Publish(_ context.Context, kind string, row map[string]any) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if kind == "session.upsert" {
		p.titles = append(p.titles, stringValue(row, "title"))
	}
	return nil
}

func TestSubmittedSessionAlwaysGetsFirstMessageTitle(t *testing.T) {
	for _, kind := range []string{"codex", "hermes", "grok"} {
		t.Run(kind, func(t *testing.T) {
			h, st, _, cleanup := setupTestServer(t, &mockDaemonServer{})
			defer cleanup()
			h.publisher = &titlePublisher{}
			ctx := context.Background()
			st.UpsertAgent(ctx, map[string]any{"id": kind, "kind": kind, "name": kind, "status": "ready"})
			st.UpsertSession(ctx, map[string]any{"id": "s-" + kind, "agent_id": kind, "title": "New Session", "status": "idle", "control_state": "owned", "updated_at": "2026-10-09T00:00:00Z"})
			postSend(t, h, "first", kind, "持续部署这个提示我觉得没必要。后续内容")
			row, err := st.GetSession(ctx, kind, "s-"+kind)
			if err != nil || row["title"] != "持续部署这个提示我觉" {
				t.Fatalf("fallback title=%v err=%v", row, err)
			}
			postSend(t, h, "second", kind, "第二条消息不应该覆盖标题")
			row, _ = st.GetSession(ctx, kind, "s-"+kind)
			if row["title"] != "持续部署这个提示我觉" {
				t.Fatalf("title changed: %v", row)
			}
		})
	}
}

func TestOldSessionGetsTitleWhenListed(t *testing.T) {
	h, st, _, cleanup := setupTestServer(t, &mockDaemonServer{})
	defer cleanup()
	h.publisher = &titlePublisher{}
	ctx := context.Background()
	st.UpsertAgent(ctx, map[string]any{"id": "codex", "kind": "codex", "name": "Codex", "status": "ready"})
	st.UpsertSession(ctx, map[string]any{"id": "old", "agent_id": "codex", "title": "New Session", "status": "idle", "updated_at": "2026-10-09T00:00:00Z"})
	st.CreateCommand(ctx, map[string]any{"id": "old-send", "agent_id": "codex", "session_id": "old", "action": "send", "text": "旧会话也应该根据第一条消息补上标题"}, "completed")
	req := httptest.NewRequest("GET", "/api/v1/sessions", nil)
	req.Header.Set("Authorization", "Bearer browser-sec")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	var result struct {
		Items []map[string]any `json:"items"`
	}
	json.Unmarshal(w.Body.Bytes(), &result)
	if w.Code != 200 || len(result.Items) != 1 || result.Items[0]["title"] != "旧会话也应该根据第一" {
		t.Fatalf("list=%s", w.Body.String())
	}
}

func TestNativeTitleUpgradesGeneratedFallback(t *testing.T) {
	h, st, _, _ := newNativeFixture(t)
	ctx := context.Background()
	row, _ := st.UpsertSession(ctx, map[string]any{"id": "native", "agent_id": "codex-1", "title": "New Session", "status": "running", "updated_at": "2026-10-09T00:00:00Z"})
	st.CompareSessionTitle(ctx, "codex-1", "native", stringValue(row, "title"), "截取的标题", "fallback")
	agent, _ := st.GetAgent(ctx, "codex-1")
	name := "原生正式标题"
	_, err := h.ensureNativeSession(ctx, agent, nativeThread{ID: "native", Name: &name, Source: "vscode"})
	row, _ = st.GetSession(ctx, "codex-1", "native")
	if err != nil || row["title"] != name {
		t.Fatalf("native title=%v err=%v", row, err)
	}
}

func TestNativeTitleTakesPriorityOverLLM(t *testing.T) {
	h, st, _, _ := newNativeFixture(t, fakeThread("native-name", "Agent 提供的标题", "首条用户消息", "vscode", time.Now().Unix()))
	ctx := context.Background()
	st.UpsertSession(ctx, map[string]any{"id": "native-name", "agent_id": "codex-1", "title": "New Session", "status": "idle", "updated_at": "2026-10-09T00:00:00Z"})
	row, _ := st.GetSession(ctx, "codex-1", "native-name")
	// Read-only native title retrieval uses the same exact thread as discovery.
	title := h.nativeSessionTitle(ctx, row)
	if title != "Agent 提供的标题" {
		t.Fatalf("native title=%q", title)
	}
}

func TestManualRenameToSameFallbackStillPreventsUpgrade(t *testing.T) {
	h, st, _, _ := newNativeFixture(t)
	ctx := context.Background()
	st.UpsertSession(ctx, map[string]any{"id": "manual", "agent_id": "codex-1", "title": "New Session", "status": "idle", "updated_at": "2026-10-09T00:00:00Z"})
	st.CompareSessionTitle(ctx, "codex-1", "manual", "New Session", "截取标题", "fallback")
	st.UpdateSession(ctx, "codex-1", "manual", map[string]any{"title": "截取标题"})
	changed, err := st.CompareSessionTitle(ctx, "codex-1", "manual", "截取标题", "异步生成标题", "llm")
	if err != nil || changed != nil {
		t.Fatalf("manual title replaced: %v %v", changed, err)
	}
	row, _ := st.GetSession(ctx, "codex-1", "manual")
	if h.canUpgradeSessionTitle(ctx, row) {
		t.Fatal("manual title is still marked generated")
	}
}

func TestTitleLLMFailuresKeepFallback(t *testing.T) {
	for _, body := range []string{"http-error", `{"choices":[]}`, `{"choices":[{"message":{"content":""}}]}`, `{"choices":[{"message":{"content":"New Session"}}]}`, "invalid-json"} {
		t.Run(body, func(t *testing.T) {
			h, st, _, cleanup := setupTestServer(t, &mockDaemonServer{})
			defer cleanup()
			h.publisher = &titlePublisher{}
			gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if body == "http-error" {
					w.WriteHeader(500)
				}
				w.Write([]byte(body))
			}))
			defer gateway.Close()
			ctx := context.Background()
			st.SetPreference(ctx, "services:llm", map[string]string{"base_url": gateway.URL, "api_key": "fixture-key", "model": "fixture-model"}, true)
			st.UpsertAgent(ctx, map[string]any{"id": "codex", "kind": "codex", "name": "Codex", "status": "ready"})
			st.UpsertSession(ctx, map[string]any{"id": "s-codex", "agent_id": "codex", "title": "New Session", "status": "idle", "control_state": "owned", "updated_at": "2026-10-09T00:00:00Z"})
			postSend(t, h, "first", "codex", "这是第一条用户消息用于验证失败兜底")
			deadline := time.Now().Add(3 * time.Second)
			for {
				if _, busy := h.titleBusy.Load("session_title:codex::s-codex"); !busy {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("title worker did not finish")
				}
				time.Sleep(10 * time.Millisecond)
			}
			row, _ := st.GetSession(ctx, "codex", "s-codex")
			if row["title"] != "这是第一条用户消息用" {
				t.Fatalf("fallback lost: %v", row)
			}
		})
	}
}

func TestFirstSentenceTitleCountsUnicode(t *testing.T) {
	for _, tt := range []struct{ text, want string }{
		{"你好。下一句", "你好"}, {"😀一二三四五六七八九十", "😀一二三四五六七八九"}, {"\n标题\n正文", "标题"}, {"", "附件会话"},
	} {
		if got := firstSentenceTitle(tt.text); got != tt.want {
			t.Fatalf("%q got %q want %q", tt.text, got, tt.want)
		}
	}
}

func TestSessionTitleUsesConfiguredLLMAndProtectsConcurrentRename(t *testing.T) {
	for _, rename := range []bool{false, true} {
		t.Run(map[bool]string{false: "generated", true: "manual"}[rename], func(t *testing.T) {
			h, st, _, cleanup := setupTestServer(t, &mockDaemonServer{})
			defer cleanup()
			h.publisher = &titlePublisher{}
			started, release := make(chan struct{}), make(chan struct{})
			gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				if r.URL.Path != "/v1/chat/completions" || body["model"] != "configured-title-model" || r.Header.Get("Authorization") != "Bearer fixture-key" {
					t.Errorf("wrong configured LLM request")
				}
				close(started)
				<-release
				w.Header().Set("Content-Type", "application/json")
				w.Write([]byte(`{"choices":[{"message":{"content":"同步部署提示优化"}}]}`))
			}))
			defer gateway.Close()
			ctx := context.Background()
			st.SetPreference(ctx, "services:llm", map[string]string{"base_url": gateway.URL + "/v1", "api_key": "fixture-key", "model": "configured-title-model", "reasoning": "none"}, true)
			st.UpsertAgent(ctx, map[string]any{"id": "codex", "kind": "codex", "name": "Codex", "status": "ready"})
			st.UpsertSession(ctx, map[string]any{"id": "s-codex", "agent_id": "codex", "title": "New Session", "status": "idle", "control_state": "owned", "updated_at": "2026-10-09T00:00:00Z"})
			postSend(t, h, "first", "codex", "持续部署这个提示我觉得没必要")
			select {
			case <-started:
			case <-time.After(3 * time.Second):
				close(release)
				t.Fatal("title LLM was not invoked")
			}
			if rename {
				st.UpdateSession(ctx, "codex", "s-codex", map[string]any{"title": "用户手动标题"})
			}
			close(release)
			want := "同步部署提示优化"
			if rename {
				want = "用户手动标题"
			}
			deadline := time.Now().Add(3 * time.Second)
			for {
				_, busy := h.titleBusy.Load("session_title:codex::s-codex")
				row, _ := st.GetSession(ctx, "codex", "s-codex")
				if row["title"] == want && !busy {
					break
				}
				if time.Now().After(deadline) {
					t.Fatalf("title=%v want=%s", row["title"], want)
				}
				time.Sleep(10 * time.Millisecond)
			}
		})
	}
}
