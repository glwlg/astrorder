package office

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"

	"astrorder.dev/app-server/internal/targetfs"
)

// ExportSaveRequest saves an exported office document (.md, .docx, .pptx, .xlsx)
// directly into a target workspace folder without requiring a browser download round-trip.
type ExportSaveRequest struct {
	Format       string         `json:"format"` // "md" | "docx" | "pptx" | "xlsx"
	Workspace    string         `json:"workspace"`
	SessionID    string         `json:"session_id,omitempty"`
	ConnectionID string         `json:"connection_id,omitempty"`
	SubDir       string         `json:"sub_dir,omitempty"`
	FileName     string         `json:"file_name,omitempty"`
	Title        string         `json:"title,omitempty"`
	Subtitle     string         `json:"subtitle,omitempty"`
	Markdown     string         `json:"markdown,omitempty"`
	Diagrams     []DiagramImage     `json:"diagrams,omitempty"`
	Sheets       []SheetExportInput `json:"sheets,omitempty"`
	Overwrite    bool               `json:"overwrite,omitempty"`
}

func (h *Handler) handleExportSave(w http.ResponseWriter, r *http.Request) {
	if h.options.Resolver == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "文件系统服务未就绪")
		return
	}
	var req ExportSaveRequest
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxExportReqLen))
	dec.UseNumber()
	if err := dec.Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "无效的保存请求 JSON")
		return
	}

	format := strings.ToLower(strings.TrimSpace(req.Format))
	var data []byte
	var ext, fallback string
	var err error

	switch format {
	case "md", "markdown":
		if strings.TrimSpace(req.Markdown) == "" && strings.TrimSpace(req.Title) == "" {
			writeJSONError(w, http.StatusBadRequest, "保存内容不能为空")
			return
		}
		content := req.Markdown
		if strings.TrimSpace(content) == "" {
			content = "# " + strings.TrimSpace(req.Title) + "\n"
		}
		data = []byte(content)
		ext = ".md"
		fallback = "document.md"
	case "docx", "word":
		if strings.TrimSpace(req.Markdown) == "" && strings.TrimSpace(req.Title) == "" {
			writeJSONError(w, http.StatusBadRequest, "保存内容不能为空")
			return
		}
		data, err = ExportMarkdownToDocx(DocxExportRequest{
			Title:    req.Title,
			Subtitle: req.Subtitle,
			Markdown: req.Markdown,
			Diagrams: req.Diagrams,
			FileName: req.FileName,
		})
		ext = ".docx"
		fallback = "document.docx"
	case "pptx", "ppt":
		if strings.TrimSpace(req.Markdown) == "" && strings.TrimSpace(req.Title) == "" {
			writeJSONError(w, http.StatusBadRequest, "保存内容不能为空")
			return
		}
		data, err = ExportMarkdownToPptx(PptxExportRequest{
			Title:    req.Title,
			Subtitle: req.Subtitle,
			Markdown: req.Markdown,
			Diagrams: req.Diagrams,
			FileName: req.FileName,
		})
		ext = ".pptx"
		fallback = "presentation.pptx"
	case "xlsx", "excel":
		data, err = ExportSheetsToXlsx(XlsxExportRequest{
			Sheets:   req.Sheets,
			Markdown: req.Markdown,
			FileName: req.FileName,
		})
		ext = ".xlsx"
		fallback = "table.xlsx"
	default:
		writeJSONError(w, http.StatusBadRequest, "不支持的保存格式："+format)
		return
	}
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "生成文档失败："+err.Error())
		return
	}

	resolved, err := h.options.Resolver.Resolve(r.Context(), targetfs.ResolveRequest{
		Workspace:    targetfs.CleanPath(req.Workspace),
		SessionID:    strings.TrimSpace(req.SessionID),
		ConnectionID: strings.TrimSpace(req.ConnectionID),
	})
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "无法定位目标办公空间目录："+err.Error())
		return
	}
	targetDir := resolved.Workspace
	if sub := strings.TrimSpace(req.SubDir); sub != "" {
		cleanSub := strings.TrimPrefix(strings.ReplaceAll(sub, "\\", "/"), "/")
		if !strings.Contains(cleanSub, "..") {
			targetDir = filepath.Join(targetDir, filepath.FromSlash(cleanSub))
		}
	}

	fileName := sanitizeDownloadName(req.FileName, req.Title, ext, fallback)
	fileName = filepath.Base(strings.ReplaceAll(fileName, "\\", "/"))
	destPath := filepath.Join(targetDir, fileName)
	if !req.Overwrite {
		stem := strings.TrimSuffix(fileName, ext)
		for attempt := 1; attempt <= 50; attempt++ {
			if _, statErr := resolved.FileSystem.Stat(r.Context(), destPath); statErr != nil {
				break
			}
			destPath = filepath.Join(targetDir, fmt.Sprintf("%s (%d)%s", stem, attempt, ext))
		}
	}

	info, err := resolved.FileSystem.WriteAtomic(r.Context(), destPath, bytes.NewReader(data), 0o600)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "写入工作区文件失败："+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"ok":   true,
		"path": info.Path,
		"name": info.Name,
		"size": info.Size,
	})
}
