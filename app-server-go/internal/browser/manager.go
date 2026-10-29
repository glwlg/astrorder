package browser

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// SessionManager manages CDP sessions, tabs, and tab state locks.
type SessionManager struct {
	devToolsBase string
	httpClient   *http.Client
	endpoints    *CDPEndpoints
	noAutoExec   bool
	engineRunner func(context.Context, engineRequest) (engineResult, error)

	sessionMu      sync.RWMutex
	targetMu       sync.Mutex               // protects ownership maps across different sessions
	sessionLocks   map[string]*sync.Mutex   // lock per sessionKey
	taskLocks      map[string]chan struct{} // serialize autonomous tasks per session
	sessionTargets map[string][]string      // sessionKey -> list of targetIDs
	sessionActive  map[string]string        // sessionKey -> active targetID

	screenMu    sync.RWMutex
	screenshots map[string]*Snapshot // key -> snapshot (keys: sessionKey or sessionKey::targetID)
	logsMu      sync.RWMutex
	sessionLogs map[string][]LogEntry // sessionKey::targetID -> logs
}

// NewSessionManager creates a SessionManager.
func NewSessionManager(devToolsBase string, noAutoExec bool) *SessionManager {
	if devToolsBase == "" {
		devToolsBase = fmt.Sprintf("http://127.0.0.1:%d", defaultDevToolsPort)
	}
	devToolsBase = strings.TrimRight(devToolsBase, "/")
	httpClient := &http.Client{Timeout: 5 * time.Second}
	return &SessionManager{
		devToolsBase:   devToolsBase,
		httpClient:     httpClient,
		endpoints:      &CDPEndpoints{BaseURL: devToolsBase, HTTPClient: httpClient},
		noAutoExec:     noAutoExec,
		sessionLocks:   make(map[string]*sync.Mutex),
		taskLocks:      make(map[string]chan struct{}),
		sessionTargets: make(map[string][]string),
		sessionActive:  make(map[string]string),
		screenshots:    make(map[string]*Snapshot),
		sessionLogs:    make(map[string][]LogEntry),
	}
}

func (m *SessionManager) getTaskLock(sessionKey string) chan struct{} {
	m.sessionMu.Lock()
	defer m.sessionMu.Unlock()
	lock := m.taskLocks[sessionKey]
	if lock == nil {
		lock = make(chan struct{}, 1)
		m.taskLocks[sessionKey] = lock
	}
	return lock
}

// GetLock returns the lock for a specific sessionKey.
func (m *SessionManager) getLock(sessionKey string) *sync.Mutex {
	m.sessionMu.Lock()
	defer m.sessionMu.Unlock()
	l, ok := m.sessionLocks[sessionKey]
	if !ok {
		l = &sync.Mutex{}
		m.sessionLocks[sessionKey] = l
	}
	return l
}

// EnsureBrowser checks if CDP is reachable, or launches the browser if allowed.
func (m *SessionManager) EnsureBrowser(ctx context.Context) error {
	_, err := m.endpoints.GetVersion(ctx)
	if err == nil {
		return nil
	}
	if m.noAutoExec {
		return fmt.Errorf("CDP 端口不可用，未允许自动启动: %w", err)
	}

	// Try launching
	u, parseErr := url.Parse(m.devToolsBase)
	port := defaultDevToolsPort
	if parseErr == nil && u.Port() != "" {
		if p, convErr := strconv.Atoi(u.Port()); convErr == nil {
			port = p
		}
	}
	if launchErr := LaunchBrowserProcess(port); launchErr != nil {
		return fmt.Errorf("启动浏览器失败: %w", launchErr)
	}

	// Wait up to 10s
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(150 * time.Millisecond):
			_, err = m.endpoints.GetVersion(ctx)
			if err == nil {
				return nil
			}
		}
	}
	return errors.New("Edge 已启动，但 CDP 端口不可用")
}

