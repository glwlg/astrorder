package toolchain

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"astrorder.dev/app-server/internal/auth"
	"astrorder.dev/app-server/internal/envtarget"
)

const routePrefix = "/api/v1/toolchain/"

type BrowserAuthorizer interface {
	AuthorizeBrowser(*http.Request) *auth.Error
}

type Handler struct {
	service    *Service
	authorizer BrowserAuthorizer
}

func NewHandler(service *Service, authorizer BrowserAuthorizer) *Handler {
	return &Handler{service: service, authorizer: authorizer}
}

func Matches(method, path string) bool {
	if !strings.HasPrefix(path, routePrefix) {
		return false
	}
	clean := strings.TrimRight(path, "/")
	switch {
	case clean == "/api/v1/toolchain/targets":
		return method == http.MethodGet
	case clean == "/api/v1/toolchain/status":
		return method == http.MethodGet
	case clean == "/api/v1/toolchain/agent-engines":
		return method == http.MethodGet
	case clean == "/api/v1/toolchain/network":
		return method == http.MethodGet || method == http.MethodPut
	case clean == "/api/v1/toolchain/validate":
		return method == http.MethodPost
	case clean == "/api/v1/toolchain/executable":
		return method == http.MethodPut
	case clean == "/api/v1/toolchain/install/jobs":
		return method == http.MethodGet || method == http.MethodPost
	case clean == "/api/v1/toolchain/onboarding/configure":
		return method == http.MethodPost
	case clean == "/api/v1/toolchain/oauth/start":
		return method == http.MethodPost
	case clean == "/api/v1/toolchain/oauth/status" || strings.HasPrefix(clean, "/api/v1/toolchain/oauth/status/"):
		return method == http.MethodGet
	case clean == "/api/v1/toolchain/oauth/cancel":
		return method == http.MethodPost
	case strings.HasPrefix(clean, "/api/v1/toolchain/install/jobs/") && strings.HasSuffix(clean, "/cancel"):
		return method == http.MethodPost
	case strings.HasPrefix(clean, "/api/v1/toolchain/install/jobs/"):
		return method == http.MethodGet
	}
	return false
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if h.authorizer != nil {
		if failure := h.authorizer.AuthorizeBrowser(r); failure != nil {
			writeJSON(w, failure.Status, map[string]string{"detail": failure.Detail})
			return
		}
	}
	w.Header().Set("Cache-Control", "no-store")
	clean := strings.TrimRight(r.URL.Path, "/")
	switch {
	case clean == "/api/v1/toolchain/targets" && r.Method == http.MethodGet:
		h.handleTargets(w, r)
	case clean == "/api/v1/toolchain/status" && r.Method == http.MethodGet:
		h.handleStatus(w, r)
	case clean == "/api/v1/toolchain/agent-engines" && r.Method == http.MethodGet:
		writeJSON(w, http.StatusOK, map[string]any{"items": RegisteredAgentEngines()})
	case clean == "/api/v1/toolchain/network" && r.Method == http.MethodGet:
		h.handleGetNetwork(w, r)
	case clean == "/api/v1/toolchain/network" && r.Method == http.MethodPut:
		h.handleSaveNetwork(w, r)
	case clean == "/api/v1/toolchain/validate" && r.Method == http.MethodPost:
		h.handleValidate(w, r)
	case clean == "/api/v1/toolchain/executable" && r.Method == http.MethodPut:
		h.handleSaveExecutable(w, r)
	case clean == "/api/v1/toolchain/install/jobs" && r.Method == http.MethodGet:
		writeJSON(w, http.StatusOK, map[string]any{"items": h.service.ListInstallJobs()})
	case clean == "/api/v1/toolchain/install/jobs" && r.Method == http.MethodPost:
		h.handleStartInstallJob(w, r)
	case clean == "/api/v1/toolchain/onboarding/configure" && r.Method == http.MethodPost:
		h.handleConfigureOnboarding(w, r)
	case clean == "/api/v1/toolchain/oauth/start" && r.Method == http.MethodPost:
		h.handleStartOAuth(w, r)
	case (clean == "/api/v1/toolchain/oauth/status" || strings.HasPrefix(clean, "/api/v1/toolchain/oauth/status/")) && r.Method == http.MethodGet:
		h.handleGetOAuthStatus(w, r)
	case clean == "/api/v1/toolchain/oauth/cancel" && r.Method == http.MethodPost:
		h.handleCancelOAuth(w, r)
	case strings.HasPrefix(clean, "/api/v1/toolchain/install/jobs/") && strings.HasSuffix(clean, "/cancel") && r.Method == http.MethodPost:
		id := strings.TrimSuffix(strings.TrimPrefix(clean, "/api/v1/toolchain/install/jobs/"), "/cancel")
		job, err := h.service.CancelInstallJob(id)
		if errors.Is(err, ErrJobNotFound) {
			writeJSON(w, http.StatusNotFound, map[string]string{"detail": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, job)
	case strings.HasPrefix(clean, "/api/v1/toolchain/install/jobs/") && r.Method == http.MethodGet:
		id := strings.TrimPrefix(clean, "/api/v1/toolchain/install/jobs/")
		job, err := h.service.GetInstallJob(id)
		if errors.Is(err, ErrJobNotFound) {
			writeJSON(w, http.StatusNotFound, map[string]string{"detail": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, job)
	default:
		writeJSON(w, http.StatusNotFound, map[string]string{"detail": "Route not found"})
	}
}

func (h *Handler) handleTargets(w http.ResponseWriter, r *http.Request) {
	targets, err := h.service.ListTargets(r.Context())
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": targets})
}

func (h *Handler) handleStatus(w http.ResponseWriter, r *http.Request) {
	envID := strings.TrimSpace(r.URL.Query().Get("environment"))
	if envID == "" {
		envID = "local"
	}
	report, err := h.service.Inspect(r.Context(), envID)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, report)
}

func (h *Handler) handleValidate(w http.ResponseWriter, r *http.Request) {
	var body struct {
		EnvironmentID string `json:"environment_id"`
		ToolID        string `json:"tool_id"`
		Path          string `json:"path"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 32<<10)).Decode(&body); err != nil {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"detail": "请求参数格式无效"})
		return
	}
	if strings.TrimSpace(body.EnvironmentID) == "" {
		body.EnvironmentID = "local"
	}
	result, err := h.service.ValidateExecutable(r.Context(), body.EnvironmentID, body.ToolID, body.Path)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (h *Handler) handleSaveExecutable(w http.ResponseWriter, r *http.Request) {
	var body struct {
		EnvironmentID string `json:"environment_id"`
		ToolID        string `json:"tool_id"`
		Path          string `json:"path"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 32<<10)).Decode(&body); err != nil {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"detail": "请求参数格式无效"})
		return
	}
	if strings.TrimSpace(body.EnvironmentID) == "" {
		body.EnvironmentID = "local"
	}
	result, err := h.service.SaveExecutable(r.Context(), body.EnvironmentID, body.ToolID, body.Path)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (h *Handler) handleStartInstallJob(w http.ResponseWriter, r *http.Request) {
	var body InstallRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&body); err != nil {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"detail": "安装请求格式无效"})
		return
	}
	job, err := h.service.StartInstallJob(r.Context(), body)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, job)
}

