package browser

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"time"

	"astrorder.dev/app-server/internal/store"
)

//go:embed engine_worker.py
var engineWorker string

const (
	browserUseDependency = "browser-use==0.13.14"
	jevDependency        = "jev-ultrafast @ https://github.com/browser-use/jev-ultrafast/archive/1231850a0bf1a0c0341fe408ef1668dbbfdfac46.zip"
)

type engineRequest struct {
	Engine   string            `json:"engine"`
	URL      string            `json:"url"`
	Goal     string            `json:"goal"`
	Inputs   map[string]string `json:"inputs"`
	MaxSteps int               `json:"max_steps"`
	CDPURL   string            `json:"cdp_url"`
	PageWS   string            `json:"page_ws"`
	TargetID string            `json:"target_id"`
	LLM      LLMConfig         `json:"llm"`
	Jev      JevEndpoint       `json:"jev"`
}

type engineResult struct {
	OK       bool             `json:"ok"`
	Status   string           `json:"status"`
	TargetID string           `json:"target_id"`
	Steps    []map[string]any `json:"steps"`
	Answer   string           `json:"answer"`
	Error    string           `json:"error"`
}

type limitedOutput struct {
	bytes.Buffer
	limit int
}

func (b *limitedOutput) Write(p []byte) (int, error) {
	n := len(p)
	if remaining := b.limit - b.Len(); remaining > 0 {
		if len(p) > remaining {
			p = p[:remaining]
		}
		_, _ = b.Buffer.Write(p)
	}
	return n, nil
}

func runEngine(ctx context.Context, payload engineRequest) (engineResult, error) {
	uv, err := ensureUV(ctx)
	if err != nil {
		return engineResult{}, err
	}
	dependency := browserUseDependency
	args := []string{"run", "--no-project", "--python", "3.12", "--no-progress"}
	if payload.Engine == "jev_ultrafast" {
		dependency = jevDependency
		args = append(args, "--with", "websockets>=14,<17")
	}
	args = append(args, "--with", dependency, "python", "-c", engineWorker)
	cmd := exec.CommandContext(ctx, uv, args...)
	hideWindow(cmd)
	cmd.Env = append(os.Environ(), "PYTHONUTF8=1", "PYTHONIOENCODING=utf-8", "ANONYMIZED_TELEMETRY=false")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return engineResult{}, err
	}
	var stdout = limitedOutput{limit: 8 << 20}
	var stderr = limitedOutput{limit: 128 << 10}
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Start(); err != nil {
		return engineResult{}, fmt.Errorf("启动浏览器引擎失败: %w", err)
	}
	writeErr := make(chan error, 1)
	go func() { writeErr <- json.NewEncoder(stdin).Encode(payload) }()
	finished := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = stdin.Close()
		case <-finished:
		}
	}()
	waitErr := cmd.Wait()
	close(finished)
	_ = stdin.Close()
	if ctx.Err() != nil {
		return engineResult{}, ctx.Err()
	}
	if err := <-writeErr; err != nil {
		return engineResult{}, fmt.Errorf("发送浏览器任务失败: %w", err)
	}
	var result engineResult
	if err := json.Unmarshal(bytes.TrimSpace(stdout.Bytes()), &result); err != nil {
		detail := strings.TrimSpace(stderr.String())
		for _, secret := range []string{payload.LLM.APIKey, payload.Jev.APIKey} {
			if secret != "" {
				detail = strings.ReplaceAll(detail, secret, "[已隐藏]")
			}
		}
		if len(detail) > 1800 {
			detail = detail[len(detail)-1800:]
		}
		if detail == "" {
			detail = "引擎没有返回有效结果"
		}
		return engineResult{}, fmt.Errorf("浏览器引擎运行失败: %s", detail)
	}
	if result.Error != "" {
		detail := result.Error
		for _, secret := range []string{payload.LLM.APIKey, payload.Jev.APIKey} {
			if secret != "" {
				detail = strings.ReplaceAll(detail, secret, "[已隐藏]")
			}
		}
		return engineResult{}, fmt.Errorf("浏览器引擎运行失败: %s", detail)
	}
	if waitErr != nil {
		return engineResult{}, fmt.Errorf("浏览器引擎退出失败: %w", waitErr)
	}
	if result.Status != "done" && result.Status != "blocked" && result.Status != "step_limit" {
		return engineResult{}, errors.New("浏览器引擎返回了无效的任务状态")
	}
	return result, nil
}