// RecoverSessionTargets inspects existing pages via /json/list and recovers window.name tags.
func (m *SessionManager) recoverSessionTargets(ctx context.Context, pages []PageTarget) {
	m.targetMu.Lock()
	defer m.targetMu.Unlock()
	pageIDs := make(map[string]bool)
	for _, p := range pages {
		pageIDs[p.ID] = true
	}

	// Clean dead targets
	for sKey, targets := range m.sessionTargets {
		var alive []string
		for _, tid := range targets {
			if pageIDs[tid] {
				alive = append(alive, tid)
			}
		}
		if len(alive) == 0 {
			delete(m.sessionTargets, sKey)
			delete(m.sessionActive, sKey)
		} else {
			m.sessionTargets[sKey] = alive
			if !pageIDs[m.sessionActive[sKey]] {
				m.sessionActive[sKey] = alive[len(alive)-1]
			}
		}
	}

	known := make(map[string]bool)
	for _, targets := range m.sessionTargets {
		for _, tid := range targets {
			known[tid] = true
		}
	}

	for _, p := range pages {
		if known[p.ID] || p.WebSocketDebuggerURL == "" {
			continue
		}
		client, err := DialCDP(ctx, p.WebSocketDebuggerURL)
		if err != nil {
			continue
		}
		res, err := client.Call(ctx, "Runtime.evaluate", map[string]any{
			"expression":    "window.name",
			"returnByValue": true,
		})
		_ = client.Close()
		if err != nil {
			continue
		}

		var evalResult struct {
			Result struct {
				Value any `json:"value"`
			} `json:"result"`
		}
		_ = json.Unmarshal(res, &evalResult)
		strVal, _ := evalResult.Result.Value.(string)
		if sKey, ok := MarkerSessionKey(strVal); ok && sKey != "" {
			targets := m.sessionTargets[sKey]
			already := false
			for _, tid := range targets {
				if tid == p.ID {
					already = true
					break
				}
			}
			if !already {
				targets = append(targets, p.ID)
				m.sessionTargets[sKey] = targets
			}
			if m.sessionActive[sKey] == "" {
				m.sessionActive[sKey] = p.ID
			}
		}
	}
}

// NewPage creates a new browser page using Target.createTarget on browser websocket.
func (m *SessionManager) newPage(ctx context.Context) (*PageTarget, error) {
	var ver *VersionInfo
	ver, err := m.endpoints.GetVersion(ctx)
	if err != nil {
		if ensureErr := m.EnsureBrowser(ctx); ensureErr != nil {
			return nil, ensureErr
		}
		ver, err = m.endpoints.GetVersion(ctx)
		if err != nil {
			return nil, fmt.Errorf("read cdp version: %w", err)
		}
	}

	if ver.WebSocketDebuggerURL == "" {
		return nil, errors.New("browser webSocketDebuggerUrl is missing")
	}

	browserConn, err := DialCDP(ctx, ver.WebSocketDebuggerURL)
	if err != nil {
		return nil, fmt.Errorf("connect browser cdp: %w", err)
	}
	defer browserConn.Close()

	// Fixture-backed managers must never alter a real browser profile.
	if !m.noAutoExec {
		if err := ensureSessionExtension(ctx, browserConn, func(ctx context.Context) error {
			return m.enableExtensionDeveloperMode(ctx, browserConn)
		}); err != nil {
			return nil, err
		}
	}

	res, err := browserConn.Call(ctx, "Target.createTarget", map[string]any{"url": "about:blank"})
	if err != nil {
		return nil, fmt.Errorf("Target.createTarget: %w", err)
	}

	var createResp struct {
		TargetID string `json:"targetId"`
	}
	if err := json.Unmarshal(res, &createResp); err != nil || createResp.TargetID == "" {
		return nil, fmt.Errorf("invalid Target.createTarget response: %v", err)
	}

	_, _ = browserConn.Call(ctx, "Target.activateTarget", map[string]any{"targetId": createResp.TargetID})

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		pages, _ := m.endpoints.ListPages(ctx)
		for _, p := range pages {
			if p.ID == createResp.TargetID {
				return &p, nil
			}
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
	}
	return nil, errors.New("Edge 已创建标签页，但未能连接")
}

func (m *SessionManager) tagPage(ctx context.Context, page *PageTarget, sessionKey string) error {
	client, err := DialCDP(ctx, page.WebSocketDebuggerURL)
	if err != nil {
		return err
	}
	defer client.Close()

	marker := SessionMarker(sessionKey)
	b, _ := json.Marshal(marker)
	_, err = client.Call(ctx, "Runtime.evaluate", map[string]any{
		"expression":    fmt.Sprintf("window.name=%s;window.dispatchEvent(new Event('astrorder:session-bound'));", string(b)),
		"returnByValue": true,
	})
	return err
}

