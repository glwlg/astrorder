package browser

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
)

// Registration is the endpoint descriptor for browser routes.
type Registration struct {
	Kind   string `json:"kind"`
	Method string `json:"method"`
	Path   string `json:"path"`
	Auth   string `json:"auth"`
	Owner  string `json:"owner"`
}

// Registrations returns static registrations for all browser routes.
func Registrations() []Registration {
	return []Registration{
		{Kind: "http", Method: http.MethodGet, Path: "/api/v1/browser/screenshot", Auth: "browser", Owner: "go-app"},
		{Kind: "http", Method: http.MethodPost, Path: "/api/v1/browser/navigate", Auth: "browser", Owner: "go-app"},
		{Kind: "http", Method: http.MethodPost, Path: "/api/v1/browser/interact", Auth: "browser", Owner: "go-app"},
		{Kind: "http", Method: http.MethodGet, Path: "/api/v1/browser/page-content", Auth: "browser", Owner: "go-app"},
		{Kind: "http", Method: http.MethodGet, Path: "/api/v1/browser/diagnostics", Auth: "browser", Owner: "go-app"},
		{Kind: "http", Method: http.MethodPost, Path: "/api/v1/browser/tabs/select", Auth: "browser", Owner: "go-app"},
		{Kind: "http", Method: http.MethodPost, Path: "/api/v1/browser/tabs/new", Auth: "browser", Owner: "go-app"},
		{Kind: "http", Method: http.MethodPost, Path: "/api/v1/browser/tabs/close", Auth: "browser", Owner: "go-app"},
		{Kind: "http", Method: http.MethodGet, Path: "/api/v1/browser/proxy", Auth: "browser", Owner: "go-app"},
		{Kind: "http", Method: http.MethodPost, Path: "/api/v1/browser/proxy", Auth: "browser", Owner: "go-app"},
		{Kind: "http", Method: http.MethodPost, Path: "/api/v1/system/open-browser", Auth: "browser", Owner: "go-app"},
	}
}

// Matches returns true if method and path correspond to browser endpoints.
func Matches(method, path string) bool {
	clean := strings.TrimRight(path, "/")
	switch clean {
	case "/api/v1/browser/screenshot",
		"/api/v1/browser/page-content",
		"/api/v1/browser/diagnostics":
		return method == http.MethodGet
	case "/api/v1/browser/navigate",
		"/api/v1/browser/interact",
		"/api/v1/browser/tabs/select",
		"/api/v1/browser/tabs/new",
		"/api/v1/browser/tabs/close",
		"/api/v1/system/open-browser":
		return method == http.MethodPost
	case "/api/v1/browser/proxy":
		return method == http.MethodGet || method == http.MethodPost
	default:
		return false
	}
}

// Handler implements http.Handler for browser routes.
type Handler struct {
	options Options
	manager *SessionManager
}

// NewHandler constructs a browser handler.
func NewHandler(options Options) (http.Handler, error) {
	mgr := NewSessionManager(options.CDPURL, options.NoAutoExec)
	return &Handler{
		options: options,
		manager: mgr,
	}, nil
}

// Manager exposes the underlying SessionManager.
func (h *Handler) Manager() *SessionManager {
	return h.manager
}

// CloseSession releases browser tabs and cached captures for a deleted session.
func (h *Handler) CloseSession(ctx context.Context, sessionKey string) {
	h.manager.CloseSession(ctx, sessionKey)
}

// RunTask implements agentapi.BrowserManager interface.
func (h *Handler) RunTask(ctx context.Context, input map[string]any) (map[string]any, error) {
	res, err := h.Invoke(ctx, "browser.run", input)
	if err != nil {
		return nil, err
	}
	if taskRes, ok := res.(*BrowserTaskResult); ok {
		data, err := json.Marshal(taskRes)
		if err != nil {
			return nil, err
		}
		var m map[string]any
		_ = json.Unmarshal(data, &m)
		return m, nil
	}
	if m, ok := res.(map[string]any); ok {
		return m, nil
	}
	return nil, fmt.Errorf("unexpected task result type: %T", res)
}

// CaptureScreenshot implements agentapi.BrowserManager interface.
func (h *Handler) CaptureScreenshot(ctx context.Context, sessionKey string, requireOwner bool, targetID string) (*Snapshot, error) {
	return h.manager.CaptureScreenshot(ctx, sessionKey, requireOwner, targetID)
}

