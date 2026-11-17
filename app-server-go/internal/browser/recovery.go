package browser

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"
)

const (
	recoveryTimeout      = 3 * time.Second
	recoveryProbeTimeout = 1500 * time.Millisecond
	recoveryWorkers      = 8
)

type recoveredTarget struct {
	id         string
	sessionKey string
	err        error
}

// sessionPages must be called while the session's page lock is held.
func (m *SessionManager) sessionPages(ctx context.Context, sessionKey, targetID string) ([]PageTarget, error) {
	m.targetMu.Lock()
	observed := make(map[string]bool, len(m.sessionTargets[sessionKey]))
	for _, id := range m.sessionTargets[sessionKey] {
		observed[id] = true
	}
	m.targetMu.Unlock()

	pages, err := m.endpoints.ListPages(ctx)
	if err != nil {
		return nil, fmt.Errorf("读取浏览器标签页失败: %w", err)
	}
	if err := m.recoverSessionTargets(ctx, sessionKey, targetID, pages, observed); err != nil {
		return nil, err
	}
	return pages, nil
}

// Recovery only updates this session's ownership and never performs CDP I/O
// while holding the lock shared by all sessions.
func (m *SessionManager) recoverSessionTargets(ctx context.Context, sessionKey, targetID string, pages []PageTarget, observed map[string]bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	pageIDs := make(map[string]bool, len(pages))
	for _, page := range pages {
		pageIDs[page.ID] = true
	}

	m.targetMu.Lock()
	var kept []string
	for _, id := range m.sessionTargets[sessionKey] {
		// A target added after discovery started was not part of the snapshot.
		if pageIDs[id] || !observed[id] {
			kept = append(kept, id)
		}
	}
	if len(kept) == 0 {
		delete(m.sessionTargets, sessionKey)
		delete(m.sessionActive, sessionKey)
	} else {
		m.sessionTargets[sessionKey] = kept
		if !ownsTarget(kept, m.sessionActive[sessionKey]) {
			m.sessionActive[sessionKey] = kept[len(kept)-1]
		}
	}
	available := false
	for _, id := range kept {
		if pageIDs[id] && (targetID == "" || targetID == id) {
			available = true
			break
		}
	}
	known := make(map[string]bool)
	for _, targets := range m.sessionTargets {
		for _, id := range targets {
			known[id] = true
		}
	}
	m.targetMu.Unlock()

	if available {
		return nil
	}

	var candidates []PageTarget
	var probeErr error
	for _, page := range pages {
		if known[page.ID] {
			continue
		}
		if page.WebSocketDebuggerURL == "" {
			probeErr = errors.New("浏览器标签页缺少调试连接")
			continue
		}
		candidates = append(candidates, page)
	}
	if len(candidates) == 0 {
		if probeErr != nil {
			return fmt.Errorf("读取浏览器标签页会话失败: %w", probeErr)
		}
		return nil
	}

	scanCtx, cancel := context.WithTimeout(ctx, recoveryTimeout)
	defer cancel()
	jobs := make(chan PageTarget, len(candidates))
	results := make(chan recoveredTarget, len(candidates))
	for _, page := range candidates {
		jobs <- page
	}
	close(jobs)
	var workers sync.WaitGroup
	count := min(recoveryWorkers, len(candidates))
	for i := 0; i < count; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for page := range jobs {
				probeCtx, stop := context.WithTimeout(scanCtx, recoveryProbeTimeout)
				key, err := probeSessionMarker(probeCtx, page)
				stop()
				results <- recoveredTarget{id: page.ID, sessionKey: key, err: err}
			}
		}()
	}
	workers.Wait()
	close(results)
	if err := ctx.Err(); err != nil {
		return err
	}

	matches := make(map[string]bool)
	for result := range results {
		if result.err != nil && probeErr == nil {
			probeErr = result.err
		}
		if result.sessionKey == sessionKey {
			matches[result.id] = true
		}
	}

	m.targetMu.Lock()
	for _, page := range pages {
		id := page.ID
		if !matches[id] {
			continue
		}
		claimed := false
		for owner, targets := range m.sessionTargets {
			if owner != sessionKey && ownsTarget(targets, id) {
				claimed = true
				break
			}
		}
		if claimed {
			continue
		}
		if !ownsTarget(m.sessionTargets[sessionKey], id) {
			m.sessionTargets[sessionKey] = append(m.sessionTargets[sessionKey], id)
		}
		if m.sessionActive[sessionKey] == "" {
			m.sessionActive[sessionKey] = id
		}
		if targetID == "" || targetID == id {
			available = true
		}
	}
	m.targetMu.Unlock()

	if !available && probeErr != nil {
		return fmt.Errorf("读取浏览器标签页会话失败: %w", probeErr)
	}
	return nil
}

func probeSessionMarker(ctx context.Context, page PageTarget) (string, error) {
	client, err := DialCDP(ctx, page.WebSocketDebuggerURL)
	if err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return "", errors.New("连接浏览器标签页失败")
	}
	defer client.CloseNow()
	raw, err := client.Call(ctx, "Runtime.evaluate", map[string]any{
		"expression":    "window.name",
		"returnByValue": true,
	})
	if err != nil {
		return "", err
	}
	var result struct {
		Result struct {
			Value any `json:"value"`
		} `json:"result"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return "", fmt.Errorf("解析浏览器标签页会话失败: %w", err)
	}
	value, _ := result.Result.Value.(string)
	key, _ := MarkerSessionKey(value)
	return key, nil
}