// SessionPage resolves or creates the page target for sessionKey.
func (m *SessionManager) SessionPage(ctx context.Context, sessionKey string, reset bool, create bool, targetID string) (*PageTarget, bool, error) {
	pages, _ := m.endpoints.ListPages(ctx)
	m.recoverSessionTargets(ctx, pages)

	m.targetMu.Lock()
	defer m.targetMu.Unlock()
	targets := m.sessionTargets[sessionKey]
	if targetID != "" && !ownsTarget(targets, targetID) {
		return nil, false, errors.New("指定的标签页不属于当前会话")
	}
	resolvedID := targetID
	if resolvedID == "" {
		resolvedID = m.sessionActive[sessionKey]
	}

	var existing *PageTarget
	for _, p := range pages {
		if p.ID == resolvedID {
			existing = &p
			break
		}
	}

	if existing != nil && !reset {
		m.sessionActive[sessionKey] = existing.ID
		return existing, false, nil
	}

	if existing != nil {
		_ = m.endpoints.ClosePage(ctx, existing.ID)
		var kept []string
		for _, tid := range targets {
			if tid != existing.ID {
				kept = append(kept, tid)
			}
		}
		m.sessionTargets[sessionKey] = kept
		m.screenMu.Lock()
		delete(m.screenshots, sessionKey)
		delete(m.screenshots, sessionKey+"::"+existing.ID)
		m.screenMu.Unlock()
	}

	if !create {
		return nil, false, errors.New("该会话还没有浏览器标签页")
	}

	page, err := m.newPage(ctx)
	if err != nil {
		return nil, false, err
	}

	if err := m.tagPage(ctx, page, sessionKey); err != nil {
		_ = m.endpoints.ClosePage(ctx, page.ID)
		return nil, false, fmt.Errorf("bind browser session: %w", err)
	}
	targets = m.sessionTargets[sessionKey]
	already := false
	for _, tid := range targets {
		if tid == page.ID {
			already = true
			break
		}
	}
	if !already {
		targets = append(targets, page.ID)
		m.sessionTargets[sessionKey] = targets
	}
	m.sessionActive[sessionKey] = page.ID
	return page, true, nil
}

// ConnectPage connects a CDP client to a target and sets up log listeners.
func (m *SessionManager) ConnectPage(ctx context.Context, page *PageTarget, sessionKey string) (*CDPClient, error) {
	client, err := DialCDP(ctx, page.WebSocketDebuggerURL)
	if err != nil {
		return nil, err
	}

	logKey := sessionKey + "::" + page.ID
	client.SetEventHandler(func(method string, params json.RawMessage) {
		m.handleCDPEvent(logKey, method, params)
	})

	return client, nil
}

func (m *SessionManager) handleCDPEvent(logKey string, method string, params json.RawMessage) {
	now := float64(time.Now().UnixNano()) / 1e9
	var logType, text string

	switch method {
	case "Runtime.exceptionThrown":
		var p struct {
			ExceptionDetails struct {
				Text      string `json:"text"`
				Exception struct {
					Description string `json:"description"`
				} `json:"exception"`
			} `json:"exceptionDetails"`
		}
		if json.Unmarshal(params, &p) == nil {
			text = p.ExceptionDetails.Exception.Description
			if text == "" {
				text = p.ExceptionDetails.Text
			}
			logType = "error"
		}
	case "Runtime.consoleAPICalled":
		var p struct {
			Type string `json:"type"`
			Args []struct {
				Value       any    `json:"value"`
				Description string `json:"description"`
			} `json:"args"`
		}
		if json.Unmarshal(params, &p) == nil {
			var words []string
			for _, a := range p.Args {
				if a.Value != nil {
					words = append(words, fmt.Sprintf("%v", a.Value))
				} else if a.Description != "" {
					words = append(words, a.Description)
				}
			}
			text = strings.Join(words, " ")
			if p.Type == "error" || p.Type == "assert" {
				logType = "error"
			} else {
				logType = p.Type
			}
		}
	case "Log.entryAdded":
		var p struct {
			Entry struct {
				Level string `json:"level"`
				Text  string `json:"text"`
			} `json:"entry"`
		}
		if json.Unmarshal(params, &p) == nil {
			text = p.Entry.Text
			if p.Entry.Level == "error" {
				logType = "error"
			} else {
				logType = p.Entry.Level
			}
		}
	case "Network.loadingFailed":
		var p struct {
			Type      string `json:"type"`
			ErrorText string `json:"errorText"`
		}
		if json.Unmarshal(params, &p) == nil {
			resourceType := p.Type
			if resourceType == "" {
				resourceType = "Resource"
			}
			errText := p.ErrorText
			if errText == "" {
				errText = "failed"
			}
			text = fmt.Sprintf("%s 加载失败: %s", resourceType, errText)
			logType = "network_error"
		}
	case "Network.responseReceived":
		var p struct {
			Response struct {
				Status int    `json:"status"`
				URL    string `json:"url"`
			} `json:"response"`
		}
		if json.Unmarshal(params, &p) == nil {
			if p.Response.Status >= 400 {
				text = fmt.Sprintf("HTTP %d: %s", p.Response.Status, p.Response.URL)
				logType = "network_error"
			}
		}
	}

	if text != "" {
		if len(text) > 1000 {
			text = text[:1000]
		}
		m.logsMu.Lock()
		logs := m.sessionLogs[logKey]
		logs = append(logs, LogEntry{
			Type: logType,
			Text: text,
			Time: now,
		})
		if len(logs) > 100 {
			logs = logs[len(logs)-100:]
		}
		m.sessionLogs[logKey] = logs
		m.logsMu.Unlock()
	}
}

