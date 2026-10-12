// Package settingsapi serves the small settings endpoints the Settings dialog
// reads on open: network exposure, the helper LLM, the Jev key, the managed
// runtime list and the Codex Desktop control status. They are plain
// workspace-preference reads and writes; nothing here starts a process.
package settingsapi

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"astrorder.dev/app-server/internal/auth"
	"astrorder.dev/app-server/internal/store"
)

const (
	prefNetworkOrigins = "network:allowed_origins"
	prefNetworkPublic  = "network:public_url"
	prefLLM            = "services:llm"
	prefJevKey         = "services:jev_api_key"
	maxBody            = 64 * 1024
)

// BrowserAuthorizer preserves the shared browser token and Origin policy.
type BrowserAuthorizer interface {
	AuthorizeBrowser(*http.Request) *auth.Error
}

// Options wires the handler. Port, Origins and PublicURL are the process
// configuration used when no preference has been saved yet.
type Options struct {
	Store         *store.Store
	Authorizer    BrowserAuthorizer
	Port          int
	Origins       []string
	PublicURL     string
	BrowserSecret string
	HTTPClient    *http.Client
	// CodexDesktop reports Codex Desktop control availability.
	RestartDaemon func(context.Context, bool) error
	CodexDesktop  func(context.Context) (map[string]any, error)
}

type handler struct {
	Options
	operations restartOperations
}

var paths = map[string][]string{
	"/api/v1/network/config":       {http.MethodGet, http.MethodPost},
	"/api/v1/services/llm/config":  {http.MethodGet, http.MethodPost},
	"/api/v1/services/llm/test":    {http.MethodPost},
	"/api/v1/services/jev/test":    {http.MethodPost},
	"/api/v1/tools/jev-filter":     {http.MethodPost},
	"/api/v1/services/jev/config":  {http.MethodGet, http.MethodPost},
	"/api/v1/runtime/launch":       {http.MethodPost},
	"/api/v1/runtime":              {http.MethodGet},
	"/api/v1/codex-desktop/status": {http.MethodGet},
}

// Matches reports whether this package owns the route.
func Matches(method, path string) bool {
	if path == "/api/v1/services/daemon/restart" {
		return method == http.MethodPost
	}
	if strings.HasPrefix(path, "/api/v1/services/daemon/restart/") {
		return method == http.MethodGet && !strings.Contains(strings.TrimPrefix(path, "/api/v1/services/daemon/restart/"), "/")
	}
	for _, allowed := range paths[strings.TrimRight(path, "/")] {
		if allowed == method {
			return true
		}
	}
	return false
}

// NewHandler returns the settings endpoints.
func NewHandler(options Options) http.Handler {
	if options.HTTPClient == nil {
		options.HTTPClient = &http.Client{Timeout: 20 * 1e9}
	}
	return &handler{Options: options}
}

