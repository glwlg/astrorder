package toolchain

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"

	"astrorder.dev/app-server/internal/envtarget"
)

const (
	oauthSessionTimeout = 5 * time.Minute
	oauthStartWait      = 2 * time.Second
	oauthMaxOutputBytes = 32 * 1024
)

var (
	ErrOAuthNotFound = errors.New("授权会话不存在")

	ansiEscapeRE = regexp.MustCompile(`\x1b(?:\[[0-9;?]*[ -/]*[@-~]|\][^\x07\x1b]*(?:\x07|\x1b\\))`)
	httpURLRE    = regexp.MustCompile(`https?://[^\s"'<>)\]]+`)
	// Matches labelled device/one-time codes like "Enter code: ABCD-1234" or "code ABCD-EFGH"
	labelledDeviceCodeRE = regexp.MustCompile(`(?i)(?:code|代码|设备码|验证码|授权码)[^A-Za-z0-9]{0,8}([A-Z0-9]{4,5}-[A-Z0-9]{4,5})`)
	// Standalone uppercase XXXX-XXXX or XXXX-XXXXX code on its own line/token
	standaloneDeviceCodeRE = regexp.MustCompile(`\b([A-Z0-9]{4}-[A-Z0-9]{4,5})\b`)
)

type OAuthStartRequest struct {
	EnvironmentID string `json:"environment_id"`
	AgentKind     string `json:"agent_kind"`
	Mode          string `json:"mode,omitempty"` // "browser" (default) | "device"
	OpenBrowser   *bool  `json:"open_browser,omitempty"`
}

type OAuthSessionView struct {
	ID             string      `json:"id"`
	EnvironmentID  string      `json:"environment_id"`
	AgentKind      string      `json:"agent_kind"`
	Mode           string      `json:"mode"`
	Status         string      `json:"status"` // starting | waiting_user | succeeded | failed | cancelled
	Detail         string      `json:"detail"`
	AuthURL        string      `json:"auth_url,omitempty"`
	DeviceCode     string      `json:"device_code,omitempty"`
	BrowserOpened  bool        `json:"browser_opened"`
	Output         string      `json:"output,omitempty"`
	Error          string      `json:"error,omitempty"`
	CreatedAt      string      `json:"created_at"`
	UpdatedAt      string      `json:"updated_at"`
	CompletedAt    string      `json:"completed_at,omitempty"`
	AuthConfigured bool        `json:"auth_configured"`
	AgentStatus    *ToolStatus `json:"agent_status,omitempty"`
}

type oauthSession struct {
	mu            sync.Mutex
	view          OAuthSessionView
	openBrowser   bool
	browserTried  bool
	cancel        context.CancelFunc
	readyOnce     sync.Once
	readyCh       chan struct{}
	browserOpener func(string) error
}

func (o *oauthSession) signalReady() {
	o.readyOnce.Do(func() {
		close(o.readyCh)
	})
}

func (o *oauthSession) snapshot() OAuthSessionView {
	o.mu.Lock()
	defer o.mu.Unlock()
	cp := o.view
	if o.view.AgentStatus != nil {
		stCopy := *o.view.AgentStatus
		cp.AgentStatus = &stCopy
	}
	return cp
}

func (o *oauthSession) appendOutput(chunk string, isLocal bool) {
	clean := stripANSI(chunk)
	if clean == "" {
		return
	}
	var urlToOpen string

	o.mu.Lock()
	o.view.Output = appendBounded(o.view.Output, clean, oauthMaxOutputBytes)
	o.view.UpdatedAt = time.Now().UTC().Format(time.RFC3339)

	if parsedURL := extractAuthURL(o.view.Output); parsedURL != "" && o.view.AuthURL == "" {
		o.view.AuthURL = parsedURL
	}
	if code := extractDeviceCode(o.view.Output); code != "" && o.view.DeviceCode == "" {
		o.view.DeviceCode = code
	}
	if o.view.Status == "starting" && (o.view.AuthURL != "" || o.view.DeviceCode != "") {
		o.view.Status = "waiting_user"
		if o.view.DeviceCode != "" {
			o.view.Detail = "请在浏览器中打开授权链接并输入设备码完成登录"
		} else {
			o.view.Detail = "请在浏览器中完成授权登录"
		}
	}
	if o.openBrowser && isLocal && !o.browserTried && o.view.AuthURL != "" {
		o.browserTried = true
		urlToOpen = o.view.AuthURL
	}
	readyNow := o.view.AuthURL != "" || o.view.DeviceCode != ""
	if o.view.Mode == "device" {
		readyNow = o.view.AuthURL != "" && o.view.DeviceCode != ""
	}
	opener := o.browserOpener
	o.mu.Unlock()

	if urlToOpen != "" && opener != nil {
		if err := opener(urlToOpen); err == nil {
			o.mu.Lock()
			o.view.BrowserOpened = true
			o.view.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
			o.mu.Unlock()
		}
	}
	if readyNow {
		o.signalReady()
	}
}