// ListTabs returns Tab info for a sessionKey.
func (m *SessionManager) ListTabs(ctx context.Context, sessionKey string) TabsInfo {
	pages, _ := m.endpoints.ListPages(ctx)
	m.recoverSessionTargets(ctx, pages)

	m.targetMu.Lock()
	defer m.targetMu.Unlock()
	targets := m.sessionTargets[sessionKey]
	activeID := m.sessionActive[sessionKey]
	pageMap := make(map[string]PageTarget)
	for _, p := range pages {
		pageMap[p.ID] = p
	}

	var tabs []Tab
	for _, tid := range targets {
		if p, ok := pageMap[tid]; ok {
			title := p.Title
			if title == "" {
				title = "新标签页"
			}
			urlStr := p.URL
			if urlStr == "" {
				urlStr = "about:blank"
			}
			tabs = append(tabs, Tab{
				ID:     tid,
				Title:  title,
				URL:    urlStr,
				Active: tid == activeID,
			})
		}
	}

	return TabsInfo{
		SessionKey:     sessionKey,
		ActiveTargetID: activeID,
		Tabs:           tabs,
	}
}

// RememberScreenshot stores screenshot snapshot and associates tabs info.
func (m *SessionManager) RememberScreenshot(ctx context.Context, sessionKey string, data string, pageURL string, pageTitle string, targetID string) *Snapshot {
	tabsInfo := m.ListTabs(ctx, sessionKey)
	tid := targetID
	if tid == "" {
		tid = tabsInfo.ActiveTargetID
	}

	m.logsMu.RLock()
	logs := m.sessionLogs[sessionKey+"::"+tid]
	errCount := 0
	for _, l := range logs {
		if l.Type == "error" || l.Type == "network_error" {
			errCount++
		}
	}
	m.logsMu.RUnlock()

	now := time.Now()
	snapshot := &Snapshot{
		URL:            pageURL,
		Title:          pageTitle,
		Screenshot:     data,
		MimeType:       "image/jpeg",
		Revision:       now.UnixNano(),
		CapturedAt:     float64(now.UnixNano()) / 1e9,
		SessionKey:     sessionKey,
		TargetID:       tid,
		ActiveTargetID: tabsInfo.ActiveTargetID,
		Tabs:           tabsInfo.Tabs,
		ErrorCount:     errCount,
	}

	m.screenMu.Lock()
	m.screenshots[sessionKey] = snapshot
	if tid != "" {
		m.screenshots[sessionKey+"::"+tid] = snapshot
	}
	m.screenMu.Unlock()

	return snapshot
}

// LatestScreenshot returns cached snapshot if available.
func (m *SessionManager) LatestScreenshot(ctx context.Context, sessionKey string, targetID string) *Snapshot {
	m.screenMu.RLock()
	key := sessionKey
	if targetID != "" {
		key = sessionKey + "::" + targetID
	}
	snap := m.screenshots[key]
	m.screenMu.RUnlock()

	if snap == nil {
		return nil
	}

	// Update tabs dynamically
	tabsInfo := m.ListTabs(ctx, sessionKey)
	res := *snap
	res.Tabs = tabsInfo.Tabs
	res.ActiveTargetID = tabsInfo.ActiveTargetID
	return &res
}