func (h *Handler) handleConfigureOnboarding(w http.ResponseWriter, r *http.Request) {
	var body OnboardingConfigureRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&body); err != nil {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"detail": "初始化配置请求格式无效"})
		return
	}
	result, err := h.service.ConfigureOnboarding(r.Context(), body)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (h *Handler) handleStartOAuth(w http.ResponseWriter, r *http.Request) {
	var body OAuthStartRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&body); err != nil {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"detail": "授权登录请求格式无效"})
		return
	}
	result, err := h.service.StartOAuth(r.Context(), body)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (h *Handler) handleGetOAuthStatus(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.URL.Query().Get("id"))
	if id == "" {
		clean := strings.TrimRight(r.URL.Path, "/")
		if strings.HasPrefix(clean, "/api/v1/toolchain/oauth/status/") {
			id = strings.TrimSpace(strings.TrimPrefix(clean, "/api/v1/toolchain/oauth/status/"))
		}
	}
	result, err := h.service.GetOAuthStatus(id)
	if errors.Is(err, ErrOAuthNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"detail": err.Error()})
		return
	}
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (h *Handler) handleCancelOAuth(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.URL.Query().Get("id"))
	if id == "" && r.Body != nil {
		var body struct {
			ID string `json:"id"`
		}
		_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10)).Decode(&body)
		id = strings.TrimSpace(body.ID)
	}
	result, err := h.service.CancelOAuth(id)
	if errors.Is(err, ErrOAuthNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"detail": err.Error()})
		return
	}
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (h *Handler) handleGetNetwork(w http.ResponseWriter, r *http.Request) {
	settings, err := h.service.GetNetworkSettings(r.Context())
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, settings)
}

func (h *Handler) handleSaveNetwork(w http.ResponseWriter, r *http.Request) {
	var body struct {
		EnvironmentID string `json:"environment_id"`
		ProxyURL      string `json:"proxy_url"`
		Mirror        string `json:"mirror"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&body); err != nil {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"detail": "网络配置格式无效"})
		return
	}
	settings, err := h.service.SaveNetworkSettings(r.Context(), body.EnvironmentID, NetworkProfile{
		ProxyURL: body.ProxyURL,
		Mirror:   body.Mirror,
	})
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, settings)
}

func writeServiceError(w http.ResponseWriter, err error) {
	var targetErr *envtarget.Error
	if errors.As(err, &targetErr) {
		writeJSON(w, targetErr.Status, map[string]string{"detail": targetErr.Detail})
		return
	}
	writeJSON(w, http.StatusInternalServerError, map[string]string{"detail": err.Error()})
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}