func (h *handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if h.Authorizer != nil {
		if failure := h.Authorizer.AuthorizeBrowser(r); failure != nil {
			writeJSON(w, failure.Status, map[string]string{"detail": failure.Detail})
			return
		}
	}
	if strings.HasPrefix(r.URL.Path, "/api/v1/services/daemon/restart") {
		h.restart(w, r)
		return
	}
	switch strings.TrimRight(r.URL.Path, "/") {
	case "/api/v1/network/config":
		if r.Method == http.MethodGet {
			h.getNetwork(w, r)
		} else {
			h.setNetwork(w, r)
		}
	case "/api/v1/services/llm/config":
		if r.Method == http.MethodGet {
			h.getLLM(w, r)
		} else {
			h.setLLM(w, r)
		}
	case "/api/v1/services/llm/test":
		h.testLLM(w, r)
	case "/api/v1/services/jev/config":
		if r.Method == http.MethodGet {
			h.getJev(w, r)
		} else {
			h.setJev(w, r)
		}
	case "/api/v1/services/jev/test", "/api/v1/tools/jev-filter":
		h.jev(w, r)
	case "/api/v1/runtime/launch":
		var payload struct {
			Kind      string `json:"kind"`
			Workspace string `json:"workspace"`
		}
		if decode(r, &payload) != nil || payload.Kind == "" || payload.Workspace == "" {
			fail(w, 422, "kind and workspace are required")
			return
		}
		fail(w, 409, "请通过 Agent 连接和创建会话启动运行时；不支持脱离小内核的进程启动")
	case "/api/v1/runtime":
		h.runtime(w)
	case "/api/v1/codex-desktop/status":
		h.codexDesktop(w, r)
	}
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func fail(w http.ResponseWriter, status int, detail string) {
	writeJSON(w, status, map[string]string{"detail": detail})
}

func decode(r *http.Request, into any) error {
	decoder := json.NewDecoder(io.LimitReader(r.Body, maxBody))
	return decoder.Decode(into)
}

func (h *handler) stringPref(ctx context.Context, key string) (string, bool) {
	pref, err := h.Store.GetPreference(ctx, key)
	if err != nil || !pref.Exists {
		return "", false
	}
	text, ok := pref.Value.(string)
	return strings.TrimSpace(text), ok
}

// ---- network ----------------------------------------------------------

func localIP() string {
	connection, err := net.Dial("udp", "8.8.8.8:80")
	if err != nil {
		return "127.0.0.1"
	}
	defer connection.Close()
	if address, ok := connection.LocalAddr().(*net.UDPAddr); ok {
		return address.IP.String()
	}
	return "127.0.0.1"
}

func (h *handler) networkView(ctx context.Context) map[string]any {
	origins := append([]string{}, h.Origins...)
	if pref, err := h.Store.GetPreference(ctx, prefNetworkOrigins); err == nil && pref.Exists {
		if list, ok := pref.Value.([]any); ok {
			origins = origins[:0]
			for _, item := range list {
				if text, ok := item.(string); ok && strings.TrimSpace(text) != "" {
					origins = append(origins, strings.TrimSpace(text))
				}
			}
		}
	}
	public, _ := h.stringPref(ctx, prefNetworkPublic)
	if public == "" {
		public = h.PublicURL
	}
	if public != "" {
		present := false
		for _, origin := range origins {
			present = present || origin == public
		}
		if !present {
			origins = append(origins, public)
		}
	}
	return map[string]any{
		"public_url": public, "allowed_origins": origins,
		"local_ip": localIP(), "port": h.Port, "token": h.BrowserSecret,
	}
}

func (h *handler) getNetwork(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, h.networkView(r.Context()))
}

func (h *handler) setNetwork(w http.ResponseWriter, r *http.Request) {
	var patch struct {
		PublicURL      *string   `json:"public_url"`
		AllowedOrigins *[]string `json:"allowed_origins"`
	}
	if err := decode(r, &patch); err != nil {
		fail(w, http.StatusUnprocessableEntity, "Invalid request")
		return
	}
	if patch.PublicURL != nil {
		if err := h.Store.SetPreference(r.Context(), prefNetworkPublic, strings.TrimSpace(*patch.PublicURL), true); err != nil {
			fail(w, http.StatusInternalServerError, "Preference storage is unavailable")
			return
		}
	}
	if patch.AllowedOrigins != nil {
		seen := map[string]bool{}
		cleaned := []string{}
		for _, origin := range *patch.AllowedOrigins {
			origin = strings.TrimRight(strings.TrimSpace(origin), "/")
			if origin != "" && !seen[origin] {
				seen[origin] = true
				cleaned = append(cleaned, origin)
			}
		}
		if err := h.Store.SetPreference(r.Context(), prefNetworkOrigins, cleaned, true); err != nil {
			fail(w, http.StatusInternalServerError, "Preference storage is unavailable")
			return
		}
	}
	writeJSON(w, http.StatusOK, h.networkView(r.Context()))
}

// ---- helper LLM -------------------------------------------------------

