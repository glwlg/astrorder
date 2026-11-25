package office

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"astrorder.dev/app-server/internal/officepersona"
	"astrorder.dev/app-server/internal/store"
	"astrorder.dev/app-server/internal/targetfs"
)

func TestOfficeProfileGetAndPut(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "profile.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	h := NewHandler(Options{
		Store:      st,
		Authorizer: allowAllAuth{},
	})

	// Initial GET returns defaults
	req := httptest.NewRequest(http.MethodGet, BasePath+"/profile", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET profile status=%d body=%s", rec.Code, rec.Body.String())
	}

	// PUT custom persona
	payload := officepersona.OfficePersona{
		DisplayName:     "陈思",
		Role:            "运营总监",
		Department:      "战略运营部",
		DefaultAudience: "集团执委会",
		WritingStyle:    "formal_doc",
		ToneRules:       "数据必须附带同比环比",
		Glossary:        "星序、GMV",
		AvoidWords:      "内部测试机",
		AutoInjectChat:  true,
	}
	raw, _ := json.Marshal(payload)
	putReq := httptest.NewRequest(http.MethodPut, BasePath+"/profile", bytes.NewReader(raw))
	putRec := httptest.NewRecorder()
	h.ServeHTTP(putRec, putReq)
	if putRec.Code != http.StatusOK {
		t.Fatalf("PUT profile status=%d body=%s", putRec.Code, putRec.Body.String())
	}

	// Reload via officepersona.Load and verify prompt section
	loaded := officepersona.Load(t.Context(), st)
	if loaded.DisplayName != "陈思" || loaded.WritingStyle != "formal_doc" {
		t.Fatalf("unexpected loaded persona: %+v", loaded)
	}
	prompt := officepersona.PromptSection(loaded)
	if !strings.Contains(prompt, "运营总监") || !strings.Contains(prompt, "同比环比") {
		t.Fatalf("expected persona prompt details, got: %s", prompt)
	}
}

func TestRecentDocsAndSourcesExtractSynthesize(t *testing.T) {
	ws := t.TempDir()
	mdPath := filepath.Join(ws, "Q4方案.md")
	csvPath := filepath.Join(ws, "预算表.csv")
	if err := os.WriteFile(mdPath, []byte("# Q4 推进方案\n\n目标是提升客户续费率 15%。"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(csvPath, []byte("部门,预算\n运营部,120000\n市场部,85000\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	resolver := targetfs.NewResolver(targetfs.ResolverOptions{
		AllowedRoots: []string{ws},
	})
	h := NewHandler(Options{
		Authorizer: allowAllAuth{},
		Resolver:   resolver,
	})

	// 1. Recent docs scan
	recDocsReq := httptest.NewRequest(http.MethodGet, BasePath+"/recent-docs?workspace="+ws, nil)
	recDocsRes := httptest.NewRecorder()
	h.ServeHTTP(recDocsRes, recDocsReq)
	if recDocsRes.Code != http.StatusOK {
		t.Fatalf("recent-docs status=%d body=%s", recDocsRes.Code, recDocsRes.Body.String())
	}
	var docsPayload struct {
		Items []RecentOfficeDoc `json:"items"`
	}
	if err := json.Unmarshal(recDocsRes.Body.Bytes(), &docsPayload); err != nil {
		t.Fatal(err)
	}
	if len(docsPayload.Items) != 2 {
		t.Fatalf("expected 2 recent docs, got %d: %+v", len(docsPayload.Items), docsPayload.Items)
	}

	// 2. Multi-doc extract
	extractBody, _ := json.Marshal(SourcesExtractRequest{
		Paths: []string{mdPath, csvPath},
	})
	extReq := httptest.NewRequest(http.MethodPost, BasePath+"/sources/extract", bytes.NewReader(extractBody))
	extRes := httptest.NewRecorder()
	h.ServeHTTP(extRes, extReq)
	if extRes.Code != http.StatusOK {
		t.Fatalf("sources/extract status=%d body=%s", extRes.Code, extRes.Body.String())
	}
	var extPayload SourcesExtractResponse
	if err := json.Unmarshal(extRes.Body.Bytes(), &extPayload); err != nil {
		t.Fatal(err)
	}
	if len(extPayload.Sources) != 2 || !strings.Contains(extPayload.AssembledContext, "[资料 1]") || !strings.Contains(extPayload.AssembledContext, "120000") {
		t.Fatalf("unexpected extract output: %+v", extPayload)
	}

	// 3. Synthesize in prompt_only mode
	synthBody, _ := json.Marshal(SourcesSynthesizeRequest{
		Paths:       []string{mdPath, csvPath},
		Action:      "compare",
		Instruction: "对比方案目标与预算匹配度",
		Mode:        "prompt_only",
	})
	synthReq := httptest.NewRequest(http.MethodPost, BasePath+"/sources/synthesize", bytes.NewReader(synthBody))
	synthRes := httptest.NewRecorder()
	h.ServeHTTP(synthRes, synthReq)
	if synthRes.Code != http.StatusOK {
		t.Fatalf("sources/synthesize status=%d body=%s", synthRes.Code, synthRes.Body.String())
	}
	var synthPayload SourcesSynthesizeResponse
	if err := json.Unmarshal(synthRes.Body.Bytes(), &synthPayload); err != nil {
		t.Fatal(err)
	}
	if synthPayload.Mode != "prompt_only" || !strings.Contains(synthPayload.AssembledPrompt, "对比方案目标与预算匹配度") {
		t.Fatalf("unexpected synthesize payload: %+v", synthPayload)
	}
}