// CallCDP implements agentapi.BrowserManager interface.
func (h *Handler) CallCDP(ctx context.Context, sessionKey string, method string, params map[string]any, targetID string) (map[string]any, error) {
	return h.manager.CallCDP(ctx, sessionKey, method, params, targetID)
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimRight(r.URL.Path, "/")
	if !Matches(r.Method, path) {
		writeError(w, http.StatusNotFound, "Not Found")
		return
	}

	if h.options.Authorizer == nil {
		writeError(w, http.StatusServiceUnavailable, "Authentication is not configured")
		return
	}
	if failure := h.options.Authorizer.AuthorizeBrowser(r); failure != nil {
		writeError(w, failure.Status, failure.Detail)
		return
	}

	var err error
	switch path {
	case "/api/v1/browser/screenshot":
		err = h.handleScreenshot(w, r)
	case "/api/v1/browser/navigate":
		err = h.handleNavigate(w, r)
	case "/api/v1/browser/interact":
		err = h.handleInteract(w, r)
	case "/api/v1/browser/page-content":
		err = h.handlePageContent(w, r)
	case "/api/v1/browser/diagnostics":
		err = h.handleDiagnostics(w, r)
	case "/api/v1/browser/tabs/select":
		err = h.handleTabSelect(w, r)
	case "/api/v1/browser/tabs/new":
		err = h.handleTabNew(w, r)
	case "/api/v1/browser/tabs/close":
		err = h.handleTabClose(w, r)
	case "/api/v1/browser/proxy":
		err = h.handleProxy(w, r)
	case "/api/v1/system/open-browser":
		err = h.handleOpenBrowser(w, r)
	}

	if err != nil {
		writeTargetError(w, err)
	}
}

func (h *Handler) verifySession(ctx context.Context, agentID, sessionID string) error {
	if h.options.Store == nil {
		return nil
	}
	sess, err := h.options.Store.GetSession(ctx, agentID, sessionID)
	if err != nil || sess == nil {
		return &httpError{status: http.StatusNotFound, message: "Session not found"}
	}
	return nil
}

func (h *Handler) publishMirrorUpdated(ctx context.Context, agentID, sessionID string, snap *Snapshot) {
	if h.options.Publisher == nil || snap == nil {
		return
	}
	_ = h.options.Publisher.Publish(ctx, "browser.mirror.updated", map[string]any{
		"agent_id":   agentID,
		"session_id": sessionID,
		"data": map[string]any{
			"revision":    snap.Revision,
			"url":         snap.URL,
			"title":       snap.Title,
			"session_key": snap.SessionKey,
		},
	})
}

func (h *Handler) handleScreenshot(w http.ResponseWriter, r *http.Request) error {
	q := r.URL.Query()
	agentID := q.Get("agent_id")
	sessionID := q.Get("session_id")
	if agentID == "" || sessionID == "" {
		return &httpError{status: http.StatusUnprocessableEntity, message: "agent_id and session_id are required"}
	}
	if err := h.verifySession(r.Context(), agentID, sessionID); err != nil {
		return err
	}

	sessionKey := agentID + "::" + sessionID
	refresh := q.Get("refresh") == "true"
	targetID := q.Get("target_id")

	snap := h.manager.LatestScreenshot(r.Context(), sessionKey, targetID)
	if refresh {
		newSnap, err := h.manager.CaptureScreenshot(r.Context(), sessionKey, true, targetID)
		if err == nil {
			snap = newSnap
		} else if snap == nil {
			return &httpError{status: http.StatusConflict, message: err.Error()}
		}
	}

	if snap == nil {
		return &httpError{status: http.StatusNotFound, message: "该会话还没有浏览器画面"}
	}

	writeJSON(w, http.StatusOK, snap)
	return nil
}

func (h *Handler) handleNavigate(w http.ResponseWriter, r *http.Request) error {
	var payload struct {
		AgentID   string `json:"agent_id"`
		SessionID string `json:"session_id"`
		URL       string `json:"url"`
	}
	dec := json.NewDecoder(r.Body)
	dec.UseNumber()
	if err := dec.Decode(&payload); err != nil || payload.AgentID == "" || payload.SessionID == "" || payload.URL == "" {
		return &httpError{status: http.StatusUnprocessableEntity, message: "Invalid payload"}
	}

	if err := h.verifySession(r.Context(), payload.AgentID, payload.SessionID); err != nil {
		return err
	}

	sessionKey := payload.AgentID + "::" + payload.SessionID
	snap, err := h.manager.Navigate(r.Context(), sessionKey, payload.URL, "")
	if err != nil {
		return &httpError{status: http.StatusBadRequest, message: err.Error()}
	}

	h.publishMirrorUpdated(r.Context(), payload.AgentID, payload.SessionID, snap)
	writeJSON(w, http.StatusOK, snap)
	return nil
}