var llmDefaults = map[string]string{
	"base_url": "https://api.deepseek.com/v1", "api_key": "", "model": "deepseek-chat", "reasoning": "low",
}

func (h *handler) llmConfig(ctx context.Context) map[string]string {
	config := map[string]string{}
	for key, value := range llmDefaults {
		config[key] = value
	}
	if pref, err := h.Store.GetPreference(ctx, prefLLM); err == nil && pref.Exists {
		if saved, ok := pref.Value.(map[string]any); ok {
			for key := range llmDefaults {
				if text, ok := saved[key].(string); ok && strings.TrimSpace(text) != "" {
					config[key] = strings.TrimSpace(text)
				}
			}
		}
	}
	return config
}

func llmView(config map[string]string) map[string]any {
	key := config["api_key"]
	masked := ""
	switch {
	case len(key) > 12:
		masked = key[:6] + "..." + key[len(key)-4:]
	case key != "":
		masked = "已配置"
	}
	return map[string]any{
		"configured": key != "", "masked_key": masked,
		"base_url": config["base_url"], "model": config["model"], "reasoning": config["reasoning"],
	}
}

type llmPatch struct {
	BaseURL   string  `json:"base_url"`
	APIKey    *string `json:"api_key"`
	Model     string  `json:"model"`
	Reasoning string  `json:"reasoning"`
}

func validReasoning(value string) bool {
	switch value {
	case "none", "low", "medium", "high":
		return true
	}
	return false
}

func (h *handler) getLLM(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, llmView(h.llmConfig(r.Context())))
}

func (h *handler) setLLM(w http.ResponseWriter, r *http.Request) {
	var patch llmPatch
	if err := decode(r, &patch); err != nil {
		fail(w, http.StatusUnprocessableEntity, "Invalid request")
		return
	}
	config := h.llmConfig(r.Context())
	config["base_url"] = strings.TrimRight(strings.TrimSpace(patch.BaseURL), "/")
	config["model"] = strings.TrimSpace(patch.Model)
	config["reasoning"] = strings.TrimSpace(patch.Reasoning)
	if patch.APIKey != nil {
		config["api_key"] = strings.TrimSpace(*patch.APIKey)
	}
	if parsed, err := url.Parse(config["base_url"]); err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		fail(w, http.StatusUnprocessableEntity, "API URL 必须是完整的 http 或 https 地址")
		return
	}
	if config["model"] == "" {
		fail(w, http.StatusUnprocessableEntity, "模型不能为空")
		return
	}
	if !validReasoning(config["reasoning"]) {
		fail(w, http.StatusUnprocessableEntity, "思考程度无效")
		return
	}
	if err := h.Store.SetPreference(r.Context(), prefLLM, config, true); err != nil {
		fail(w, http.StatusInternalServerError, "Preference storage is unavailable")
		return
	}
	writeJSON(w, http.StatusOK, llmView(config))
}

func reasoningPayload(baseURL, reasoning string) map[string]any {
	if strings.Contains(strings.TrimRight(baseURL, "/")+"/", "api.deepseek.com/") {
		kind := "enabled"
		if reasoning == "none" {
			kind = "disabled"
		}
		return map[string]any{"thinking": map[string]any{"type": kind}}
	}
	if reasoning == "none" {
		return map[string]any{"reasoning": map[string]any{"enabled": false}}
	}
	return map[string]any{"reasoning": map[string]any{"effort": reasoning}}
}

