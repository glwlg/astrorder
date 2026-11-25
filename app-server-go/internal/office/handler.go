package office

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"astrorder.dev/app-server/internal/auth"
	"astrorder.dev/app-server/internal/store"
	"astrorder.dev/app-server/internal/targetfs"
)

const (
	BasePath        = "/api/v1/office"
	maxExportReqLen = 32 << 20 // allow base64 diagrams & large tables up to 32 MiB
)

type BrowserAuthorizer interface {
	AuthorizeBrowser(*http.Request) *auth.Error
}

type Options struct {
	Store      *store.Store
	Authorizer BrowserAuthorizer
	Resolver   *targetfs.Resolver
	HTTPClient *http.Client
}

type Handler struct {
	options    Options
	httpClient *http.Client
}

func NewHandler(options Options) http.Handler {
	client := options.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 90 * time.Second}
	}
	return &Handler{options: options, httpClient: client}
}

// Matches reports whether this handler owns the requested method and path.
func Matches(method, reqPath string) bool {
	switch reqPath {
	case BasePath + "/export/docx",
		BasePath + "/export/xlsx",
		BasePath + "/export/pptx",
		BasePath + "/export/save",
		BasePath + "/refine",
		BasePath + "/sources/extract",
		BasePath + "/sources/synthesize":
		return method == http.MethodPost
	case BasePath + "/preview/docx",
		BasePath + "/preview/sheet",
		BasePath + "/preview/pptx",
		BasePath + "/recent-docs":
		return method == http.MethodGet
	case BasePath + "/profile":
		return method == http.MethodGet || method == http.MethodPut
	default:
		return false
	}
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if h.options.Authorizer == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "Authentication is not configured")
		return
	}
	if failure := h.options.Authorizer.AuthorizeBrowser(r); failure != nil {
		writeJSONError(w, failure.Status, failure.Detail)
		return
	}

	switch r.URL.Path {
	case BasePath + "/export/docx":
		h.handleExportDocx(w, r)
	case BasePath + "/export/xlsx":
		h.handleExportXlsx(w, r)
	case BasePath + "/export/pptx":
		h.handleExportPptx(w, r)
	case BasePath + "/export/save":
		h.handleExportSave(w, r)
	case BasePath + "/refine":
		h.handleRefine(w, r)
	case BasePath + "/profile":
		if r.Method == http.MethodGet {
			h.handleGetProfile(w, r)
		} else {
			h.handlePutProfile(w, r)
		}
	case BasePath + "/recent-docs":
		h.handleRecentDocs(w, r)
	case BasePath + "/sources/extract":
		h.handleExtractSources(w, r)
	case BasePath + "/sources/synthesize":
		h.handleSynthesizeSources(w, r)
	case BasePath + "/preview/docx":
		h.handlePreviewDocx(w, r)
	case BasePath + "/preview/sheet":
		h.handlePreviewSheet(w, r)
	case BasePath + "/preview/pptx":
		h.handlePreviewPptx(w, r)
	default:
		writeJSONError(w, http.StatusNotFound, "Not Found")
	}
}

func (h *Handler) handleExportDocx(w http.ResponseWriter, r *http.Request) {
	var req DocxExportRequest
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxExportReqLen))
	if err := dec.Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "无效的 Word 导出请求 JSON")
		return
	}
	if strings.TrimSpace(req.Markdown) == "" && strings.TrimSpace(req.Title) == "" {
		writeJSONError(w, http.StatusBadRequest, "导出内容不能为空")
		return
	}
	data, err := ExportMarkdownToDocx(req)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "生成 Word 文档失败: "+err.Error())
		return
	}

	fileName := sanitizeDownloadName(req.FileName, req.Title, ".docx", "document.docx")
	w.Header().Set("Content-Type", "application/vnd.openxmlformats-officedocument.wordprocessingml.document")
	w.Header().Set("Content-Disposition", formatContentDisposition(fileName))
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