func (m *SessionManager) RunExternalBrowserTask(ctx context.Context, req BrowserTaskRequest, st *store.Store) (*BrowserTaskResult, error) {
	parsed, err := url.Parse(req.URL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return nil, errors.New("url 必须是完整的 http 或 https 地址")
	}
	if strings.TrimSpace(req.Goal) == "" {
		return nil, errors.New("goal 不能为空")
	}
	if req.MaxSteps == 0 {
		req.MaxSteps = 30
	}
	if req.MaxSteps < 1 || req.MaxSteps > 60 {
		return nil, errors.New("max_steps 必须在 1 到 60 之间")
	}
	engine, err := GetBrowserEngine(ctx, st)
	if err != nil {
		return nil, err
	}
	llm := GetLLMConfig(ctx, st)
	if engine == "browser_use" && (llm.APIKey == "" || llm.Model == "" || llm.BaseURL == "") {
		return nil, errors.New("请先配置普通模型服务")
	}
	payload := engineRequest{
		Engine: engine, URL: req.URL, Goal: req.Goal, Inputs: req.Inputs,
		MaxSteps: req.MaxSteps, LLM: llm,
	}
	if payload.Inputs == nil {
		payload.Inputs = map[string]string{}
	}
	if engine == "jev_ultrafast" {
		config, err := GetJevConfig(ctx, st)
		if err != nil {
			return nil, err
		}
		payload.Jev, err = ResolveJevConfig(ctx, st, config, nil)
		if err != nil {
			return nil, err
		}
	}

	taskLock := m.getTaskLock(req.SessionKey)
	select {
	case taskLock <- struct{}{}:
		defer func() { <-taskLock }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	start := time.Now()
	if req.Reset {
		m.CloseSession(ctx, req.SessionKey)
	}
	if err := m.EnsureBrowser(ctx); err != nil {
		return nil, err
	}
	page, created, err := m.SessionPage(ctx, req.SessionKey, false, true, "")
	if err != nil {
		return nil, err
	}
	snap, err := m.Navigate(ctx, req.SessionKey, req.URL, page.ID)
	if err != nil {
		return nil, err
	}
	page, _, err = m.SessionPage(ctx, req.SessionKey, false, false, snap.TargetID)
	if err != nil {
		return nil, err
	}
	payload.TargetID, payload.PageWS = page.ID, page.WebSocketDebuggerURL
	var scoped *scopedCDP
	if engine == "browser_use" {
		version, err := m.endpoints.GetVersion(ctx)
		if err != nil {
			return nil, err
		}
		var stop func()
		payload.CDPURL, scoped, stop, err = startScopedCDP(ctx, version.WebSocketDebuggerURL, page.ID, func(target string) {
			m.rememberEngineTarget(ctx, req.SessionKey, target)
		})
		if err != nil {
			return nil, err
		}
		defer stop()
	}
	runner := runEngine
	if m.engineRunner != nil {
		runner = m.engineRunner
	}
	result, err := runner(ctx, payload)
	if err != nil {
		return nil, err
	}
	target := page.ID
	if result.TargetID != "" && result.TargetID != target {
		if scoped == nil {
			return nil, errors.New("浏览器引擎返回了不属于当前会话的标签")
		}
		scoped.mu.Lock()
		owned := scoped.ownsTarget(result.TargetID)
		scoped.mu.Unlock()
		if !owned {
			return nil, errors.New("浏览器引擎返回了不属于当前会话的标签")
		}
		target = result.TargetID
		m.rememberEngineTarget(ctx, req.SessionKey, target)
		m.targetMu.Lock()
		m.sessionActive[req.SessionKey] = target
		m.targetMu.Unlock()
	}
	m.targetMu.Lock()
	targets := append([]string(nil), m.sessionTargets[req.SessionKey]...)
	m.targetMu.Unlock()
	for _, ownedTarget := range targets {
		m.rememberEngineTarget(ctx, req.SessionKey, ownedTarget)
	}
	content, err := m.ExtractPageContent(ctx, req.SessionKey, target)
	if err != nil {
		return nil, err
	}
	diagnostics, err := m.Diagnostics(ctx, req.SessionKey, target)
	if err != nil {
		return nil, err
	}
	snap, err = m.CaptureScreenshot(ctx, req.SessionKey, true, target)
	if err != nil {
		return nil, err
	}
	errorsList := []string{}
	for _, entry := range diagnostics.Logs {
		if entry.Type == "error" {
			errorsList = append(errorsList, entry.Text)
		}
	}
	response := &BrowserTaskResult{
		OK: result.OK, Status: result.Status, BrowserSession: target, Resumed: !created,
		URL: content.URL, Title: content.Title, VisibleText: content.Text, Answer: result.Answer, ConsoleErrors: errorsList,
		Steps: result.Steps, ElapsedMS: time.Since(start).Milliseconds(), ScreenshotRevision: snap.Revision,
		SessionKey: req.SessionKey,
	}
	if req.IncludeScreenshot {
		response.Screenshot, response.MimeType = snap.Screenshot, snap.MimeType
	}
	return response, nil
}