func (h *handler) testLLM(w http.ResponseWriter, r *http.Request) {
	var patch llmPatch
	if err := decode(r, &patch); err != nil {
		fail(w, http.StatusUnprocessableEntity, "Invalid request")
		return
	}
	current := h.llmConfig(r.Context())
	key := current["api_key"]
	if patch.APIKey != nil && strings.TrimSpace(*patch.APIKey) != "" {
		key = strings.TrimSpace(*patch.APIKey)
	}
	if key == "" {
		fail(w, http.StatusBadRequest, "请先填写 API Key")
		return
	}
	base := strings.TrimRight(strings.TrimSpace(patch.BaseURL), "/")
	if parsed, err := url.Parse(base); err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		fail(w, http.StatusUnprocessableEntity, "API URL 必须是完整的 http 或 https 地址")
		return
	}
	body := map[string]any{
		"model": strings.TrimSpace(patch.Model), "max_tokens": 16,
		"messages": []map[string]string{{"role": "user", "content": "Reply with OK only."}},
	}
	for name, value := range reasoningPayload(base, patch.Reasoning) {
		body[name] = value
	}
	encoded, _ := json.Marshal(body)
	request, err := http.NewRequestWithContext(r.Context(), http.MethodPost, base+"/chat/completions", strings.NewReader(string(encoded)))
	if err != nil {
		fail(w, http.StatusBadRequest, "LLM 连接测试失败："+err.Error())
		return
	}
	request.Header.Set("Authorization", "Bearer "+key)
	request.Header.Set("Content-Type", "application/json")
	response, err := h.HTTPClient.Do(request)
	if err != nil {
		fail(w, http.StatusBadRequest, "LLM 连接测试失败："+err.Error())
		return
	}
	defer response.Body.Close()
	var reply struct {
		Model   string `json:"model"`
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	raw, _ := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if response.StatusCode >= 400 || json.Unmarshal(raw, &reply) != nil || len(reply.Choices) == 0 {
		fail(w, http.StatusBadRequest, "LLM 连接测试失败：HTTP "+strconv.Itoa(response.StatusCode))
		return
	}
	model := reply.Model
	if model == "" {
		model = strings.TrimSpace(patch.Model)
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "model": model, "reply": reply.Choices[0].Message.Content})
}

// ---- Jev --------------------------------------------------------------

func (h *handler) jevView(ctx context.Context) map[string]any {
	key, _ := h.stringPref(ctx, prefJevKey)
	masked := ""
	switch {
	case len(key) > 20:
		masked = key[:10] + "..." + key[len(key)-8:]
	case key != "":
		masked = "已配置"
	}
	return map[string]any{"configured": key != "", "masked_key": masked}
}

func (h *handler) getJev(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, h.jevView(r.Context()))
}

func (h *handler) setJev(w http.ResponseWriter, r *http.Request) {
	var patch struct {
		APIKey *string `json:"api_key"`
	}
	if err := decode(r, &patch); err != nil {
		fail(w, http.StatusUnprocessableEntity, "Invalid request")
		return
	}
	key := ""
	if patch.APIKey != nil {
		key = strings.TrimSpace(*patch.APIKey)
	}
	if key == "" {
		if _, err := h.Store.DeletePreference(r.Context(), prefJevKey); err != nil {
			fail(w, http.StatusInternalServerError, "Preference storage is unavailable")
			return
		}
	} else if err := h.Store.SetPreference(r.Context(), prefJevKey, key, true); err != nil {
		fail(w, http.StatusInternalServerError, "Preference storage is unavailable")
		return
	}
	writeJSON(w, http.StatusOK, h.jevView(r.Context()))
}

// ---- runtime ----------------------------------------------------------

// runtime lists managed launch targets. Astrorder attaches to Agents the
// session daemon owns and does not start detached ones, so none is available.
func (h *handler) runtime(w http.ResponseWriter) {
	items := []map[string]any{}
	for _, kind := range []string{"hermes", "codex"} {
		items = append(items, map[string]any{
			"kind": kind, "available": false, "capabilities": []string{},
			"reason": "Agents are attached through the session daemon, not launched here",
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (h *handler) codexDesktop(w http.ResponseWriter, r *http.Request) {
	if h.CodexDesktop != nil {
		if status, err := h.CodexDesktop(r.Context()); err == nil && status != nil {
			writeJSON(w, http.StatusOK, status)
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"available": false, "detail": "Codex Desktop control is not configured"})
}