func (h *Handler) handleExportXlsx(w http.ResponseWriter, r *http.Request) {
	var req XlsxExportRequest
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxExportReqLen))
	dec.UseNumber()
	if err := dec.Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "无效的 Excel 导出请求 JSON")
		return
	}
	data, err := ExportSheetsToXlsx(req)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}

	fileName := sanitizeDownloadName(req.FileName, "", ".xlsx", "export.xlsx")
	w.Header().Set("Content-Type", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet")
	w.Header().Set("Content-Disposition", formatContentDisposition(fileName))
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

func (h *Handler) handleExportPptx(w http.ResponseWriter, r *http.Request) {
	var req PptxExportRequest
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxExportReqLen))
	if err := dec.Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "无效的 PPT 导出请求 JSON")
		return
	}
	if strings.TrimSpace(req.Markdown) == "" && strings.TrimSpace(req.Title) == "" {
		writeJSONError(w, http.StatusBadRequest, "导出内容不能为空")
		return
	}
	data, err := ExportMarkdownToPptx(req)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "生成 PPT 失败: "+err.Error())
		return
	}
	fileName := sanitizeDownloadName(req.FileName, req.Title, ".pptx", "presentation.pptx")
	w.Header().Set("Content-Type", "application/vnd.openxmlformats-officedocument.presentationml.presentation")
	w.Header().Set("Content-Disposition", formatContentDisposition(fileName))
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

func (h *Handler) handlePreviewDocx(w http.ResponseWriter, r *http.Request) {
	if h.options.Resolver == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "Target filesystem is not configured")
		return
	}
	q := r.URL.Query()
	filePath := targetfs.CleanPath(q.Get("path"))
	if filePath == "" {
		writeJSONError(w, http.StatusUnprocessableEntity, "path is required")
		return
	}
	raw, info, err := h.readTargetFile(r.Context(), filePath, q.Get("session_id"), q.Get("connection_id"), maxDocxBytes)
	if err != nil {
		writeTargetErr(w, err)
		return
	}
	preview, err := ParseDocxBytes(info.Name, info.Path, raw)
	if err != nil {
		writeJSONError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, preview)
}

func (h *Handler) handlePreviewPptx(w http.ResponseWriter, r *http.Request) {
	if h.options.Resolver == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "Target filesystem is not configured")
		return
	}
	q := r.URL.Query()
	filePath := targetfs.CleanPath(q.Get("path"))
	if filePath == "" {
		writeJSONError(w, http.StatusUnprocessableEntity, "path is required")
		return
	}
	raw, info, err := h.readTargetFile(r.Context(), filePath, q.Get("session_id"), q.Get("connection_id"), maxPptxBytes)
	if err != nil {
		writeTargetErr(w, err)
		return
	}
	preview, err := ParsePptxBytes(info.Name, info.Path, raw)
	if err != nil {
		writeJSONError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, preview)
}

func (h *Handler) handleRefine(w http.ResponseWriter, r *http.Request) {
	var req RefineRequest
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<20))
	if err := dec.Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "无效的精修请求 JSON")
		return
	}
	resp, err := h.refineSelection(r.Context(), req)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *Handler) handlePreviewSheet(w http.ResponseWriter, r *http.Request) {
	if h.options.Resolver == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "Target filesystem is not configured")
		return
	}
	q := r.URL.Query()
	filePath := targetfs.CleanPath(q.Get("path"))
	if filePath == "" {
		writeJSONError(w, http.StatusUnprocessableEntity, "path is required")
		return
	}
	maxRows, _ := strconv.Atoi(q.Get("max_rows"))
	maxCols, _ := strconv.Atoi(q.Get("max_cols"))

	raw, info, err := h.readTargetFile(r.Context(), filePath, q.Get("session_id"), q.Get("connection_id"), maxSheetBytes)
	if err != nil {
		writeTargetErr(w, err)
		return
	}
	preview, err := ParseSheetBytes(info.Name, info.Path, raw, maxRows, maxCols)
	if err != nil {
		writeJSONError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, preview)
}