func stripANSI(text string) string {
	cleaned := ansiEscapeRE.ReplaceAllString(text, "")
	return strings.ReplaceAll(cleaned, "\r\n", "\n")
}

func appendBounded(existing, extra string, maxBytes int) string {
	combined := existing + extra
	if len(combined) <= maxBytes {
		return combined
	}
	return combined[len(combined)-maxBytes:]
}

func extractAuthURL(text string) string {
	matches := httpURLRE.FindAllString(text, -1)
	if len(matches) == 0 {
		return ""
	}
	best := ""
	for _, raw := range matches {
		cleaned := strings.TrimRight(raw, ".,;:'\"`")
		if _, err := validateBrowserURL(cleaned); err != nil {
			continue
		}
		lower := strings.ToLower(cleaned)
		if strings.Contains(lower, "oauth") ||
			strings.Contains(lower, "authorize") ||
			strings.Contains(lower, "login") ||
			strings.Contains(lower, "device") ||
			strings.Contains(lower, "auth.") ||
			strings.Contains(lower, "accounts.google.com") ||
			strings.Contains(lower, "github.com/login") {
			return cleaned
		}
		if best == "" {
			best = cleaned
		}
	}
	return best
}

func extractDeviceCode(text string) string {
	if m := labelledDeviceCodeRE.FindStringSubmatch(text); len(m) > 1 {
		return strings.ToUpper(strings.TrimSpace(m[1]))
	}
	lower := strings.ToLower(text)
	if strings.Contains(lower, "device") || strings.Contains(lower, "code") || strings.Contains(lower, "verify") || strings.Contains(lower, "设备码") {
		if m := standaloneDeviceCodeRE.FindStringSubmatch(text); len(m) > 1 {
			cand := strings.ToUpper(m[1])
			// Avoid matching dates or plain numeric pairs like 2026-1127
			if hasASCIIAlpha(cand) {
				return cand
			}
		}
	}
	return ""
}

func hasASCIIAlpha(s string) bool {
	for i := 0; i < len(s); i++ {
		if (s[i] >= 'A' && s[i] <= 'Z') || (s[i] >= 'a' && s[i] <= 'z') {
			return true
		}
	}
	return false
}

func validateBrowserURL(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" || strings.ContainsAny(trimmed, "\r\n\t \x00") {
		return "", errors.New("无效的浏览器 URL")
	}
	parsed, err := url.Parse(trimmed)
	if err != nil {
		return "", err
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", fmt.Errorf("仅允许 http/https 链接：%s", parsed.Scheme)
	}
	if parsed.Host == "" {
		return "", errors.New("浏览器 URL 缺少主机名")
	}
	return parsed.String(), nil
}

func normalizeOAuthKindAndArgs(rawKind, rawMode string) (string, string, []string, error) {
	kind := strings.ToLower(strings.TrimSpace(rawKind))
	if engine, ok := LookupAgentEngine(kind); ok {
		kind = engine.Kind
	}
	mode := strings.ToLower(strings.TrimSpace(rawMode))
	if mode == "" {
		mode = "browser"
	}
	if mode != "browser" && mode != "device" {
		return "", "", nil, envtarget.Errorf(422, "不支持的登录模式：%s（仅支持 browser 或 device）", rawMode)
	}
	switch kind {
	case "codex":
		if mode == "device" {
			return "codex", mode, []string{"login", "--device-auth"}, nil
		}
		return "codex", mode, []string{"login"}, nil
	case "antigravity":
		return "antigravity", mode, []string{"login"}, nil
	case "grok":
		return "grok", mode, []string{"login"}, nil
	case "gh":
		return "gh", mode, []string{"auth", "login", "--web"}, nil
	case "hermes":
		return "", "", nil, envtarget.Errorf(422, "Hermes 引擎请通过 API Key 配置或交互终端执行 hermes setup")
	default:
		return "", "", nil, envtarget.Errorf(422, "暂不支持浏览器 OAuth 登录的引擎：%s", rawKind)
	}
}