// CloseSession closes all browser tabs for the session.
func (m *SessionManager) CloseSession(ctx context.Context, sessionKey string) {
	lock := m.getLock(sessionKey)
	lock.Lock()
	defer lock.Unlock()

	pages, _ := m.endpoints.ListPages(ctx)
	m.recoverSessionTargets(ctx, pages)

	m.targetMu.Lock()
	targets := m.sessionTargets[sessionKey]
	delete(m.sessionTargets, sessionKey)
	delete(m.sessionActive, sessionKey)
	m.targetMu.Unlock()

	m.screenMu.Lock()
	delete(m.screenshots, sessionKey)
	for _, tid := range targets {
		delete(m.screenshots, sessionKey+"::"+tid)
	}
	m.screenMu.Unlock()

	for _, tid := range targets {
		_ = m.endpoints.ClosePage(ctx, tid)
	}
}

// SelectTab selects a tab for the session and activates it in CDP.
func (m *SessionManager) SelectTab(ctx context.Context, sessionKey string, targetID string) (*Snapshot, error) {
	lock := m.getLock(sessionKey)
	lock.Lock()
	defer lock.Unlock()

	pages, _ := m.endpoints.ListPages(ctx)
	m.recoverSessionTargets(ctx, pages)

	m.targetMu.Lock()
	targets := m.sessionTargets[sessionKey]
	found := false
	for _, tid := range targets {
		if tid == targetID {
			found = true
			break
		}
	}
	if !found {
		m.targetMu.Unlock()
		return nil, errors.New("指定的标签页不属于当前会话")
	}

	m.sessionActive[sessionKey] = targetID
	m.targetMu.Unlock()

	ver, _ := m.endpoints.GetVersion(ctx)
	if ver != nil && ver.WebSocketDebuggerURL != "" {
		if client, err := DialCDP(ctx, ver.WebSocketDebuggerURL); err == nil {
			_, _ = client.Call(ctx, "Target.activateTarget", map[string]any{"targetId": targetID})
			_ = client.Close()
		}
	}

	return m.CaptureScreenshot(ctx, sessionKey, false, targetID)
}

// NewTab creates a new tab in the session.
func (m *SessionManager) NewTab(ctx context.Context, sessionKey string, urlStr string) (*Snapshot, error) {
	lock := m.getLock(sessionKey)
	lock.Lock()

	pages, _ := m.endpoints.ListPages(ctx)
	m.recoverSessionTargets(ctx, pages)

	page, err := m.newPage(ctx)
	if err != nil {
		lock.Unlock()
		return nil, err
	}
	if err := m.tagPage(ctx, page, sessionKey); err != nil {
		_ = m.endpoints.ClosePage(ctx, page.ID)
		lock.Unlock()
		return nil, fmt.Errorf("bind browser session: %w", err)
	}
	m.targetMu.Lock()

	targets := m.sessionTargets[sessionKey]
	already := false
	for _, tid := range targets {
		if tid == page.ID {
			already = true
			break
		}
	}
	if !already {
		targets = append(targets, page.ID)
		m.sessionTargets[sessionKey] = targets
	}
	m.sessionActive[sessionKey] = page.ID

	if urlStr != "" && urlStr != "about:blank" {
		m.targetMu.Unlock()
		lock.Unlock()
		return m.Navigate(ctx, sessionKey, urlStr, page.ID)
	}
	m.targetMu.Unlock()
	lock.Unlock()
	return m.CaptureScreenshot(ctx, sessionKey, false, page.ID)
}

