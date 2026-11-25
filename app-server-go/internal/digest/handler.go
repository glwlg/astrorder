package digest

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"astrorder.dev/app-server/internal/auth"
	"astrorder.dev/app-server/internal/store"
)

const (
	BasePath        = "/api/v1/digest"
	maxJSONReqBytes = 4 << 20
)

type BrowserAuthorizer interface {
	AuthorizeBrowser(*http.Request) *auth.Error
}

// SessionHistoryReader retrieves recent projected transcript messages for a session.
type SessionHistoryReader func(ctx context.Context, agentID, sessionID string, limit int) ([]map[string]any, error)

type Options struct {
	Store         *store.Store
	Authorizer    BrowserAuthorizer
	AuditDir      string
	HistoryReader SessionHistoryReader
	HTTPClient    *http.Client
	Logger        *slog.Logger
}

type handler struct {
	store         *store.Store
	authorizer    BrowserAuthorizer
	auditDir      string
	historyReader SessionHistoryReader
	httpClient    *http.Client
	logger        *slog.Logger
	mu            sync.Mutex
}

func NewHandler(options Options) http.Handler {
	client := options.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 120 * time.Second}
	}
	logger := options.Logger
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return &handler{
		store:         options.Store,
		authorizer:    options.Authorizer,
		auditDir:      strings.TrimSpace(options.AuditDir),
		historyReader: options.HistoryReader,
		httpClient:    client,
		logger:        logger,
	}
}

// Matches reports whether this package owns the request route.
func Matches(method, reqPath string) bool {
	switch reqPath {
	case BasePath + "/collect", BasePath + "/generate":
		return method == http.MethodPost
	case BasePath + "/history":
		return method == http.MethodGet || method == http.MethodPost || method == http.MethodDelete
	case BasePath + "/templates":
		return method == http.MethodGet || method == http.MethodPut || method == http.MethodDelete
	default:
		return false
	}
}

func (h *handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if h.authorizer == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "Authentication is not configured")
		return
	}
	if failure := h.authorizer.AuthorizeBrowser(r); failure != nil {
		writeJSONError(w, failure.Status, failure.Detail)
		return
	}

	switch r.URL.Path {
	case BasePath + "/collect":
		h.handleCollect(w, r)
	case BasePath + "/generate":
		h.handleGenerate(w, r)
	case BasePath + "/history":
		switch r.Method {
		case http.MethodGet:
			h.handleListHistory(w, r)
		case http.MethodPost:
			h.handleSaveHistory(w, r)
		case http.MethodDelete:
			h.handleDeleteHistory(w, r)
		default:
			writeJSONError(w, http.StatusMethodNotAllowed, "Method Not Allowed")
		}
	case BasePath + "/templates":
		switch r.Method {
		case http.MethodGet:
			h.handleListTemplates(w, r)
		case http.MethodPut:
			h.handlePutTemplate(w, r)
		case http.MethodDelete:
			h.handleDeleteTemplate(w, r)
		default:
			writeJSONError(w, http.StatusMethodNotAllowed, "Method Not Allowed")
		}
	default:
		writeJSONError(w, http.StatusNotFound, "Not Found")
	}
}

func (h *handler) handleCollect(w http.ResponseWriter, r *http.Request) {
	var req CollectRequest
	if r.Body != nil {
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxJSONReqBytes)).Decode(&req); err != nil && err != io.EOF {
			writeJSONError(w, http.StatusBadRequest, "无效的采集参数 JSON")
			return
		}
	}
	bundle, err := h.Collect(r.Context(), req)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "采集工作素材失败: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, bundle)
}

func (h *handler) handleGenerate(w http.ResponseWriter, r *http.Request) {
	var req GenerateRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxJSONReqBytes)).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "无效的生成请求 JSON")
		return
	}
	resp, err := h.Generate(r.Context(), req)
	if err != nil {
		writeJSONError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *handler) handleListHistory(w http.ResponseWriter, r *http.Request) {
	items, err := h.listHistory(r.Context())
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "读取历史记录失败")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (h *handler) handleSaveHistory(w http.ResponseWriter, r *http.Request) {
	var rec DigestHistoryRecord
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxJSONReqBytes)).Decode(&rec); err != nil {
		writeJSONError(w, http.StatusBadRequest, "无效的历史记录 JSON")
		return
	}
	saved, err := h.saveHistory(r.Context(), rec)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, saved)
}

func (h *handler) handleDeleteHistory(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.URL.Query().Get("id"))
	if err := h.deleteHistory(r.Context(), id); err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "id": id})
}

func (h *handler) handleListTemplates(w http.ResponseWriter, r *http.Request) {
	items, err := h.listAllTemplates(r.Context())
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "读取模板列表失败")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (h *handler) handlePutTemplate(w http.ResponseWriter, r *http.Request) {
	var tpl DigestTemplate
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxJSONReqBytes)).Decode(&tpl); err != nil {
		writeJSONError(w, http.StatusBadRequest, "无效的模板 JSON")
		return
	}
	saved, err := h.saveCustomTemplate(r.Context(), tpl)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, saved)
}

func (h *handler) handleDeleteTemplate(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.URL.Query().Get("id"))
	if err := h.deleteCustomTemplate(r.Context(), id); err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "id": id})
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