func (h *Handler) handleInteract(w http.ResponseWriter, r *http.Request) error {
	var payload struct {
		AgentID   string   `json:"agent_id"`
		SessionID string   `json:"session_id"`
		Action    string   `json:"action"`
		TargetID  *string  `json:"target_id"`
		X         *float64 `json:"x"`
		Y         *float64 `json:"y"`
		RatioX    *float64 `json:"ratio_x"`
		RatioY    *float64 `json:"ratio_y"`
		DeltaY    *float64 `json:"delta_y"`
		Text      *string  `json:"text"`
	}
	dec := json.NewDecoder(r.Body)
	dec.UseNumber()
	if err := dec.Decode(&payload); err != nil || payload.AgentID == "" || payload.SessionID == "" || payload.Action == "" {
		return &httpError{status: http.StatusUnprocessableEntity, message: "Invalid payload"}
	}

	switch payload.Action {
	case "click", "wheel", "text", "back", "forward":
	default:
		return &httpError{status: http.StatusUnprocessableEntity, message: "Invalid action pattern"}
	}

	if err := h.verifySession(r.Context(), payload.AgentID, payload.SessionID); err != nil {
		return err
	}

	sessionKey := payload.AgentID + "::" + payload.SessionID
	tid := ""
	if payload.TargetID != nil {
		tid = *payload.TargetID
	}

	snap, err := h.manager.Interact(r.Context(), sessionKey, payload.Action, tid, payload.X, payload.Y, payload.RatioX, payload.RatioY, payload.DeltaY, payload.Text)
	if err != nil {
		return &httpError{status: http.StatusBadRequest, message: err.Error()}
	}

	h.publishMirrorUpdated(r.Context(), payload.AgentID, payload.SessionID, snap)
	writeJSON(w, http.StatusOK, snap)
	return nil
}

func (h *Handler) handlePageContent(w http.ResponseWriter, r *http.Request) error {
	q := r.URL.Query()
	agentID := q.Get("agent_id")
	sessionID := q.Get("session_id")
	if agentID == "" || sessionID == "" {
		return &httpError{status: http.StatusUnprocessableEntity, message: "agent_id and session_id are required"}
	}
	if err := h.verifySession(r.Context(), agentID, sessionID); err != nil {
		return err
	}

	sessionKey := agentID + "::" + sessionID
	targetID := q.Get("target_id")

	content, err := h.manager.ExtractPageContent(r.Context(), sessionKey, targetID)
	if err != nil {
		return &httpError{status: http.StatusConflict, message: err.Error()}
	}

	writeJSON(w, http.StatusOK, content)
	return nil
}

func (h *Handler) handleDiagnostics(w http.ResponseWriter, r *http.Request) error {
	q := r.URL.Query()
	agentID := q.Get("agent_id")
	sessionID := q.Get("session_id")
	if agentID == "" || sessionID == "" {
		return &httpError{status: http.StatusUnprocessableEntity, message: "agent_id and session_id are required"}
	}
	if err := h.verifySession(r.Context(), agentID, sessionID); err != nil {
		return err
	}

	sessionKey := agentID + "::" + sessionID
	targetID := q.Get("target_id")

	diag, err := h.manager.Diagnostics(r.Context(), sessionKey, targetID)
	if err != nil {
		return &httpError{status: http.StatusInternalServerError, message: err.Error()}
	}

	writeJSON(w, http.StatusOK, diag)
	return nil
}

func (h *Handler) handleTabSelect(w http.ResponseWriter, r *http.Request) error {
	var payload struct {
		AgentID   string `json:"agent_id"`
		SessionID string `json:"session_id"`
		TargetID  string `json:"target_id"`
	}
	dec := json.NewDecoder(r.Body)
	dec.UseNumber()
	if err := dec.Decode(&payload); err != nil || payload.AgentID == "" || payload.SessionID == "" {
		return &httpError{status: http.StatusUnprocessableEntity, message: "Invalid payload"}
	}
	if payload.TargetID == "" {
		return &httpError{status: http.StatusBadRequest, message: "target_id is required"}
	}

	if err := h.verifySession(r.Context(), payload.AgentID, payload.SessionID); err != nil {
		return err
	}

	sessionKey := payload.AgentID + "::" + payload.SessionID
	snap, err := h.manager.SelectTab(r.Context(), sessionKey, payload.TargetID)
	if err != nil {
		return &httpError{status: http.StatusBadRequest, message: err.Error()}
	}

	h.publishMirrorUpdated(r.Context(), payload.AgentID, payload.SessionID, snap)
	writeJSON(w, http.StatusOK, snap)
	return nil
}

