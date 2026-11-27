package office

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"astrorder.dev/app-server/internal/aiprovider"
	"astrorder.dev/app-server/internal/llmcompat"
	"astrorder.dev/app-server/internal/officepersona"
	"astrorder.dev/app-server/internal/targetfs"
)

const (
	maxSourceFiles      = 8
	defaultCharsPerDoc  = 6000
	maxCharsPerDocLimit = 18000
	maxTextSourceBytes  = 4 << 20
)

var recentOfficeExts = map[string]bool{
	".docx":     true,
	".doc":      true,
	".xlsx":     true,
	".xls":      true,
	".pptx":     true,
	".ppt":      true,
	".pdf":      true,
	".md":       true,
	".markdown": true,
	".csv":      true,
	".tsv":      true,
	".txt":      true,
	".wps":      true,
	".mp4":      true,
	".m4v":      true,
	".webm":     true,
	".mov":      true,
	".mkv":      true,
	".wav":      true,
	".mp3":      true,
	".m4a":      true,
	".ogg":      true,
	".flac":     true,
	".aac":      true,
	".srt":      true,
	".vtt":      true,
}

var skipScanDirs = map[string]bool{
	".git":         true,
	"node_modules": true,
	"dist":         true,
	"build":        true,
	"vendor":       true,
	"target":       true,
	"__pycache__":  true,
	".venv":        true,
	"venv":         true,
	".next":        true,
	".umi":         true,
	".cache":       true,
	".idea":        true,
	".vscode":      true,
}

type RecentOfficeDoc struct {
	Path        string `json:"path"`
	Name        string `json:"name"`
	Ext         string `json:"ext"`
	Size        int64  `json:"size"`
	ModTime     string `json:"mod_time"`
	Workspace   string `json:"workspace"`
	ProjectKey  string `json:"project_key,omitempty"`
	ProjectName string `json:"project_name,omitempty"`
}

type SourcesExtractRequest struct {
	Paths           []string `json:"paths"`
	SessionID       string   `json:"session_id,omitempty"`
	ConnectionID    string   `json:"connection_id,omitempty"`
	MaxCharsPerDoc  int      `json:"max_chars_per_doc,omitempty"`
}

type ExtractedSource struct {
	Index     int    `json:"index"`
	Path      string `json:"path"`
	Name      string `json:"name"`
	Kind      string `json:"kind"` // docx | sheet | pptx | markdown | text | pdf | unsupported
	Excerpt   string `json:"excerpt"`
	CharCount int    `json:"char_count"`
	Truncated bool   `json:"truncated"`
	Error     string `json:"error,omitempty"`
}

type SourcesExtractResponse struct {
	OK               bool              `json:"ok"`
	Sources          []ExtractedSource `json:"sources"`
	AssembledContext string            `json:"assembled_context"`
}

type SourcesSynthesizeRequest struct {
	Paths        []string `json:"paths,omitempty"`
	Action       string   `json:"action"` // summary | compare | table | ppt_outline | review | draft | custom
	Title        string   `json:"title,omitempty"`
	Instruction  string   `json:"instruction,omitempty"`
	SessionID    string   `json:"session_id,omitempty"`
	ConnectionID string   `json:"connection_id,omitempty"`
	ProviderID   string   `json:"provider_id,omitempty"`
	Model        string   `json:"model,omitempty"`
	Mode         string   `json:"mode,omitempty"` // llm | prompt_only
}

type SourcesSynthesizeResponse struct {
	OK              bool              `json:"ok"`
	Mode            string            `json:"mode"` // llm | prompt_only
	LLMConfigured   bool              `json:"llm_configured"`
	Model           string            `json:"model,omitempty"`
	Title           string            `json:"title"`
	Markdown        string            `json:"markdown"`
	AssembledPrompt string            `json:"assembled_prompt"`
	Sources         []ExtractedSource `json:"sources"`
	Message         string            `json:"message,omitempty"`
}

