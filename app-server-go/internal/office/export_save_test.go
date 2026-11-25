package office

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

	"astrorder.dev/app-server/internal/aiprovider"
	"astrorder.dev/app-server/internal/store"
	"astrorder.dev/app-server/internal/targetfs"
)

func TestExportSaveToWorkspaceFormats(t *testing.T) {
	root := t.TempDir()
	resolver := targetfs.NewResolver(targetfs.ResolverOptions{AllowedRoots: []string{root}})
	h := NewHandler(Options{
		Authorizer: allowAllAuth{},
		Resolver:   resolver,
	})

	for _, tc := range []struct {
		format  string
		wantExt string
	}{
		{"md", ".md"},
		{"docx", ".docx"},
		{"pptx", ".pptx"},
	} {
		payload, _ := json.Marshal(ExportSaveRequest{
			Workspace: root,
			FileName:  "季度总结",
			Format:    tc.format,
			Title:     "季度总结",
			Markdown:  "# 季度总结\n\n## 核心进展\n- 完成交付\n",
		})
		req := httptest.NewRequest(http.MethodPost, BasePath+"/export/save", bytes.NewReader(payload))
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("save %s failed (%d): %s", tc.format, rec.Code, rec.Body.String())
		}
		var resp struct {
			OK   bool   `json:"ok"`
			Path string `json:"path"`
			Name string `json:"name"`
			Size int64  `json:"size"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatalf("decode response: %v", err)
		}
		if !resp.OK || !strings.HasSuffix(resp.Name, tc.wantExt) || resp.Size <= 0 {
			t.Fatalf("unexpected save response for %s: %+v", tc.format, resp)
		}
		if _, err := os.Stat(resp.Path); err != nil {
			t.Fatalf("saved file missing %s: %v", resp.Path, err)
		}
	}
}

func TestRefineSupportsExplicitProviderAndModel(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	var capturedModel string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		capturedModel, _ = body["model"].(string)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"model": capturedModel,
			"choices": []map[string]any{
				{"message": map[string]any{"content": "润色后的正文内容"}},
			},
		})
	}))
	defer srv.Close()

	key := "sk-office-refine-key"
	_, p, err := aiprovider.UpsertProvider(context.Background(), st, aiprovider.ProviderPatch{
		Name:     "Refine Provider",
		Protocol: "openai",
		BaseURL:  srv.URL + "/v1",
		APIKey:   &key,
		Models:   []string{"refine-model-x"},
	})
	if err != nil {
		t.Fatal(err)
	}

	h := NewHandler(Options{
		Authorizer: allowAllAuth{},
		Store:      st,
		HTTPClient: srv.Client(),
	})

	payload, _ := json.Marshal(RefineRequest{
		SelectedText: "这是一段原始草稿",
		Instruction:  "润色语言，使其更正式简洁",
		ProviderID:   p.ID,
		Model:        "refine-model-x",
	})
	req := httptest.NewRequest(http.MethodPost, BasePath+"/refine", bytes.NewReader(payload))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("refine failed (%d): %s", rec.Code, rec.Body.String())
	}
	if capturedModel != "refine-model-x" {
		t.Fatalf("expected refine-model-x, got %q", capturedModel)
	}
}