func (h *Handler) readTargetFile(ctx context.Context, filePath, sessionID, connectionID string, maxBytes int64) ([]byte, targetfs.Info, error) {
	resolved, err := h.options.Resolver.Resolve(ctx, targetfs.ResolveRequest{
		Workspace:    fileParent(filePath),
		SessionID:    strings.TrimSpace(sessionID),
		ConnectionID: strings.TrimSpace(connectionID),
	})
	if err != nil {
		return nil, targetfs.Info{}, err
	}
	result, err := resolved.FileSystem.Read(ctx, filePath)
	if err != nil {
		return nil, targetfs.Info{}, err
	}
	defer result.Reader.Close()
	if result.Info.IsDir {
		return nil, targetfs.Info{}, &targetfs.Error{Status: 404, Code: "file_not_found", Detail: "File was not found"}
	}
	if result.Info.Size > maxBytes {
		return nil, targetfs.Info{}, &targetfs.Error{
			Status: 413,
			Code:   "file_too_large",
			Detail: fmt.Sprintf("文件超过预览上限 (%d MB)", maxBytes>>20),
		}
	}
	raw, err := io.ReadAll(io.LimitReader(result.Reader, maxBytes+1))
	if err != nil {
		return nil, targetfs.Info{}, err
	}
	if int64(len(raw)) > maxBytes {
		return nil, targetfs.Info{}, &targetfs.Error{
			Status: 413,
			Code:   "file_too_large",
			Detail: fmt.Sprintf("文件超过预览上限 (%d MB)", maxBytes>>20),
		}
	}
	return raw, result.Info, nil
}

func fileParent(p string) string {
	cleaned := targetfs.CleanPath(p)
	if cleaned == "" {
		return ""
	}
	if strings.Contains(cleaned, "\\") || (len(cleaned) >= 2 && cleaned[1] == ':') {
		dir := filepath.Dir(cleaned)
		return dir
	}
	return path.Dir(cleaned)
}

func sanitizeDownloadName(requested, fallbackTitle, ext, defaultName string) string {
	name := strings.TrimSpace(requested)
	if name == "" {
		name = strings.TrimSpace(fallbackTitle)
	}
	if name == "" {
		return defaultName
	}
	name = strings.Map(func(r rune) rune {
		if strings.ContainsRune(`\/:*?"<>|`+"\r\n\t", r) {
			return '_'
		}
		return r
	}, name)
	name = strings.TrimSpace(strings.Trim(name, "."))
	if name == "" {
		return defaultName
	}
	if !strings.HasSuffix(strings.ToLower(name), ext) {
		name += ext
	}
	return name
}

func formatContentDisposition(fileName string) string {
	asciiFallback := strings.Map(func(r rune) rune {
		if r >= 0x20 && r <= 0x7E && r != '"' && r != '\\' && r != ';' {
			return r
		}
		return '_'
	}, fileName)
	if strings.Trim(asciiFallback, "_.") == "" {
		asciiFallback = "export" + filepath.Ext(fileName)
	}
	escaped := url.PathEscape(fileName)
	return fmt.Sprintf(`attachment; filename="%s"; filename*=UTF-8''%s`, asciiFallback, escaped)
}

func writeTargetErr(w http.ResponseWriter, err error) {
	var target *targetfs.Error
	if errors.As(err, &target) {
		writeJSONError(w, target.Status, target.Detail)
		return
	}
	if errors.Is(err, context.Canceled) {
		writeJSONError(w, 499, "Request cancelled")
		return
	}
	writeJSONError(w, http.StatusInternalServerError, "Internal server error")
}

func writeJSONError(w http.ResponseWriter, status int, detail string) {
	writeJSON(w, status, map[string]string{"detail": detail})
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}