func (h *Handler) handleRecentDocs(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	if limit <= 0 {
		limit = 12
	}
	if limit > 30 {
		limit = 30
	}
	requestedWS := strings.TrimSpace(q.Get("workspace"))

	type scanRoot struct {
		workspace   string
		projectKey  string
		projectName string
	}
	roots := make([]scanRoot, 0, 8)
	seenWS := make(map[string]bool)

	addRoot := func(ws, pKey, pName string) {
		cleaned := strings.TrimSpace(ws)
		if cleaned == "" {
			return
		}
		normKey := strings.ToLower(strings.ReplaceAll(strings.TrimRight(cleaned, `/\`), `\`, `/`))
		if seenWS[normKey] {
			return
		}
		info, err := os.Stat(cleaned)
		if err != nil || !info.IsDir() {
			return
		}
		seenWS[normKey] = true
		if pName == "" {
			pName = filepath.Base(cleaned)
		}
		roots = append(roots, scanRoot{
			workspace:   cleaned,
			projectKey:  pKey,
			projectName: pName,
		})
	}

	if requestedWS != "" {
		addRoot(requestedWS, "", "")
	} else if h.options.Store != nil {
		if projects, err := h.options.Store.ListProjects(r.Context()); err == nil {
			for _, p := range projects {
				connID := stringField(p["connection_id"])
				if connID != "" && connID != "local" {
					continue
				}
				ws := stringField(p["workspace"])
				pID := stringField(p["project_id"])
				pName := stringField(p["project_name"])
				addRoot(ws, pID, pName)
			}
		}
		if sessions, err := h.options.Store.ListSessions(r.Context(), ""); err == nil {
			for _, s := range sessions {
				connID := stringField(s["connection_id"])
				if connID != "" && connID != "local" {
					continue
				}
				ws := stringField(s["workspace"])
				pID := stringField(s["project_id"])
				pName := stringField(s["project_name"])
				addRoot(ws, pID, pName)
			}
		}
	}

	type docEntry struct {
		doc     RecentOfficeDoc
		modTime time.Time
	}
	collected := make([]docEntry, 0, 32)
	seenPaths := make(map[string]bool)

	for _, root := range roots {
		baseDepth := strings.Count(filepath.Clean(root.workspace), string(filepath.Separator))
		scannedCount := 0
		_ = filepath.WalkDir(root.workspace, func(p string, d fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return nil
			}
			name := d.Name()
			if d.IsDir() {
				if p != root.workspace {
					if strings.HasPrefix(name, ".") || skipScanDirs[strings.ToLower(name)] {
						return filepath.SkipDir
					}
					depth := strings.Count(filepath.Clean(p), string(filepath.Separator)) - baseDepth
					if depth > 4 {
						return filepath.SkipDir
					}
				}
				return nil
			}
			scannedCount++
			if scannedCount > 600 {
				return filepath.SkipAll
			}
			if strings.HasPrefix(name, ".") || strings.HasPrefix(name, "~$") {
				return nil
			}
			ext := strings.ToLower(filepath.Ext(name))
			if !recentOfficeExts[ext] {
				return nil
			}
			info, err := d.Info()
			if err != nil || info.IsDir() || info.Size() == 0 {
				return nil
			}
			normPath := strings.ToLower(strings.ReplaceAll(p, `\`, `/`))
			if seenPaths[normPath] {
				return nil
			}
			seenPaths[normPath] = true
			collected = append(collected, docEntry{
				modTime: info.ModTime(),
				doc: RecentOfficeDoc{
					Path:        p,
					Name:        name,
					Ext:         strings.TrimPrefix(ext, "."),
					Size:        info.Size(),
					ModTime:     info.ModTime().UTC().Format(time.RFC3339),
					Workspace:   root.workspace,
					ProjectKey:  root.projectKey,
					ProjectName: root.projectName,
				},
			})
			return nil
		})
	}

	sort.Slice(collected, func(i, j int) bool {
		return collected[i].modTime.After(collected[j].modTime)
	})
	if len(collected) > limit {
		collected = collected[:limit]
	}
	items := make([]RecentOfficeDoc, 0, len(collected))
	for _, entry := range collected {
		items = append(items, entry.doc)
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (h *Handler) handleExtractSources(w http.ResponseWriter, r *http.Request) {
	var req SourcesExtractRequest
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	if err := dec.Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "无效的资料提取请求 JSON")
		return
	}
	if len(req.Paths) == 0 {
		writeJSONError(w, http.StatusBadRequest, "请至少选择一份资料文档")
		return
	}
	resp := h.extractSources(r.Context(), req)
	writeJSON(w, http.StatusOK, resp)
}

func (h *Handler) handleSynthesizeSources(w http.ResponseWriter, r *http.Request) {
	var req SourcesSynthesizeRequest
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 2<<20))
	if err := dec.Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "无效的多文档综合请求 JSON")
		return
	}
	resp, err := h.synthesizeSources(r.Context(), req)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *Handler) extractSources(ctx context.Context, req SourcesExtractRequest) SourcesExtractResponse {
	maxChars := req.MaxCharsPerDoc
	if maxChars <= 0 {
		maxChars = defaultCharsPerDoc
	}
	if maxChars > maxCharsPerDocLimit {
		maxChars = maxCharsPerDocLimit
	}

	paths := req.Paths
	if len(paths) > maxSourceFiles {
		paths = paths[:maxSourceFiles]
	}

	sources := make([]ExtractedSource, 0, len(paths))
	var ctxBuf strings.Builder
	for idx, rawPath := range paths {
		cleaned := targetfs.CleanPath(rawPath)
		if cleaned == "" {
			continue
		}
		item := h.extractOneSource(ctx, idx+1, cleaned, req.SessionID, req.ConnectionID, maxChars)
		sources = append(sources, item)

		ctxBuf.WriteString(fmt.Sprintf("### [资料 %d] %s（%s）\n", item.Index, item.Name, item.Path))
		if item.Error != "" {
			ctxBuf.WriteString("（读取提示：" + item.Error + "）\n\n")
		} else if strings.TrimSpace(item.Excerpt) != "" {
			ctxBuf.WriteString(item.Excerpt + "\n\n")
		}
	}

	return SourcesExtractResponse{
		OK:               true,
		Sources:          sources,
		AssembledContext: strings.TrimSpace(ctxBuf.String()),
	}
}

func (h *Handler) extractOneSource(ctx context.Context, idx int, filePath, sessionID, connectionID string, maxChars int) ExtractedSource {
	baseName := path.Base(strings.ReplaceAll(filePath, `\`, `/`))
	ext := strings.ToLower(path.Ext(baseName))
	out := ExtractedSource{
		Index: idx,
		Path:  filePath,
		Name:  baseName,
		Kind:  "text",
	}

	if h.options.Resolver == nil {
		out.Error = "文件系统解析器未初始化"
		return out
	}

	var maxBytes int64 = maxTextSourceBytes
	switch ext {
	case ".docx":
		out.Kind = "docx"
		maxBytes = maxDocxBytes
	case ".xlsx", ".csv", ".tsv":
		out.Kind = "sheet"
		maxBytes = maxSheetBytes
	case ".pptx":
		out.Kind = "pptx"
		maxBytes = maxPptxBytes
	case ".md", ".markdown":
		out.Kind = "markdown"
	case ".srt", ".vtt":
		out.Kind = "subtitle"
	case ".mp4", ".m4v", ".webm", ".mov", ".mkv", ".ogv":
		out.Kind = "video"
		sizeLabel := ""
		if st, err := os.Stat(filePath); err == nil && !st.IsDir() && st.Size() > 0 {
			sizeLabel = fmt.Sprintf("（大小 %.1f MB）", float64(st.Size())/(1024*1024))
		}
		out.Excerpt = fmt.Sprintf("（视频文件 `%s`%s，可在右侧视频审片台逐帧截帧引用、打点批注或结合同目录字幕/脚本进行分析修改）", filePath, sizeLabel)
		out.CharCount = len([]rune(out.Excerpt))
		return out
	case ".wav", ".mp3", ".m4a", ".ogg", ".flac", ".aac", ".opus":
		out.Kind = "audio"
		sizeLabel := ""
		if st, err := os.Stat(filePath); err == nil && !st.IsDir() && st.Size() > 0 {
			sizeLabel = fmt.Sprintf("（大小 %.2f MB）", float64(st.Size())/(1024*1024))
		}
		out.Excerpt = fmt.Sprintf("（音频文件 `%s`%s，可在右侧音频试听器播放、A-B 循环核对或引用精确时间点）", filePath, sizeLabel)
		out.CharCount = len([]rune(out.Excerpt))
		return out
	case ".pdf":
		out.Kind = "pdf"
		out.Excerpt = fmt.Sprintf("（PDF 文档 `%s`，可在对话中结合此路径深入查阅完整页面内容）", filePath)
		out.CharCount = len([]rune(out.Excerpt))
		return out
	case ".doc", ".xls", ".ppt", ".wps", ".et", ".dps":
		out.Kind = "unsupported"
		out.Error = "传统二进制 Office/WPS 格式暂不支持自动抽取，请先另存为 .docx/.xlsx/.pptx 或在对话中引用路径"
		return out
	}

	raw, info, err := h.readTargetFile(ctx, filePath, sessionID, connectionID, maxBytes)
	if err != nil {
		out.Error = err.Error()
		return out
	}
	if info.Name != "" {
		out.Name = info.Name
	}

	var text string
	switch ext {
	case ".docx":
		preview, err := ParseDocxBytes(out.Name, filePath, raw)
		if err != nil {
			out.Error = err.Error()
			return out
		}
		text = docxPreviewToText(preview)
	case ".xlsx", ".csv", ".tsv":
		preview, err := ParseSheetBytes(out.Name, filePath, raw, 80, 20)
		if err != nil {
			out.Error = err.Error()
			return out
		}
		text = sheetPreviewToText(preview)
	case ".pptx":
		preview, err := ParsePptxBytes(out.Name, filePath, raw)
		if err != nil {
			out.Error = err.Error()
			return out
		}
		text = pptxPreviewToText(preview)
	case ".srt", ".vtt":
		text = subtitleBytesToText(raw)
	default:
		text = strings.TrimSpace(string(raw))
	}

	runes := []rune(strings.TrimSpace(text))
	out.CharCount = len(runes)
	if len(runes) > maxChars {
		out.Excerpt = string(runes[:maxChars]) + "\n…（已按长度上限截断）"
		out.Truncated = true
	} else {
		out.Excerpt = string(runes)
	}
	return out
}

func subtitleBytesToText(raw []byte) string {
	cleaned := strings.TrimPrefix(string(raw), "\uFEFF")
	cleaned = strings.ReplaceAll(cleaned, "\r\n", "\n")
	cleaned = strings.ReplaceAll(cleaned, "\r", "\n")
	blocks := strings.Split(cleaned, "\n\n")
	var out strings.Builder
	for _, block := range blocks {
		rawLines := strings.Split(block, "\n")
		lines := make([]string, 0, len(rawLines))
		for _, l := range rawLines {
			if t := strings.TrimSpace(l); t != "" {
				lines = append(lines, t)
			}
		}
		if len(lines) == 0 {
			continue
		}
		upper := strings.ToUpper(lines[0])
		if strings.HasPrefix(upper, "WEBVTT") || strings.HasPrefix(upper, "NOTE") || strings.HasPrefix(upper, "STYLE") {
			continue
		}
		timingIdx := -1
		for i := 0; i < len(lines) && i < 3; i++ {
			if strings.Contains(lines[i], "-->") {
				timingIdx = i
				break
			}
		}
		if timingIdx < 0 || timingIdx+1 >= len(lines) {
			continue
		}
		startPart := strings.TrimSpace(strings.Split(lines[timingIdx], "-->")[0])
		startPart = strings.ReplaceAll(startPart, ",", ".")
		text := strings.Join(lines[timingIdx+1:], " ")
		if text != "" {
			out.WriteString(fmt.Sprintf("[%s] %s\n", startPart, text))
		}
	}
	if out.Len() == 0 {
		return strings.TrimSpace(cleaned)
	}
	return strings.TrimSpace(out.String())
}

func docxPreviewToText(p *DocxPreviewResponse) string {
	if p == nil {
		return ""
	}
	var b strings.Builder
	for _, blk := range p.Blocks {
		var line strings.Builder
		for _, r := range blk.Runs {
			line.WriteString(r.Text)
		}
		txt := strings.TrimSpace(line.String())
		switch blk.Kind {
		case "heading":
			if txt != "" {
				b.WriteString(strings.Repeat("#", max(1, min(blk.Level, 4))) + " " + txt + "\n")
			}
		case "list_item":
			if txt != "" {
				b.WriteString("- " + txt + "\n")
			}
		case "table":
			for ri, row := range blk.Rows {
				cols := make([]string, 0, len(row.Cells))
				for _, c := range row.Cells {
					cols = append(cols, strings.ReplaceAll(strings.TrimSpace(c.Text), "\n", " "))
				}
				b.WriteString("| " + strings.Join(cols, " | ") + " |\n")
				if ri == 0 {
					seps := make([]string, len(cols))
					for i := range seps {
						seps[i] = "---"
					}
					b.WriteString("| " + strings.Join(seps, " | ") + " |\n")
				}
			}
		default:
			if txt != "" {
				b.WriteString(txt + "\n")
			}
		}
	}
	return strings.TrimSpace(b.String())
}

func sheetPreviewToText(p *SheetPreviewResponse) string {
	if p == nil {
		return ""
	}
	var b strings.Builder
	for si, sh := range p.Sheets {
		if si >= 3 {
			break
		}
		b.WriteString(fmt.Sprintf("工作表「%s」（共 %d 行）：\n", sh.Name, sh.TotalRows))
		if len(sh.Columns) > 0 {
			b.WriteString("| " + strings.Join(sh.Columns, " | ") + " |\n")
			seps := make([]string, len(sh.Columns))
			for i := range seps {
				seps[i] = "---"
			}
			b.WriteString("| " + strings.Join(seps, " | ") + " |\n")
		}
		limit := min(len(sh.Rows), 40)
		for ri := 0; ri < limit; ri++ {
			row := sh.Rows[ri]
			vals := make([]string, 0, len(sh.Columns))
			for ci := range sh.Columns {
				var v any
				if ci < len(row) {
					v = row[ci]
				}
				vals = append(vals, strings.ReplaceAll(fmt.Sprint(v), "|", `\|`))
			}
			b.WriteString("| " + strings.Join(vals, " | ") + " |\n")
		}
		b.WriteString("\n")
	}
	return strings.TrimSpace(b.String())
}

func pptxPreviewToText(p *PptxPreviewResponse) string {
	if p == nil {
		return ""
	}
	var b strings.Builder
	for _, sl := range p.Slides {
		b.WriteString(fmt.Sprintf("## 第 %d 页：%s\n", sl.Index, sl.Title))
		for _, para := range sl.Paragraphs {
			if strings.TrimSpace(para.Text) != "" {
				b.WriteString("- " + strings.TrimSpace(para.Text) + "\n")
			}
		}
		if strings.TrimSpace(sl.Notes) != "" {
			b.WriteString("> 备注：" + strings.TrimSpace(sl.Notes) + "\n")
		}
		b.WriteString("\n")
	}
	return strings.TrimSpace(b.String())
}

func synthesizeActionSpec(action string) (defaultTitle string, systemDirective string) {
	switch strings.ToLower(strings.TrimSpace(action)) {
	case "compare":
		return "多文档交叉对比分析", "请逐项对比各份参考资料之间的核心异同、口径差异、数据冲突与共识结论。输出结构：1) 核心差异总览表（Markdown 表格）；2) 关键一致点；3) 冲突/待确认项与建议。每条关键结论请用 `[资料 N]` 标注出处。"
	case "table":
		return "多文档结构化汇总表", "请从参考资料中提炼所有关键维度、指标、责任人、时间节点或方案参数，整理为清晰规范的 Markdown 汇总对比表，并在表后给出 3-5 条关键数据洞察，标注 `[资料 N]` 出处。"
	case "ppt_outline":
		return "汇报 PPT 演示大纲", "请综合参考资料规划一份 8-12 页的高管汇报 PPT 大纲。每页使用 `## 第 X 页：标题` 开头，包含 3-5 条凝练要点（每条不超过 24 字）及 1 条「配图/图表建议」，可直接一键导出为 PPT。"
	case "review":
		return "文档与合同合规审查报告", "请站在专业法务/合规/项目评审视角审阅参考资料，梳理潜在风险点、条款遗漏、逻辑漏洞与修改建议。输出：1) 风险等级汇总表（高/中/低、涉及原文位置 `[资料 N]`、风险说明、修订建议）；2) 关键修订措辞参考。"
	case "draft":
		return "新建办公文档", "请严格根据用户的场景模板与核心要求，起草一份结构完整、逻辑严密、可直接对外流转或导出为 Word/PPT 的高质量中文成品文档。直接输出规范 Markdown 正文，不要夹杂寒暄解释。"
	case "summary":
		return "多文档综合摘要报告", "请通盘提炼所有参考资料的核心主旨、关键数据、核心结论与下一步行动建议，用 `[资料 N]` 标注关键事实出处，形成可直接发给团队或领导审阅的精炼摘要。"
	default:
		return "智能办公综合成品", "请结合用户提供的参考资料与具体要求，产出结构清晰、可直接编辑和导出的中文 Markdown 成品文档；若引用资料事实请用 `[资料 N]` 标注出处。"
	}
}

func (h *Handler) synthesizeSources(ctx context.Context, req SourcesSynthesizeRequest) (*SourcesSynthesizeResponse, error) {
	action := strings.TrimSpace(req.Action)
	if action == "" {
		action = "summary"
	}
	if len(req.Paths) == 0 && strings.TrimSpace(req.Instruction) == "" {
		return nil, fmt.Errorf("请至少选择一份参考资料，或填写起草/分析要求")
	}

	var extracted SourcesExtractResponse
	if len(req.Paths) > 0 {
		extracted = h.extractSources(ctx, SourcesExtractRequest{
			Paths:        req.Paths,
			SessionID:    req.SessionID,
			ConnectionID: req.ConnectionID,
		})
	}

	defaultTitle, directive := synthesizeActionSpec(action)
	title := strings.TrimSpace(req.Title)
	if title == "" {
		title = defaultTitle
	}

	personaPrompt := officepersona.PromptSection(officepersona.Load(ctx, h.options.Store))
	systemParts := []string{
		"你是星序智能办公写作与综合分析专家。请直接输出干净、结构化的中文 Markdown 成品文档，不要包裹外层 ```markdown 围栏，不要寒暄。",
		directive,
	}
	if personaPrompt != "" {
		systemParts = append(systemParts, personaPrompt)
	}
	systemPrompt := strings.Join(systemParts, "\n\n")

	var userBuf strings.Builder
	userBuf.WriteString("目标标题：" + title + "\n")
	if instr := strings.TrimSpace(req.Instruction); instr != "" {
		userBuf.WriteString("\n【任务要求】\n" + instr + "\n")
	}
	if extracted.AssembledContext != "" {
		userBuf.WriteString("\n【参考资料】\n" + extracted.AssembledContext + "\n")
	}
	userPrompt := strings.TrimSpace(userBuf.String())
	assembledPrompt := fmt.Sprintf("【要求】\n%s\n\n%s", directive, userPrompt)

	llmCfg := loadHelperLLMConfig(ctx, h.options.Store)
	if strings.TrimSpace(req.ProviderID) != "" {
		if resolved, ok := aiprovider.Resolve(ctx, h.options.Store, req.ProviderID, req.Model); ok {
			llmCfg = resolved
		}
	} else if strings.TrimSpace(req.Model) != "" {
		llmCfg.Model = strings.TrimSpace(req.Model)
	}

	llmConfigured := llmCfg.IsConfigured()
	if strings.EqualFold(strings.TrimSpace(req.Mode), "prompt_only") || !llmConfigured {
		draft := fmt.Sprintf("# %s\n\n", title)
		if instr := strings.TrimSpace(req.Instruction); instr != "" {
			draft += "## 需求要点\n\n" + instr + "\n\n"
		}
		if len(extracted.Sources) > 0 {
			draft += "## 已关联参考资料\n\n"
			for _, src := range extracted.Sources {
				draft += fmt.Sprintf("- **[资料 %d]** %s（%d 字）\n", src.Index, src.Name, src.CharCount)
			}
		}
		return &SourcesSynthesizeResponse{
			OK:              true,
			Mode:            "prompt_only",
			LLMConfigured:   llmConfigured,
			Model:           llmCfg.Model,
			Title:           title,
			Markdown:        strings.TrimSpace(draft),
			AssembledPrompt: assembledPrompt,
			Sources:         extracted.Sources,
			Message:         "未配置通用 AI 模型（可在「设置 → AI 供应商」直接选择已接入 Agent 或填写 API Key），已组装多文档提示词，可直接发送到对话生成。",
		}, nil
	}

	callCtx, cancel := context.WithTimeout(ctx, 120*time.Second)
	defer cancel()

	res, err := llmcompat.CompleteChat(callCtx, h.httpClient, llmCfg.ToChatRequest(4096, []llmcompat.ChatMessage{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: userPrompt},
	}))
	if err != nil {
		return nil, fmt.Errorf("调用 AI 供应商生成失败：%w", err)
	}
	if res.Status >= 400 || strings.TrimSpace(res.Content) == "" {
		detail := llmcompat.ExtractErrorMessage(res.Raw)
		if detail != "" {
			return nil, fmt.Errorf("AI 供应商返回错误（HTTP %d）：%s", res.Status, detail)
		}
		return nil, fmt.Errorf("AI 供应商返回异常（HTTP %d）", res.Status)
	}

	cleaned := strings.TrimSpace(res.Content)
	cleaned = strings.TrimPrefix(cleaned, "```markdown\n")
	cleaned = strings.TrimPrefix(cleaned, "```md\n")
	cleaned = strings.TrimSuffix(cleaned, "\n```")

	return &SourcesSynthesizeResponse{
		OK:              true,
		Mode:            "llm",
		LLMConfigured:   true,
		Model:           res.Model,
		Title:           title,
		Markdown:        strings.TrimSpace(cleaned),
		AssembledPrompt: assembledPrompt,
		Sources:         extracted.Sources,
	}, nil
}

func stringField(v any) string {
	if s, ok := v.(string); ok {
		return strings.TrimSpace(s)
	}
	return ""
}
