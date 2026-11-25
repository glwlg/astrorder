package meetings

import (
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
	BasePath        = "/api/v1/meetings"
	maxJSONReqBytes = 8 << 20
)

type BrowserAuthorizer interface {
	AuthorizeBrowser(*http.Request) *auth.Error
}

type Options struct {
	Store      *store.Store
	Authorizer BrowserAuthorizer
	HTTPClient *http.Client
	Logger     *slog.Logger
}

type handler struct {
	store      *store.Store
	authorizer BrowserAuthorizer
	httpClient *http.Client
	logger     *slog.Logger
	mu         sync.Mutex
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
		store:      options.Store,
		authorizer: options.Authorizer,
		httpClient: client,
		logger:     logger,
	}
}

// Matches reports whether this package owns the requested method and path.
func Matches(method, reqPath string) bool {
	switch reqPath {
	case BasePath:
		return method == http.MethodGet || method == http.MethodPost || method == http.MethodDelete
	case BasePath + "/summarize":
		return method == http.MethodPost
	case BasePath + "/action-items":
		return method == http.MethodGet || method == http.MethodPatch
	case BasePath + "/settings":
		return method == http.MethodGet || method == http.MethodPut
	case BasePath + "/settings/test":
		return method == http.MethodPost
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
	case BasePath:
		switch r.Method {
		case http.MethodGet:
			h.handleListMeetings(w, r)
		case http.MethodPost:
			h.handleSaveMeeting(w, r)
		case http.MethodDelete:
			h.handleDeleteMeeting(w, r)
		default:
			writeJSONError(w, http.StatusMethodNotAllowed, "Method Not Allowed")
		}
	case BasePath + "/summarize":
		h.handleSummarize(w, r)
	case BasePath + "/action-items":
		switch r.Method {
		case http.MethodGet:
			h.handleListActionItems(w, r)
		case http.MethodPatch:
			h.handlePatchActionItem(w, r)
		default:
			writeJSONError(w, http.StatusMethodNotAllowed, "Method Not Allowed")
		}
	case BasePath + "/settings":
		switch r.Method {
		case http.MethodGet:
			h.handleGetSettings(w, r)
		case http.MethodPut:
			h.handlePutSettings(w, r)
		default:
			writeJSONError(w, http.StatusMethodNotAllowed, "Method Not Allowed")
		}
	case BasePath + "/settings/test":
		h.handleTestSettings(w, r)
	default:
		writeJSONError(w, http.StatusNotFound, "Not Found")
	}
}

func (h *handler) handleListMeetings(w http.ResponseWriter, r *http.Request) {
	items, err := h.loadMeetings(r.Context())
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "读取会议记录失败")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (h *handler) handleSaveMeeting(w http.ResponseWriter, r *http.Request) {
	var rec MeetingRecord
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxJSONReqBytes)).Decode(&rec); err != nil {
		writeJSONError(w, http.StatusBadRequest, "无效的会议记录 JSON")
		return
	}
	saved, err := h.saveMeeting(r.Context(), rec)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, saved)
}

func (h *handler) handleDeleteMeeting(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.URL.Query().Get("id"))
	if err := h.deleteMeeting(r.Context(), id); err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "id": id})
}

func (h *handler) handleSummarize(w http.ResponseWriter, r *http.Request) {
	var req SummarizeRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxJSONReqBytes)).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "无效的会议总结请求 JSON")
		return
	}
	resp, err := h.Summarize(r.Context(), req)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *handler) handleListActionItems(w http.ResponseWriter, r *http.Request) {
	statusFilter := strings.TrimSpace(r.URL.Query().Get("status"))
	items, openCount, err := h.listActionItems(r.Context(), statusFilter)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "读取会议行动项失败")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"items":      items,
		"open_count": openCount,
	})
}

func (h *handler) handlePatchActionItem(w http.ResponseWriter, r *http.Request) {
	var req ActionItemPatchRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "无效的行动项更新请求 JSON")
		return
	}
	updated, err := h.patchActionItem(r.Context(), req)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

func (h *handler) handleGetSettings(w http.ResponseWriter, r *http.Request) {
	settings, err := h.getPublicSettings(r.Context())
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "读取会议配置失败")
		return
	}
	writeJSON(w, http.StatusOK, settings)
}

func (h *handler) handlePutSettings(w http.ResponseWriter, r *http.Request) {
	var req MeetingSettings
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "无效的配置 JSON")
		return
	}
	saved, err := h.saveSettings(r.Context(), req)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, saved)
}

func (h *handler) handleTestSettings(w http.ResponseWriter, r *http.Request) {
	var req *MeetingSettings
	if r.Body != nil {
		var body MeetingSettings
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&body); err == nil {
			req = &body
		}
	}
	resp := h.TestSettingsConnectivity(r.Context(), req)
	writeJSON(w, http.StatusOK, resp)
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
