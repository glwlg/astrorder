package browser

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"astrorder.dev/app-server/internal/store"
)

const (
	defaultJevURL = "https://api.typesafe.ai/v1/systemone"
)

// LLMConfig holds model gateway configuration from store.
type LLMConfig struct {
	BaseURL   string `json:"base_url"`
	APIKey    string `json:"api_key"`
	Model     string `json:"model"`
	Reasoning string `json:"reasoning"`
}

// GetJevKey retrieves configured Jev API key from store or env.
func GetJevKey(ctx context.Context, st *store.Store) string {
	if st != nil {
		pref, err := st.GetPreference(ctx, "services:jev_api_key")
		if err == nil && pref.Exists {
			if s, ok := pref.Value.(string); ok && strings.TrimSpace(s) != "" {
				return strings.TrimSpace(s)
			}
		}
	}
	if env := os.Getenv("JEV_API_KEY"); strings.TrimSpace(env) != "" {
		return strings.TrimSpace(env)
	}
	if env := os.Getenv("TYPESAFE_API_KEY"); strings.TrimSpace(env) != "" {
		return strings.TrimSpace(env)
	}
	return ""
}

// GetLLMConfig retrieves LLM configuration from store.
func GetLLMConfig(ctx context.Context, st *store.Store) LLMConfig {
	cfg := LLMConfig{
		BaseURL:   "https://api.deepseek.com/v1",
		APIKey:    "",
		Model:     "deepseek-chat",
		Reasoning: "low",
	}
	if st == nil {
		return cfg
	}
	pref, err := st.GetPreference(ctx, "services:llm")
	if err == nil && pref.Exists {
		if m, ok := pref.Value.(map[string]any); ok {
			if v, ok := m["base_url"].(string); ok && strings.TrimSpace(v) != "" {
				cfg.BaseURL = strings.TrimSpace(v)
			}
			if v, ok := m["api_key"].(string); ok {
				cfg.APIKey = strings.TrimSpace(v)
			}
			if v, ok := m["model"].(string); ok && strings.TrimSpace(v) != "" {
				cfg.Model = strings.TrimSpace(v)
			}
			if v, ok := m["reasoning"].(string); ok && strings.TrimSpace(v) != "" {
				cfg.Reasoning = strings.TrimSpace(v)
			}
		}
	}
	return cfg
}

// BrowserTaskRequest contains options for running a browser task.
type BrowserTaskRequest struct {
	URL               string            `json:"url"`
	Goal              string            `json:"goal"`
	Inputs            map[string]string `json:"inputs,omitempty"`
	MaxSteps          int               `json:"max_steps,omitempty"`
	Reset             bool              `json:"reset,omitempty"`
	IncludeScreenshot bool              `json:"include_screenshot,omitempty"`
	SessionKey        string            `json:"session_key"`
}

// BrowserTaskResult contains result of a browser task execution.
type BrowserTaskResult struct {
	OK                 bool             `json:"ok"`
	Status             string           `json:"status"`
	BrowserSession     string           `json:"browser_session,omitempty"`
	Resumed            bool             `json:"resumed"`
	URL                string           `json:"url"`
	Title              string           `json:"title"`
	VisibleText        string           `json:"visible_text"`
	Answer             string           `json:"answer,omitempty"`
	ConsoleErrors      []string         `json:"console_errors"`
	Steps              []map[string]any `json:"steps"`
	ElapsedMS          int64            `json:"elapsed_ms"`
	ScreenshotRevision int64            `json:"screenshot_revision,omitempty"`
	SessionKey         string           `json:"session_key"`
	Screenshot         string           `json:"screenshot,omitempty"`
	MimeType           string           `json:"mime_type,omitempty"`
}

// JevPlanner invokes the System One endpoint for service operations.
type JevPlanner struct {
	Client   *http.Client
	JevKey   string
	JevURL   string
	JevModel string
}

// InvokeJevHTTP makes the HTTP call to TypeSafe AI Jev endpoint.
func (p *JevPlanner) InvokeJevHTTP(ctx context.Context, state any, questions map[string]any) (map[string]any, error) {
	if p.JevKey == "" {
		return nil, errors.New("Jev API Key 未配置，请在设置中配置 Jev API Key。")
	}

	client := p.Client
	if client == nil {
		client = &http.Client{Timeout: 25 * time.Second}
	}
	// A gateway redirect must never forward its credential to another endpoint.
	requestClient := *client
	requestClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	endpoint, model := p.JevURL, p.JevModel
	if endpoint == "" {
		endpoint = defaultJevURL
	}
	if model == "" {
		model = "jev-latest"
	}

	payload := map[string]any{
		"model":     model,
		"state":     state,
		"questions": questions,
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal jev request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+p.JevKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := requestClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("Jev API 网络异常: %w", err)
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(io.LimitReader(resp.Body, 16000001))
	if err != nil {
		return nil, fmt.Errorf("read jev response: %w", err)
	}
	if len(bodyBytes) > 16000000 {
		return nil, errors.New("Jev response exceeds size limit")
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Jev API 调用失败 (%d): %s", resp.StatusCode, string(bodyBytes))
	}

	var res map[string]any
	dec := json.NewDecoder(bytes.NewReader(bodyBytes))
	dec.UseNumber()
	if err := dec.Decode(&res); err != nil {
		return nil, fmt.Errorf("decode jev response: %w", err)
	}
	if err := dec.Decode(new(any)); err != io.EOF {
		return nil, errors.New("decode jev response: unexpected trailing json data")
	}
	return res, nil
}