// CloseTab closes a specific tab and updates active target.
func (m *SessionManager) CloseTab(ctx context.Context, sessionKey string, targetID string) (*Snapshot, error) {
	lock := m.getLock(sessionKey)
	lock.Lock()
	defer lock.Unlock()

	pages, _ := m.endpoints.ListPages(ctx)
	m.recoverSessionTargets(ctx, pages)

	m.targetMu.Lock()
	targets := m.sessionTargets[sessionKey]
	if !ownsTarget(targets, targetID) {
		m.targetMu.Unlock()
		return nil, errors.New("指定的标签页不属于当前会话")
	}
	var remaining []string
	for _, tid := range targets {
		if tid != targetID {
			remaining = append(remaining, tid)
		}
	}
	m.sessionTargets[sessionKey] = remaining

	if m.sessionActive[sessionKey] == targetID {
		if len(remaining) > 0 {
			m.sessionActive[sessionKey] = remaining[len(remaining)-1]
		} else {
			delete(m.sessionActive, sessionKey)
		}
	}
	nextActive := m.sessionActive[sessionKey]
	m.targetMu.Unlock()

	m.screenMu.Lock()
	delete(m.screenshots, sessionKey+"::"+targetID)
	m.screenMu.Unlock()

	_ = m.endpoints.ClosePage(ctx, targetID)

	if nextActive != "" {
		return m.CaptureScreenshot(ctx, sessionKey, false, nextActive)
	}

	m.screenMu.Lock()
	delete(m.screenshots, sessionKey)
	m.screenMu.Unlock()

	return &Snapshot{
		URL:            "",
		Title:          "",
		Screenshot:     "",
		MimeType:       "image/jpeg",
		Revision:       time.Now().UnixNano(),
		CapturedAt:     float64(time.Now().UnixNano()) / 1e9,
		SessionKey:     sessionKey,
		TargetID:       "",
		ActiveTargetID: "",
		Tabs:           []Tab{},
	}, nil
}

// CaptureScreenshot captures screenshot for a session tab.
func (m *SessionManager) CaptureScreenshot(ctx context.Context, sessionKey string, requireOwner bool, targetID string) (*Snapshot, error) {
	page, _, err := m.SessionPage(ctx, sessionKey, false, !requireOwner, targetID)
	if err != nil {
		return nil, err
	}

	client, err := m.ConnectPage(ctx, page, sessionKey)
	if err != nil {
		return nil, err
	}
	defer client.Close()

	shotRes, err := client.Call(ctx, "Page.captureScreenshot", map[string]any{
		"format":  "jpeg",
		"quality": 80,
	})
	if err != nil {
		return nil, err
	}

	var shotData struct {
		Data string `json:"data"`
	}
	_ = json.Unmarshal(shotRes, &shotData)

	evalRes, _ := client.Call(ctx, "Runtime.evaluate", map[string]any{
		"expression":    "({url:location.href,title:document.title})",
		"returnByValue": true,
	})
	var evalData struct {
		Result struct {
			Value struct {
				URL   string `json:"url"`
				Title string `json:"title"`
			} `json:"value"`
		} `json:"result"`
	}
	_ = json.Unmarshal(evalRes, &evalData)

	pURL := evalData.Result.Value.URL
	pTitle := evalData.Result.Value.Title
	if pURL == "" {
		pURL = page.URL
	}
	if pTitle == "" {
		pTitle = page.Title
	}

	return m.RememberScreenshot(ctx, sessionKey, shotData.Data, pURL, pTitle, page.ID), nil
}

// Navigate navigates a session tab to a URL.
func (m *SessionManager) Navigate(ctx context.Context, sessionKey string, urlStr string, targetID string) (*Snapshot, error) {
	parsed, err := url.Parse(urlStr)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return nil, errors.New("url 必须是完整的 http 或 https 地址")
	}

	lock := m.getLock(sessionKey)
	lock.Lock()
	defer lock.Unlock()

	page, _, err := m.SessionPage(ctx, sessionKey, false, true, targetID)
	if err != nil {
		return nil, err
	}

	client, err := m.ConnectPage(ctx, page, sessionKey)
	if err != nil {
		return nil, err
	}
	defer client.Close()

	if _, err := client.Call(ctx, "Page.navigate", map[string]any{"url": urlStr}); err != nil {
		return nil, err
	}

	// Poll readyState complete up to 15s
	deadline := time.Now().Add(15 * time.Second)
	pageURL := urlStr
	pageTitle := ""

	for time.Now().Before(deadline) {
		res, err := client.Call(ctx, "Runtime.evaluate", map[string]any{
			"expression":    "({ready:document.readyState,url:location.href,title:document.title})",
			"returnByValue": true,
		})
		if err == nil {
			var state struct {
				Result struct {
					Value struct {
						Ready string `json:"ready"`
						URL   string `json:"url"`
						Title string `json:"title"`
					} `json:"value"`
				} `json:"result"`
			}
			if json.Unmarshal(res, &state) == nil {
				val := state.Result.Value
				if val.URL != "" {
					pageURL = val.URL
				}
				if val.Title != "" {
					pageTitle = val.Title
				}
				if val.Ready == "complete" {
					break
				}
			}
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
	}

	// Re-tag window.name
	marker := SessionMarker(sessionKey)
	b, _ := json.Marshal(marker)
	_, _ = client.Call(ctx, "Runtime.evaluate", map[string]any{
		"expression": fmt.Sprintf("window.name=%s;window.dispatchEvent(new Event('astrorder:session-bound'));", string(b)),
	})

	shotRes, err := client.Call(ctx, "Page.captureScreenshot", map[string]any{
		"format":  "jpeg",
		"quality": 80,
	})
	if err != nil {
		return nil, err
	}
	var shotData struct {
		Data string `json:"data"`
	}
	_ = json.Unmarshal(shotRes, &shotData)

	return m.RememberScreenshot(ctx, sessionKey, shotData.Data, pageURL, pageTitle, page.ID), nil
}

