package modelconfig

import (
	core "astrorder.dev/session-daemon/internal/runtime"
	"context"
	"errors"
	"sort"
)

func (s *Service) reload(ctx context.Context, request map[string]any) (map[string]any, error) {
	if (s.config.Busy == nil && s.config.BusyFor == nil) || s.config.Reload == nil {
		return nil, newProtocolErrorf("native reload and activity callbacks are required")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var agents []string
	switch raw := request["agents"].(type) {
	case []string:
		agents = raw
	case []any:
		for _, item := range raw {
			agent, ok := item.(string)
			if !ok {
				return nil, newProtocolErrorf("模型配置重载 Agent 无效")
			}
			agents = append(agents, agent)
		}
	default:
		return nil, newProtocolErrorf("模型配置重载 Agent 无效")
	}
	if len(agents) == 0 {
		return nil, newProtocolErrorf("模型配置重载 Agent 无效")
	}
	for _, agent := range agents {
		if agent != "codex" && agent != "grok" {
			return nil, newProtocolErrorf("模型配置重载 Agent 无效: %s", agent)
		}
	}
	s.pendingMu.Lock()
	defer s.pendingMu.Unlock()
	for _, agent := range agents {
		s.pendingReloads[agent] = true
	}
	return s.drainLocked(ctx)
}

// Drain retries pending requests without manufacturing a new reload request.
func (s *Service) Drain(ctx context.Context) (map[string]any, error) {
	s.pendingMu.Lock()
	defer s.pendingMu.Unlock()
	return s.drainLocked(ctx)
}
func (s *Service) drainLocked(ctx context.Context) (map[string]any, error) {
	reloaded := []string{}
	for _, agent := range s.sortedPending() {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		busy := false
		if s.config.BusyFor != nil {
			busy = s.config.BusyFor(agent)
		} else if s.config.Busy != nil {
			busy = s.config.Busy()
		} else {
			return nil, newProtocolErrorf("native activity callback is required")
		}
		if busy {
			continue
		}
		if s.config.Reload == nil {
			return nil, newProtocolErrorf("native reload callback is required")
		}
		if err := s.config.Reload(ctx, agent); err != nil {
			if errors.Is(err, core.ErrRuntimeBusy) {
				continue
			}
			return nil, newProtocolErrorf("reload agent %s: %v", agent, err)
		}
		delete(s.pendingReloads, agent)
		reloaded = append(reloaded, agent)
	}
	pending := s.sortedPending()
	status := "success"
	if len(pending) > 0 {
		status = "deferred"
	}
	return map[string]any{"reloaded": reloaded, "pending": pending, "deferred": len(pending) > 0, "status": status}, nil
}

func (s *Service) sortedPending() []string {
	res := make([]string, 0, len(s.pendingReloads))
	for a := range s.pendingReloads {
		res = append(res, a)
	}
	sort.Strings(res)
	return res
}