func (s *Service) StartOAuth(ctx context.Context, req OAuthStartRequest) (OAuthSessionView, error) {
	kind, mode, args, err := normalizeOAuthKindAndArgs(req.AgentKind, req.Mode)
	if err != nil {
		return OAuthSessionView{}, err
	}
	envID := strings.TrimSpace(req.EnvironmentID)
	if envID == "" {
		envID = "local"
	}
	env, err := s.resolver.Open(ctx, envID)
	if err != nil {
		return OAuthSessionView{}, err
	}
	if !env.IsLocal() {
		return OAuthSessionView{}, envtarget.Errorf(422, "当前一键浏览器登录仅支持本机环境，远程环境请在交互终端完成登录")
	}
	spec, ok := LookupSpec(kind)
	if !ok {
		return OAuthSessionView{}, envtarget.Errorf(422, "未知引擎：%s", kind)
	}

	executable := s.readCustomExecutable(ctx, env.Target(), spec, nil)
	if executable == "" {
		if found, ok := s.locateBinary(ctx, env, spec); ok {
			executable = found
		}
	}
	if executable == "" {
		return OAuthSessionView{}, envtarget.Errorf(422, "尚未检测到 %s 可执行文件，请先完成安装后再登录", spec.Name)
	}

	openBrowser := true
	if req.OpenBrowser != nil {
		openBrowser = *req.OpenBrowser
	}

	effectiveNet := s.ResolveEffectiveNetwork(ctx, env.Target().ID, "", "")
	extraEnv, _ := buildInstallEnv(effectiveNet.ProxyURL, effectiveNet.Mirror)

	runCtx, cancel := context.WithTimeout(s.jobContext, oauthSessionTimeout)
	now := time.Now().UTC().Format(time.RFC3339)
	opener := s.browserOpener
	if opener == nil {
		opener = defaultOpenSystemBrowser
	}
	sess := &oauthSession{
		view: OAuthSessionView{
			ID:            "oauth-" + rand.Text(),
			EnvironmentID: env.Target().ID,
			AgentKind:     kind,
			Mode:          mode,
			Status:        "starting",
			Detail:        fmt.Sprintf("正在启动 %s 浏览器授权登录…", spec.Name),
			CreatedAt:     now,
			UpdatedAt:     now,
		},
		openBrowser:   openBrowser,
		cancel:        cancel,
		readyCh:       make(chan struct{}),
		browserOpener: opener,
	}

	s.oauthMu.Lock()
	s.oauthSessions[sess.view.ID] = sess
	s.pruneOAuthSessionsLocked()
	s.oauthMu.Unlock()

	s.jobWG.Add(1)
	go func() {
		defer s.jobWG.Done()
		defer cancel()
		s.runOAuthSession(runCtx, sess, env, spec, executable, args, extraEnv)
	}()

	// Wait briefly so fast CLIs return auth_url/device_code in the initial response.
	waitTimer := time.NewTimer(oauthStartWait)
	defer waitTimer.Stop()
	select {
	case <-sess.readyCh:
	case <-waitTimer.C:
	case <-ctx.Done():
	}

	return sess.snapshot(), nil
}

func (s *Service) GetOAuthStatus(id string) (OAuthSessionView, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return OAuthSessionView{}, envtarget.Errorf(422, "缺少授权会话 id")
	}
	s.oauthMu.Lock()
	sess := s.oauthSessions[id]
	s.oauthMu.Unlock()
	if sess == nil {
		return OAuthSessionView{}, ErrOAuthNotFound
	}
	return sess.snapshot(), nil
}

func (s *Service) CancelOAuth(id string) (OAuthSessionView, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return OAuthSessionView{}, envtarget.Errorf(422, "缺少授权会话 id")
	}
	s.oauthMu.Lock()
	sess := s.oauthSessions[id]
	s.oauthMu.Unlock()
	if sess == nil {
		return OAuthSessionView{}, ErrOAuthNotFound
	}
	sess.mu.Lock()
	if sess.view.Status == "starting" || sess.view.Status == "waiting_user" {
		sess.view.Status = "cancelled"
		sess.view.Detail = "已取消授权登录"
		now := time.Now().UTC().Format(time.RFC3339)
		sess.view.UpdatedAt = now
		sess.view.CompletedAt = now
	}
	cancel := sess.cancel
	sess.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	sess.signalReady()
	return sess.snapshot(), nil
}

