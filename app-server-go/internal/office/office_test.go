package office

import (
	"archive/zip"
	"bytes"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/text/encoding/simplifiedchinese"

	"context"

	"astrorder.dev/app-server/internal/auth"
	"astrorder.dev/app-server/internal/store"
	"astrorder.dev/app-server/internal/targetfs"
)

type allowAllAuth struct{}

func (allowAllAuth) AuthorizeBrowser(*http.Request) *auth.Error { return nil }

func makeSamplePNGBase64(t *testing.T) string {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 120, 60))
	for y := 0; y < 60; y++ {
		for x := 0; x < 120; x++ {
			img.Set(x, y, color.RGBA{R: 37, G: 99, B: 235, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(buf.Bytes())
}

func TestDocxExportAndPreviewRoundTrip(t *testing.T) {
	md := `# 项目周报

本周完成了 **星序办公套件** 的设计与验证，包含 *斜体*、~~旧方案~~ 和 ` + "`inline_code`" + `，参考 [官方文档](https://example.com/docs)。

## 核心成果

- 完成 Word 原生导出
  - 支持多级列表缩进
- 支持表格与 Mermaid 流程图嵌入

1. 指标复核
2. 线上发布

> [!IMPORTANT]
> 本次升级零新增第三方依赖。

| 模块 | 状态 | 耗时(ms) |
| --- | :---: | ---: |
| Word 导出 | 完成 | 42 |
| Excel 导出 | 完成 | 18 |

` + "```mermaid\ngraph LR\n  A[Markdown] --> B[DOCX]\n```\n\n```sql\nSELECT 1;\n```"

	docxBytes, err := ExportMarkdownToDocx(DocxExportRequest{
		Title:    "项目周报",
		Subtitle: "2026-11-25 · Astrorder",
		Markdown: md,
		Diagrams: []DiagramImage{
			{Index: 0, PNGBase64: makeSamplePNGBase64(t), Width: 120, Height: 60},
		},
	})
	if err != nil {
		t.Fatalf("ExportMarkdownToDocx failed: %v", err)
	}

	// Verify zip structure contains required OOXML parts + embedded media
	zr, err := zip.NewReader(bytes.NewReader(docxBytes), int64(len(docxBytes)))
	if err != nil {
		t.Fatalf("invalid zip: %v", err)
	}
	parts := map[string]bool{}
	for _, f := range zr.File {
		parts[f.Name] = true
	}
	for _, reqPart := range []string{
		"[Content_Types].xml",
		"_rels/.rels",
		"word/document.xml",
		"word/styles.xml",
		"word/numbering.xml",
		"word/_rels/document.xml.rels",
		"word/media/diagram_1.png",
	} {
		if !parts[reqPart] {
			t.Fatalf("missing expected docx part %s", reqPart)
		}
	}

	// Verify core.xml contains no application branding
	for _, f := range zr.File {
		if f.Name == "docProps/core.xml" {
			rc, _ := f.Open()
			buf := new(bytes.Buffer)
			_, _ = buf.ReadFrom(rc)
			_ = rc.Close()
			if strings.Contains(buf.String(), "Astrorder") || strings.Contains(buf.String(), "星序") {
				t.Fatalf("core.xml must not contain app branding: %s", buf.String())
			}
		}
	}

	// Round-trip through ParseDocxBytes
	preview, err := ParseDocxBytes("weekly.docx", "/tmp/weekly.docx", docxBytes)
	if err != nil {
		t.Fatalf("ParseDocxBytes failed: %v", err)
	}
	if preview.Title != "项目周报" {
		t.Fatalf("expected title '项目周报', got %q", preview.Title)
	}
	if len(preview.Outline) < 2 {
		t.Fatalf("expected >= 2 outline entries, got %d", len(preview.Outline))
	}
	if preview.Stats.Tables < 2 {
		// 1 markdown table + 1 styled code block table
		t.Fatalf("expected >= 2 tables, got %d", preview.Stats.Tables)
	}
	if preview.Stats.Images != 1 {
		t.Fatalf("expected 1 embedded diagram image, got %d", preview.Stats.Images)
	}
	foundImageBlock := false
	foundHyperlink := false
	for _, blk := range preview.Blocks {
		if blk.Kind == "image" && strings.HasPrefix(blk.ImageDataURI, "data:image/png;base64,") {
			foundImageBlock = true
		}
		for _, r := range blk.Runs {
			if r.Href == "https://example.com/docs" {
				foundHyperlink = true
			}
		}
	}
	if !foundImageBlock {
		t.Fatalf("expected embedded PNG image block in preview")
	}
	if !foundHyperlink {
		t.Fatalf("expected external hyperlink preserved in preview")
	}
}

func TestXlsxExportAndPreviewRoundTrip(t *testing.T) {
	xlsxBytes, err := ExportSheetsToXlsx(XlsxExportRequest{
		FileName: "report.xlsx",
		Sheets: []SheetExportInput{
			{
				Name:    "销售汇总/2026",
				Columns: []string{"编号", "区域", "金额"},
				Rows: [][]any{
					{"007", "华东", 1250.5},
					{"008", "华南", 980},
				},
			},
			{
				Name:    "明细",
				Columns: []string{"项目", "状态"},
				Rows: [][]any{
					{"星序", "已完成"},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("ExportSheetsToXlsx failed: %v", err)
	}

	preview, err := ParseSheetBytes("report.xlsx", "/tmp/report.xlsx", xlsxBytes, 100, 50)
	if err != nil {
		t.Fatalf("ParseSheetBytes failed: %v", err)
	}
	if preview.Format != "xlsx" || len(preview.Sheets) != 2 {
		t.Fatalf("expected 2 xlsx sheets, got format=%s count=%d", preview.Format, len(preview.Sheets))
	}
	s0 := preview.Sheets[0]
	if s0.TotalRows != 2 || len(s0.Columns) != 3 {
		t.Fatalf("unexpected sheet0 dimensions: rows=%d cols=%d", s0.TotalRows, len(s0.Columns))
	}
	// Leading-zero string "007" must remain string, while 1250.5 must remain numeric
	if got, ok := s0.Rows[0][0].(string); !ok || got != "007" {
		t.Fatalf("expected leading-zero string '007', got %#v", s0.Rows[0][0])
	}
	if got, ok := s0.Rows[0][2].(float64); !ok || got != 1250.5 {
		t.Fatalf("expected numeric 1250.5, got %#v", s0.Rows[0][2])
	}
}

func TestCSVGBKAndUTF8Preview(t *testing.T) {
	utf8CSV := "\xef\xbb\xbf姓名,部门,分数\n张三,研发部,95.5\n李四,产品部,88\n"
	gbkBytes, err := simplifiedchinese.GB18030.NewEncoder().Bytes([]byte("姓名,部门,分数\n王五,运维部,91\n"))
	if err != nil {
		t.Fatalf("encode gbk: %v", err)
	}

	p1, err := ParseSheetBytes("utf8.csv", "/tmp/utf8.csv", []byte(utf8CSV), 100, 50)
	if err != nil {
		t.Fatalf("parse utf8 csv: %v", err)
	}
	if p1.Encoding != "UTF-8 (BOM)" || p1.Sheets[0].Columns[0] != "姓名" || p1.Sheets[0].Rows[0][2] != 95.5 {
		t.Fatalf("unexpected utf8 csv result: %+v", p1)
	}

	p2, err := ParseSheetBytes("gbk.csv", "/tmp/gbk.csv", gbkBytes, 100, 50)
	if err != nil {
		t.Fatalf("parse gbk csv: %v", err)
	}
	if p2.Encoding != "GB18030" || p2.Sheets[0].Rows[0][0] != "王五" {
		t.Fatalf("unexpected gbk csv result: %+v", p2)
	}
}

func TestHTTPHandlerPreviewAndScopeGuard(t *testing.T) {
	allowedDir := t.TempDir()
	outsideDir := t.TempDir()

	csvPath := filepath.Join(allowedDir, "metrics.csv")
	if err := os.WriteFile(csvPath, []byte("name,count\nalpha,10\nbeta,20\n"), 0o600); err != nil {
		t.Fatalf("write csv: %v", err)
	}
	outsidePath := filepath.Join(outsideDir, "secret.csv")
	if err := os.WriteFile(outsidePath, []byte("secret,val\nx,1\n"), 0o600); err != nil {
		t.Fatalf("write outside csv: %v", err)
	}

	resolver := targetfs.NewResolver(targetfs.ResolverOptions{
		AllowedRoots: []string{allowedDir},
	})
	h := NewHandler(Options{
		Authorizer: allowAllAuth{},
		Resolver:   resolver,
	})

	// Allowed file succeeds
	req := httptest.NewRequest(http.MethodGet, BasePath+"/preview/sheet?path="+csvPath, nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp SheetPreviewResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(resp.Sheets) != 1 || resp.Sheets[0].TotalRows != 2 {
		t.Fatalf("unexpected preview payload: %+v", resp)
	}

	// Outside allowlist must be rejected (403)
	req2 := httptest.NewRequest(http.MethodGet, BasePath+"/preview/sheet?path="+outsidePath, nil)
	rec2 := httptest.NewRecorder()
	h.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusForbidden && rec2.Code != http.StatusNotFound {
		t.Fatalf("expected 403/404 for outside path, got %d: %s", rec2.Code, rec2.Body.String())
	}

	// Markdown-table-to-xlsx endpoint succeeds
	body := `{"file_name":"tables.xlsx","markdown":"## 结果\n\n| A | B |\n|---|---|\n| 1 | 2 |\n"}`
	req3 := httptest.NewRequest(http.MethodPost, BasePath+"/export/xlsx", strings.NewReader(body))
	rec3 := httptest.NewRecorder()
	h.ServeHTTP(rec3, req3)
	if rec3.Code != http.StatusOK {
		t.Fatalf("expected 200 for xlsx export, got %d: %s", rec3.Code, rec3.Body.String())
	}
	if !strings.Contains(rec3.Header().Get("Content-Type"), "spreadsheetml") {
		t.Fatalf("unexpected content type: %s", rec3.Header().Get("Content-Type"))
	}
}

func TestPptxExportAndPreviewRoundTrip(t *testing.T) {
	md := `# 季度业务汇报

## 本期成果

- 完成多格式文档在线预览与导出
- 会议纪要与行动项自动沉淀

## 关键指标

| 模块 | 完成率 | 负责人 |
| --- | --- | --- |
| 办公预览 | 100% | 研发组 |
| 会议闭环 | 100% | 产品组 |

## 架构示意

` + "```mermaid\ngraph LR\n  A[会议] --> B[纪要] --> C[行动项]\n```"

	pptxBytes, err := ExportMarkdownToPptx(PptxExportRequest{
		Title:    "季度业务汇报",
		Subtitle: "2026 Q4",
		Markdown: md,
		Diagrams: []DiagramImage{
			{Index: 0, PNGBase64: makeSamplePNGBase64(t), Width: 120, Height: 60},
		},
	})
	if err != nil {
		t.Fatalf("ExportMarkdownToPptx failed: %v", err)
	}

	zr, err := zip.NewReader(bytes.NewReader(pptxBytes), int64(len(pptxBytes)))
	if err != nil {
		t.Fatalf("invalid pptx zip: %v", err)
	}
	for _, f := range zr.File {
		if f.Name == "docProps/core.xml" {
			rc, _ := f.Open()
			buf := new(bytes.Buffer)
			_, _ = buf.ReadFrom(rc)
			_ = rc.Close()
			if strings.Contains(buf.String(), "Astrorder") || strings.Contains(buf.String(), "星序") {
				t.Fatalf("pptx core.xml must not contain app branding: %s", buf.String())
			}
		}
	}

	preview, err := ParsePptxBytes("deck.pptx", "/tmp/deck.pptx", pptxBytes)
	if err != nil {
		t.Fatalf("ParsePptxBytes failed: %v", err)
	}
	if preview.Title != "季度业务汇报" {
		t.Fatalf("expected title '季度业务汇报', got %q", preview.Title)
	}
	if preview.SlideCount < 4 {
		t.Fatalf("expected >= 4 slides (cover + bullets + table + diagram), got %d", preview.SlideCount)
	}
	if preview.Stats.Tables != 1 || preview.Stats.Images != 1 {
		t.Fatalf("expected 1 table and 1 image, got stats=%+v", preview.Stats)
	}
}

func TestOfficeRefineSelection(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	h := NewHandler(Options{
		Store:      st,
		Authorizer: allowAllAuth{},
	})

	// 1. Prompt-only fallback when LLM key not configured
	req := httptest.NewRequest(http.MethodPost, BasePath+"/refine", strings.NewReader(`{
		"document_title": "方案评审",
		"selected_text": "我们打算下周开始重构整个模块，可能会花两周时间。",
		"instruction": "改写为更正式的汇报风格"
	}`))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp RefineResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode refine resp: %v", err)
	}
	if resp.Mode != "prompt_only" || resp.AssembledPrompt == "" {
		t.Fatalf("expected prompt_only fallback, got %+v", resp)
	}

	// 2. Mock LLM rewrite
	mockLLM := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"model":"mock-editor","choices":[{"message":{"content":"计划于下周启动核心模块重构，预计周期两周。"}}]}`))
	}))
	defer mockLLM.Close()

	_ = st.SetPreference(context.Background(), "services:llm", map[string]any{
		"base_url": mockLLM.URL,
		"api_key":  "sk-test",
		"model":    "mock-editor",
	}, true)

	req2 := httptest.NewRequest(http.MethodPost, BasePath+"/refine", strings.NewReader(`{
		"document_title": "方案评审",
		"selected_text": "我们打算下周开始重构整个模块，可能会花两周时间。",
		"instruction": "改写为更正式的汇报风格"
	}`))
	rec2 := httptest.NewRecorder()
	h.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusOK {
		t.Fatalf("expected 200 from LLM refine, got %d: %s", rec2.Code, rec2.Body.String())
	}
	var resp2 RefineResponse
	_ = json.Unmarshal(rec2.Body.Bytes(), &resp2)
	if resp2.Mode != "llm" || !strings.Contains(resp2.RewrittenText, "核心模块重构") {
		t.Fatalf("unexpected llm refine resp: %+v", resp2)
	}
}