func (h *Handler) handleTabNew(w http.ResponseWriter, r *http.Request) error {
	var payload struct {
		AgentID   string  `json:"agent_id"`
		SessionID string  `json:"session_id"`
		URL       *string `json:"url"`
	}
	dec := json.NewDecoder(r.Body)
	dec.UseNumber()
	if err := dec.Decode(&payload); err != nil || payload.AgentID == "" || payload.SessionID == "" {
		return &httpError{status: http.StatusUnprocessableEntity, message: "Invalid payload"}
	}

	if err := h.verifySession(r.Context(), payload.AgentID, payload.SessionID); err != nil {
		return err
	}

	sessionKey := payload.AgentID + "::" + payload.SessionID
	targetURL := "about:blank"
	if payload.URL != nil && *payload.URL != "" {
		targetURL = *payload.URL
	}

	snap, err := h.manager.NewTab(r.Context(), sessionKey, targetURL)
	if err != nil {
		return &httpError{status: http.StatusBadRequest, message: err.Error()}
	}

	h.publishMirrorUpdated(r.Context(), payload.AgentID, payload.SessionID, snap)
	writeJSON(w, http.StatusOK, snap)
	return nil
}

func (h *Handler) handleTabClose(w http.ResponseWriter, r *http.Request) error {
	var payload struct {
		AgentID   string `json:"agent_id"`
		SessionID string `json:"session_id"`
		TargetID  string `json:"target_id"`
	}
	dec := json.NewDecoder(r.Body)
	dec.UseNumber()
	if err := dec.Decode(&payload); err != nil || payload.AgentID == "" || payload.SessionID == "" {
		return &httpError{status: http.StatusUnprocessableEntity, message: "Invalid payload"}
	}
	if payload.TargetID == "" {
		return &httpError{status: http.StatusBadRequest, message: "target_id is required"}
	}

	if err := h.verifySession(r.Context(), payload.AgentID, payload.SessionID); err != nil {
		return err
	}

	sessionKey := payload.AgentID + "::" + payload.SessionID
	snap, err := h.manager.CloseTab(r.Context(), sessionKey, payload.TargetID)
	if err != nil {
		return &httpError{status: http.StatusBadRequest, message: err.Error()}
	}

	h.publishMirrorUpdated(r.Context(), payload.AgentID, payload.SessionID, snap)
	writeJSON(w, http.StatusOK, snap)
	return nil
}

func (h *Handler) handleProxy(w http.ResponseWriter, r *http.Request) error {
	targetURL := ""
	if r.Method == http.MethodPost {
		var payload map[string]any
		dec := json.NewDecoder(r.Body)
		dec.UseNumber()
		if dec.Decode(&payload) == nil {
			if u, ok := payload["url"].(string); ok {
				targetURL = strings.TrimSpace(u)
			}
		}
	}
	if targetURL == "" {
		targetURL = strings.TrimSpace(r.URL.Query().Get("url"))
	}
	if targetURL == "" {
		return &httpError{status: http.StatusBadRequest, message: "URL is required"}
	}

	ProxyBrowser(w, r, targetURL)
	return nil
}

func (h *Handler) handleOpenBrowser(w http.ResponseWriter, r *http.Request) error {
	var payload struct {
		URL string `json:"url"`
	}
	dec := json.NewDecoder(r.Body)
	dec.UseNumber()
	if err := dec.Decode(&payload); err != nil {
		return &httpError{status: http.StatusBadRequest, message: "Invalid JSON"}
	}
	urlStr := strings.TrimSpace(payload.URL)
	if urlStr == "" {
		return &httpError{status: http.StatusBadRequest, message: "URL is required"}
	}

	if err := OpenSystemURL(urlStr); err != nil {
		return &httpError{status: http.StatusInternalServerError, message: fmt.Sprintf("Failed to open browser: %v", err)}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"status": "ok",
		"opened": urlStr,
	})
	return nil
}