// Interact performs mouse/keyboard/navigation actions on a session tab.
func (m *SessionManager) Interact(ctx context.Context, sessionKey string, action string, targetID string, x, y, ratioX, ratioY, deltaY *float64, text *string) (*Snapshot, error) {
	lock := m.getLock(sessionKey)
	lock.Lock()
	defer lock.Unlock()

	page, _, err := m.SessionPage(ctx, sessionKey, false, true, targetID)
	if err != nil {
		return nil, err
	}

	client, err := m.ConnectPage(ctx, page, sessionKey)
	if err != nil {
		return nil, err
	}
	defer client.Close()

	_, _ = client.Call(ctx, "Runtime.enable", nil)
	_, _ = client.Call(ctx, "Log.enable", nil)

	// Determine viewport
	vw, vh := 1120.0, 780.0
	vpRes, err := client.Call(ctx, "Runtime.evaluate", map[string]any{
		"expression":    "({width: window.innerWidth, height: window.innerHeight})",
		"returnByValue": true,
	})
	if err == nil {
		var vp struct {
			Result struct {
				Value struct {
					Width  float64 `json:"width"`
					Height float64 `json:"height"`
				} `json:"value"`
			} `json:"result"`
		}
		if json.Unmarshal(vpRes, &vp) == nil {
			if vp.Result.Value.Width > 0 {
				vw = vp.Result.Value.Width
			}
			if vp.Result.Value.Height > 0 {
				vh = vp.Result.Value.Height
			}
		}
	}

	var px, py float64
	if ratioX != nil && ratioY != nil {
		px = math.Round(*ratioX * vw)
		py = math.Round(*ratioY * vh)
	} else {
		if x != nil {
			px = math.Round(*x)
		}
		if y != nil {
			py = math.Round(*y)
		}
	}

	switch action {
	case "click":
		_, _ = client.Call(ctx, "Input.dispatchMouseEvent", map[string]any{"type": "mouseMoved", "x": px, "y": py})
		_, _ = client.Call(ctx, "Input.dispatchMouseEvent", map[string]any{"type": "mousePressed", "x": px, "y": py, "button": "left", "clickCount": 1})
		_, _ = client.Call(ctx, "Input.dispatchMouseEvent", map[string]any{"type": "mouseReleased", "x": px, "y": py, "button": "left", "clickCount": 1})
		time.Sleep(400 * time.Millisecond)
	case "wheel":
		dy := 100.0
		if deltaY != nil {
			dy = *deltaY
		}
		_, _ = client.Call(ctx, "Input.dispatchMouseEvent", map[string]any{"type": "mouseWheel", "x": px, "y": py, "deltaX": 0, "deltaY": dy})
		time.Sleep(150 * time.Millisecond)
	case "text":
		t := ""
		if text != nil {
			t = *text
		}
		_, _ = client.Call(ctx, "Input.insertText", map[string]any{"text": t})
		time.Sleep(100 * time.Millisecond)
	case "back":
		_, _ = client.Call(ctx, "Runtime.evaluate", map[string]any{"expression": "window.history.back()"})
		time.Sleep(500 * time.Millisecond)
	case "forward":
		_, _ = client.Call(ctx, "Runtime.evaluate", map[string]any{"expression": "window.history.forward()"})
		time.Sleep(500 * time.Millisecond)
	default:
		return nil, fmt.Errorf("未知交互动作：%s", action)
	}

	evalRes, _ := client.Call(ctx, "Runtime.evaluate", map[string]any{
		"expression":    "({url:location.href,title:document.title})",
		"returnByValue": true,
	})
	var evalData struct {
		Result struct {
			Value struct {
				URL   string `json:"url"`
				Title string `json:"title"`
			} `json:"value"`
		} `json:"result"`
	}
	_ = json.Unmarshal(evalRes, &evalData)
	pURL := evalData.Result.Value.URL
	pTitle := evalData.Result.Value.Title
	if pURL == "" {
		pURL = page.URL
	}
	if pTitle == "" {
		pTitle = page.Title
	}

	shotRes, err := client.Call(ctx, "Page.captureScreenshot", map[string]any{"format": "jpeg", "quality": 80})
	if err != nil {
		return nil, err
	}
	var shotData struct {
		Data string `json:"data"`
	}
	_ = json.Unmarshal(shotRes, &shotData)

	return m.RememberScreenshot(ctx, sessionKey, shotData.Data, pURL, pTitle, page.ID), nil
}