func (s *Service) pruneOAuthSessionsLocked() {
	if len(s.oauthSessions) <= 64 {
		return
	}
	for id, sess := range s.oauthSessions {
		snap := sess.snapshot()
		if snap.Status != "starting" && snap.Status != "waiting_user" {
			delete(s.oauthSessions, id)
			if len(s.oauthSessions) <= 48 {
				break
			}
		}
	}
}

func (s *Service) runOAuthSession(
	ctx context.Context,
	sess *oauthSession,
	env envtarget.Env,
	spec ToolSpec,
	executable string,
	args []string,
	extraEnv map[string]string,
) {
	defer sess.signalReady()

	cmd := exec.CommandContext(ctx, executable, args...)
	configureHiddenCmd(cmd)
	cmd.Env = s.localChildEnv(extraEnv)
	cmd.Stdin = bytes.NewReader(nil)
	cmd.WaitDelay = 2 * time.Second

	pr, pw := io.Pipe()
	cmd.Stdout = pw
	cmd.Stderr = pw

	if err := cmd.Start(); err != nil {
		_ = pw.Close()
		_ = pr.Close()
		s.finishOAuthSession(sess, "failed", "无法启动授权登录进程："+err.Error(), err.Error(), nil)
		return
	}

	readDone := make(chan struct{})
	go func() {
		defer close(readDone)
		buf := make([]byte, 4096)
		for {
			n, err := pr.Read(buf)
			if n > 0 {
				sess.appendOutput(string(buf[:n]), env.IsLocal())
			}
			if err != nil {
				return
			}
		}
	}()

	waitErr := cmd.Wait()
	_ = pw.Close()
	<-readDone
	_ = pr.Close()

	// Check if explicitly cancelled first.
	sess.mu.Lock()
	alreadyCancelled := sess.view.Status == "cancelled"
	sess.mu.Unlock()
	if alreadyCancelled || errors.Is(ctx.Err(), context.Canceled) {
		s.finishOAuthSession(sess, "cancelled", "已取消授权登录", "", nil)
		return
	}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		s.finishOAuthSession(sess, "failed", "授权登录超时，请重试", "授权登录超时", nil)
		return
	}

	// Refresh status for this agent only (fast, avoids probing unrelated tools).
	inspectCtx, cancelInspect := context.WithTimeout(context.Background(), 10*time.Second)
	statusCopy := s.inspectSingleTool(inspectCtx, env, spec)
	cancelInspect()
	refreshed := &statusCopy

	if waitErr == nil {
		// Trigger non-fatal hot reload in case daemon should refresh runtime/config state.
		if env.IsLocal() && spec.Kind == ItemKindAgent {
			reloadCtx, cancelReload := context.WithTimeout(context.Background(), 5*time.Second)
			_ = s.tryReloadDaemonConfig(reloadCtx)
			cancelReload()
		}
		detail := fmt.Sprintf("%s 授权登录已完成", spec.Name)
		if refreshed != nil && refreshed.AuthConfigured != nil && *refreshed.AuthConfigured {
			detail = fmt.Sprintf("%s 授权登录成功，引擎已就绪", spec.Name)
		}
		s.finishOAuthSession(sess, "succeeded", detail, "", refreshed)
		return
	}

	errMsg := waitErr.Error()
	var exitErr *exec.ExitError
	if errors.As(waitErr, &exitErr) {
		errMsg = fmt.Sprintf("登录命令退出码 %d", exitErr.ExitCode())
	}
	s.finishOAuthSession(sess, "failed", fmt.Sprintf("%s 授权登录未完成：%s", spec.Name, errMsg), errMsg, refreshed)
}

func (s *Service) finishOAuthSession(sess *oauthSession, status, detail, errMsg string, agentStatus *ToolStatus) {
	now := time.Now().UTC().Format(time.RFC3339)
	sess.mu.Lock()
	defer sess.mu.Unlock()
	if sess.view.Status == "cancelled" && status != "cancelled" {
		return
	}
	sess.view.Status = status
	sess.view.Detail = detail
	sess.view.Error = errMsg
	sess.view.UpdatedAt = now
	sess.view.CompletedAt = now
	if agentStatus != nil {
		cp := *agentStatus
		sess.view.AgentStatus = &cp
		if cp.AuthConfigured != nil {
			sess.view.AuthConfigured = *cp.AuthConfigured
		}
	}
}