// Invoke handles agentapi/mcp invocation for browser capabilities.
func (h *Handler) Invoke(ctx context.Context, capability string, input map[string]any) (any, error) {
	switch capability {
	case "browser.run":
		u, _ := input["url"].(string)
		goal, _ := input["goal"].(string)
		if u == "" || goal == "" {
			return nil, errors.New("url and goal are required")
		}

		inputsMap := make(map[string]string)
		if rawInputs, ok := input["inputs"].(map[string]any); ok {
			for k, v := range rawInputs {
				inputsMap[k] = fmt.Sprintf("%v", v)
			}
		}

		maxSteps := 30
		if ms, ok := input["max_steps"].(json.Number); ok {
			if n, err := ms.Int64(); err == nil {
				maxSteps = int(n)
			}
		} else if ms, ok := input["max_steps"].(float64); ok {
			maxSteps = int(ms)
		}

		reset, _ := input["reset"].(bool)
		includeScreenshot, _ := input["include_screenshot"].(bool)

		sessionKey, err := h.resolveSessionKey(ctx, input)
		if err != nil {
			return nil, err
		}

		var planner *JevPlanner
		if h.options.PlannerInvoker != nil {
			planner = &JevPlanner{
				Invoker: h.options.PlannerInvoker,
			}
		}

		res, err := h.manager.RunBrowserTask(ctx, BrowserTaskRequest{
			URL:               u,
			Goal:              goal,
			Inputs:            inputsMap,
			MaxSteps:          maxSteps,
			Reset:             reset,
			IncludeScreenshot: includeScreenshot,
			SessionKey:        sessionKey,
		}, h.options.Store, planner)
		if err != nil {
			return nil, err
		}

		if res.ScreenshotRevision != 0 && h.options.Publisher != nil {
			parts := strings.SplitN(sessionKey, "::", 2)
			aid, sid := "", ""
			if len(parts) == 2 {
				aid, sid = parts[0], parts[1]
			}
			_ = h.options.Publisher.Publish(ctx, "browser.mirror.updated", map[string]any{
				"agent_id":   aid,
				"session_id": sid,
				"data": map[string]any{
					"revision":    res.ScreenshotRevision,
					"url":         res.URL,
					"title":       res.Title,
					"session_key": sessionKey,
				},
			})
		}
		return res, nil

	case "browser.screenshot":
		sessionKey, err := h.resolveSessionKey(ctx, input)
		if err != nil {
			return nil, err
		}
		snap, err := h.manager.CaptureScreenshot(ctx, sessionKey, true, "")
		if err != nil {
			return nil, err
		}
		return snap, nil

	case "browser.cdp":
		method, _ := input["method"].(string)
		if method == "" {
			return nil, errors.New("method is required")
		}
		params, _ := input["params"].(map[string]any)
		sessionKey, err := h.resolveSessionKey(ctx, input)
		if err != nil {
			return nil, err
		}
		return h.manager.CallCDP(ctx, sessionKey, method, params, "")

	default:
		return nil, fmt.Errorf("unknown browser capability: %s", capability)
	}
}

func (h *Handler) resolveSessionKey(ctx context.Context, input map[string]any) (string, error) {
	if sKey, ok := input["session_key"].(string); ok && strings.TrimSpace(sKey) != "" {
		return strings.TrimSpace(sKey), nil
	}
	agentID, _ := input["agent_id"].(string)
	sessionID, _ := input["session_id"].(string)
	if sessionID == "" {
		sessionID, _ = input["caller_session_id"].(string)
	}
	if agentID != "" && sessionID != "" {
		if h.options.Store != nil {
			sess, _ := h.options.Store.GetSession(ctx, agentID, sessionID)
			if sess != nil {
				return agentID + "::" + sessionID, nil
			}
		} else {
			return agentID + "::" + sessionID, nil
		}
	}
	if agentID != "" && h.options.Store != nil {
		sessions, _ := h.options.Store.ListSessions(ctx, agentID)
		var active []map[string]any
		for _, s := range sessions {
			status, _ := s["status"].(string)
			if status == "running" || status == "waiting_approval" {
				active = append(active, s)
			}
		}
		if len(active) == 1 {
			id, _ := active[0]["id"].(string)
			return agentID + "::" + id, nil
		}
	}
	return "", errors.New("browser capability requires session_key because the calling session is ambiguous")
}

type httpError struct {
	status  int
	message string
}

func (e *httpError) Error() string { return e.message }

func writeJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}

func writeError(w http.ResponseWriter, status int, detail string) {
	writeJSON(w, status, map[string]any{"detail": detail})
}

func writeTargetError(w http.ResponseWriter, err error) {
	var httpErr *httpError
	if errors.As(err, &httpErr) {
		writeError(w, httpErr.status, httpErr.message)
		return
	}
	writeError(w, http.StatusInternalServerError, err.Error())
}
