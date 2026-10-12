package browser

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
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
	ConsoleErrors      []string         `json:"console_errors"`
	Steps              []map[string]any `json:"steps"`
	ElapsedMS          int64            `json:"elapsed_ms"`
	ScreenshotRevision int64            `json:"screenshot_revision,omitempty"`
	SessionKey         string           `json:"session_key"`
	Screenshot         string           `json:"screenshot,omitempty"`
	MimeType           string           `json:"mime_type,omitempty"`
}

// MatchFieldText matches visible field label to inputs provided by user.
func MatchFieldText(inputs map[string]string, label string) (string, bool) {
	normLabel := normalizeAlnum(label)
	var bestMatch string
	bestLen := -1
	tie := false

	for rawKey, val := range inputs {
		normKey := normalizeAlnum(rawKey)
		if normKey != "" && (strings.Contains(normLabel, normKey) || strings.Contains(normKey, normLabel)) {
			if len(normKey) > bestLen {
				bestLen = len(normKey)
				bestMatch = val
				tie = false
			} else if len(normKey) == bestLen && val != bestMatch {
				tie = true
			}
		}
	}

	if bestLen >= 0 && !tie {
		return bestMatch, true
	}
	return "", false
}

func normalizeAlnum(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r > 127 {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// JevPlanner executes browser tasks via model decisions.
type JevPlanner struct {
	Client  *http.Client
	JevKey  string
	LLM     LLMConfig
	Invoker func(ctx context.Context, state any, questions map[string]any) (map[string]any, error)
}

// InvokeJevHTTP makes the HTTP call to TypeSafe AI Jev endpoint.
func (p *JevPlanner) InvokeJevHTTP(ctx context.Context, state any, questions map[string]any) (map[string]any, error) {
	if p.Invoker != nil {
		return p.Invoker(ctx, state, questions)
	}

	if p.JevKey == "" {
		return nil, errors.New("Jev API Key 未配置，请在设置中配置 Jev API Key。")
	}

	client := p.Client
	if client == nil {
		client = &http.Client{Timeout: 25 * time.Second}
	}

	payload := map[string]any{
		"model":     "jev-latest",
		"state":     state,
		"questions": questions,
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal jev request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, defaultJevURL, bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+p.JevKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
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

// DOMElement represents an observed interactive DOM element on the page.
type DOMElement struct {
	ID    string `json:"id"`
	Tag   string `json:"tag"`
	Label string `json:"label"`
	Role  string `json:"role"`
	Rect  struct {
		X float64 `json:"x"`
		Y float64 `json:"y"`
		W float64 `json:"w"`
		H float64 `json:"h"`
	} `json:"rect"`
}

const observeElementsScript = `(() => {
	if (window.__astrorderObserve) {
		return window.__astrorderObserve();
	}
	const results = [];
	const selector = 'button, a[href], input:not([type="hidden"]), select, textarea, [role="button"], [role="textbox"], [role="link"]';
	const nodes = document.querySelectorAll(selector);
	let idx = 1;
	for (const el of nodes) {
		const r = el.getBoundingClientRect();
		if (r.width > 0 && r.height > 0) {
			const label = (el.innerText || el.getAttribute('aria-label') || el.getAttribute('placeholder') || el.value || el.title || '').trim();
			results.push({
				id: String(idx++),
				tag: el.tagName,
				label: label || ('Element ' + el.tagName),
				role: el.getAttribute('role') || el.tagName.toLowerCase(),
				rect: { x: r.x, y: r.y, w: r.width, h: r.height }
			});
		}
	}
	return results;
})()`

func (m *SessionManager) observeElements(ctx context.Context, sessionKey string, targetID string) ([]DOMElement, error) {
	raw, err := m.CallCDP(ctx, sessionKey, "Runtime.evaluate", map[string]any{
		"expression":    observeElementsScript,
		"returnByValue": true,
	}, targetID)
	if err != nil {
		return nil, fmt.Errorf("observe elements evaluation error: %w", err)
	}

	var val any
	if r, ok := raw["result"]; ok {
		if rm, ok := r.(map[string]any); ok {
			if inner, ok := rm["result"].(map[string]any); ok {
				val = inner["value"]
			} else {
				val = rm["value"]
			}
		}
	}
	if val == nil {
		return nil, errors.New("observe elements returned empty or null value")
	}

	data, err := json.Marshal(val)
	if err != nil {
		return nil, fmt.Errorf("marshal observed elements: %w", err)
	}

	var elements []DOMElement
	if err := json.Unmarshal(data, &elements); err != nil {
		return nil, fmt.Errorf("malformed observed DOM elements: %w", err)
	}
	return elements, nil
}

// RunBrowserTask runs a bounded browser task using CDP and model decisions.
func (m *SessionManager) RunBrowserTask(ctx context.Context, req BrowserTaskRequest, st *store.Store, planner *JevPlanner) (*BrowserTaskResult, error) {
	parsed, err := url.Parse(req.URL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return nil, errors.New("url 必须是完整的 http 或 https 地址")
	}
	if strings.TrimSpace(req.Goal) == "" {
		return nil, errors.New("goal 不能为空")
	}
	maxSteps := req.MaxSteps
	if maxSteps == 0 {
		maxSteps = 30
	}
	if maxSteps < 1 || maxSteps > 60 {
		return nil, errors.New("max_steps 必须在 1 到 60 之间")
	}

	if req.Reset {
		m.CloseSession(ctx, req.SessionKey)
	}

	if planner == nil {
		jevKey := GetJevKey(ctx, st)
		if jevKey == "" {
			return nil, errors.New("Jev API Key 未配置，请在设置中配置 Jev API Key。")
		}
		llmCfg := GetLLMConfig(ctx, st)
		planner = &JevPlanner{
			JevKey: jevKey,
			LLM:    llmCfg,
		}
	} else if planner.Invoker == nil && planner.JevKey == "" {
		planner.JevKey = GetJevKey(ctx, st)
		if planner.JevKey == "" {
			return nil, errors.New("Jev API Key 未配置，请在设置中配置 Jev API Key。")
		}
	}

	start := time.Now()

	// Initial navigation or tab recovery
	snap, err := m.Navigate(ctx, req.SessionKey, req.URL, "")
	if err != nil {
		return nil, err
	}

	var history []map[string]any
	history = append(history, map[string]any{
		"step":   1,
		"action": "navigate",
		"url":    req.URL,
	})

	status := "step_limit"

	// Planner execution loop
	for step := 2; step <= maxSteps+1; step++ {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		// Observe page content and elements
		content, err := m.ExtractPageContent(ctx, req.SessionKey, snap.TargetID)
		if err != nil {
			return nil, fmt.Errorf("extract page content error: %w", err)
		}

		text := content.Text
		title := content.Title
		pageURL := content.URL

		elements, err := m.observeElements(ctx, req.SessionKey, snap.TargetID)
		if err != nil {
			return nil, fmt.Errorf("observe elements error: %w", err)
		}
		elementMap := make(map[string]DOMElement)
		criteriaElements := make(map[string]any)
		for _, el := range elements {
			elementMap[el.ID] = el
			criteriaElements[el.ID] = fmt.Sprintf("[%s] %s (%s)", el.ID, el.Label, el.Role)
		}

		// Ask planner for next action
		questions := map[string]any{
			"operation": map[string]any{
				"type": "choice",
				"criteria": map[string]any{
					"CLICK":     "Click an interactive element or button on the page.",
					"TYPE_TEXT": "Enter or replace text in an editable input field.",
					"DONE":      "Every requirement of the goal is visibly satisfied.",
					"BLOCKED":   "No available action can make progress towards the goal.",
				},
				"instructions": map[string]any{
					"goal":  req.Goal,
					"rules": "Advance the goal from current page. DONE requires visible evidence.",
				},
			},
			"goal_satisfied": map[string]any{
				"type": "choice",
				"criteria": map[string]any{
					"SATISFIED":     "Visible evidence proves every requirement of the goal is completely satisfied.",
					"NOT_SATISFIED": "Goal is incomplete, still in progress, or lacks positive visible evidence.",
				},
				"instructions": "Require positive visible outcome evidence. Merely finding an element is NOT completion.",
			},
		}

		if len(criteriaElements) > 0 {
			questions["click_target"] = map[string]any{
				"type":     "choice",
				"criteria": criteriaElements,
			}
			questions["type_text_target"] = map[string]any{
				"type":     "choice",
				"criteria": criteriaElements,
			}
		}

		state := map[string]any{
			"page": map[string]any{
				"url":   pageURL,
				"title": title,
				"text":  text,
			},
			"elements":       criteriaElements,
			"recent_actions": history,
		}

		decision, err := planner.InvokeJevHTTP(ctx, state, questions)
		if err != nil {
			return nil, fmt.Errorf("planner error: %w", err)
		}

		answers, _ := decision["answers"].(map[string]any)
		opAns, _ := answers["operation"].(map[string]any)
		operation, _ := opAns["choice"].(string)
		if operation == "" {
			operation = "BLOCKED"
		}

		if operation == "DONE" {
			satAns, ok := answers["goal_satisfied"].(map[string]any)
			satChoice, _ := satAns["choice"].(string)
			if !ok || satChoice != "SATISFIED" {
				history = append(history, map[string]any{
					"step":      step,
					"operation": "DONE_REJECTED",
					"action":    "缺少完成证据",
				})
				continue
			}
			status = "done"
			history = append(history, map[string]any{
				"step":      step,
				"operation": "DONE",
				"action":    "Goal satisfied",
			})
			break
		}

		if operation == "BLOCKED" {
			status = "blocked"
			history = append(history, map[string]any{
				"step":      step,
				"operation": "BLOCKED",
				"action":    "No further progress possible",
			})
			break
		}

		if operation == "CLICK" {
			targetChoice, _ := answers["click_target"].(map[string]any)
			chosenID, _ := targetChoice["choice"].(string)
			if chosenID == "" {
				return nil, errors.New("click operation requires a target element choice")
			}
			el, found := elementMap[chosenID]
			if !found {
				return nil, fmt.Errorf("chosen click target %s is not in observed elements", chosenID)
			}

			cx := el.Rect.X + el.Rect.W/2.0
			cy := el.Rect.Y + el.Rect.H/2.0
			interactSnap, err := m.Interact(ctx, req.SessionKey, "click", snap.TargetID, &cx, &cy, nil, nil, nil, nil)
			if err != nil {
				return nil, fmt.Errorf("click error: %w", err)
			}
			snap = interactSnap
			history = append(history, map[string]any{
				"step":      step,
				"operation": "CLICK",
				"action":    "Click target",
				"target_id": chosenID,
			})
		} else if operation == "TYPE_TEXT" {
			targetChoice, _ := answers["type_text_target"].(map[string]any)
			chosenID, _ := targetChoice["choice"].(string)
			if chosenID == "" {
				return nil, errors.New("type operation requires a target element choice")
			}
			el, found := elementMap[chosenID]
			if !found {
				return nil, fmt.Errorf("chosen type target %s is not in observed elements", chosenID)
			}

			textToType, matched := MatchFieldText(req.Inputs, el.Label)
			if !matched {
				return nil, fmt.Errorf("no matching input value found for field '%s'", el.Label)
			}

			cx := el.Rect.X + el.Rect.W/2.0
			cy := el.Rect.Y + el.Rect.H/2.0
			// Focus the field first by clicking
			if _, err := m.Interact(ctx, req.SessionKey, "click", snap.TargetID, &cx, &cy, nil, nil, nil, nil); err != nil {
				return nil, fmt.Errorf("focus click error: %w", err)
			}

			interactSnap, err := m.Interact(ctx, req.SessionKey, "text", snap.TargetID, nil, nil, nil, nil, nil, &textToType)
			if err != nil {
				return nil, fmt.Errorf("type error: %w", err)
			}
			snap = interactSnap
			history = append(history, map[string]any{
				"step":      step,
				"operation": "TYPE_TEXT",
				"action":    "Type input text",
				"target_id": chosenID,
				"text":      textToType,
			})
		} else {
			return nil, fmt.Errorf("unknown planner action: %s", operation)
		}
	}

	// Step limit reached if loop finishes without break
	// (status remains step_limit)

	// Collect final page content and errors
	content, err := m.ExtractPageContent(ctx, req.SessionKey, snap.TargetID)
	if err != nil {
		return nil, fmt.Errorf("final extract page content error: %w", err)
	}
	diag, err := m.Diagnostics(ctx, req.SessionKey, snap.TargetID)
	if err != nil {
		return nil, fmt.Errorf("final diagnostics error: %w", err)
	}

	var errorsList []string
	if diag != nil {
		for _, l := range diag.Logs {
			if l.Type == "error" {
				errorsList = append(errorsList, l.Text)
			}
		}
	}

	visText := ""
	if content != nil {
		visText = content.Text
	}

	elapsed := time.Since(start).Milliseconds()

	result := &BrowserTaskResult{
		OK:                 status == "done",
		Status:             status,
		BrowserSession:     snap.TargetID,
		Resumed:            false,
		URL:                snap.URL,
		Title:              snap.Title,
		VisibleText:        visText,
		ConsoleErrors:      errorsList,
		Steps:              history,
		ElapsedMS:          elapsed,
		ScreenshotRevision: snap.Revision,
		SessionKey:         req.SessionKey,
	}

	if req.IncludeScreenshot && snap.Screenshot != "" {
		result.Screenshot = snap.Screenshot
		result.MimeType = snap.MimeType
	}

	return result, nil
}