// ExtractPageContent extracts document title, URL, and body text.
func (m *SessionManager) ExtractPageContent(ctx context.Context, sessionKey string, targetID string) (*PageContent, error) {
	page, _, err := m.SessionPage(ctx, sessionKey, false, false, targetID)
	if err != nil {
		return nil, err
	}

	client, err := m.ConnectPage(ctx, page, sessionKey)
	if err != nil {
		return nil, err
	}
	defer client.Close()

	expr := `(() => {
		const title = document.title || '';
		const url = location.href;
		const text = (document.body ? document.body.innerText : '').slice(0, 5000);
		return { title, url, text };
	})()`

	res, err := client.Call(ctx, "Runtime.evaluate", map[string]any{
		"expression":    expr,
		"returnByValue": true,
	})
	if err != nil {
		return nil, err
	}

	var data struct {
		Result struct {
			Value struct {
				Title string `json:"title"`
				URL   string `json:"url"`
				Text  string `json:"text"`
			} `json:"value"`
		} `json:"result"`
	}
	_ = json.Unmarshal(res, &data)

	title := data.Result.Value.Title
	if title == "" {
		title = page.Title
	}
	pageURL := data.Result.Value.URL
	if pageURL == "" {
		pageURL = page.URL
	}

	return &PageContent{
		Title:      title,
		URL:        pageURL,
		Text:       data.Result.Value.Text,
		SessionKey: sessionKey,
		TargetID:   page.ID,
	}, nil
}

// Diagnostics returns diagnostics logs for a session tab.
func (m *SessionManager) Diagnostics(ctx context.Context, sessionKey string, targetID string) (*Diagnostics, error) {
	tid := targetID
	m.targetMu.Lock()
	if tid == "" {
		tid = m.sessionActive[sessionKey]
	}
	if tid != "" && !ownsTarget(m.sessionTargets[sessionKey], tid) {
		m.targetMu.Unlock()
		return nil, errors.New("指定的标签页不属于当前会话")
	}
	m.targetMu.Unlock()

	m.logsMu.RLock()
	logs := m.sessionLogs[sessionKey+"::"+tid]
	copied := make([]LogEntry, len(logs))
	copy(copied, logs)
	m.logsMu.RUnlock()

	return &Diagnostics{
		SessionKey: sessionKey,
		TargetID:   tid,
		Logs:       copied,
	}, nil
}

// CallCDP calls a CDP method on the session tab, checking allowed domains.
func (m *SessionManager) CallCDP(ctx context.Context, sessionKey string, method string, params map[string]any, targetID string) (map[string]any, error) {
	parts := strings.SplitN(method, ".", 2)
	if len(parts) != 2 || !allowedCDPDomains[parts[0]] {
		return nil, fmt.Errorf("不允许调用 CDP 方法：%s", method)
	}

	page, _, err := m.SessionPage(ctx, sessionKey, false, false, targetID)
	if err != nil {
		return nil, err
	}

	client, err := m.ConnectPage(ctx, page, sessionKey)
	if err != nil {
		return nil, err
	}
	defer client.Close()

	res, err := client.Call(ctx, method, params)
	if err != nil {
		return nil, err
	}

	var rawResult any
	_ = json.Unmarshal(res, &rawResult)

	return map[string]any{
		"session_key": sessionKey,
		"target_id":   page.ID,
		"method":      method,
		"result":      rawResult,
	}, nil
}
