package antigravity

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"time"
)

func int64FromAny(v any) (int64, bool) {
	switch n := v.(type) {
	case int64:
		return n, true
	case int:
		return int64(n), true
	case float64:
		if n >= 0 && n < float64(math.MaxInt64) && n == math.Trunc(n) {
			return int64(n), true
		}
	case json.Number:
		if i, err := n.Int64(); err == nil && i >= 0 {
			return i, true
		}
	}
	return 0, false
}

func (s *session) cacheUsageMetrics(payload map[string]any) {
	if s == nil || payload == nil {
		return
	}
	inTok, hasIn := int64FromAny(payload["input_tokens"])
	outTok, hasOut := int64FromAny(payload["output_tokens"])
	cacheTok, _ := int64FromAny(payload["cached_tokens"])
	reasonTok, _ := int64FromAny(payload["reasoning_tokens"])
	totTok, hasTot := int64FromAny(payload["total_tokens"])
	lastIn, _ := int64FromAny(payload["last_input_tokens"])
	ctxWin, _ := int64FromAny(payload["context_window"])

	s.mu.Lock()
	defer s.mu.Unlock()
	if (hasTot || hasIn || hasOut) && (totTok >= s.cumTotalTokens || !s.hasCumMetrics) {
		s.hasCumMetrics = true
		s.cumInputTokens = inTok
		s.cumOutputTokens = outTok
		s.cumCachedTokens = cacheTok
		s.cumReasonTokens = reasonTok
		s.cumTotalTokens = totTok
	}
	if lastIn > 0 && s.lastUsedTokens == 0 {
		s.lastUsedTokens = lastIn
	}
	if ctxWin > 0 && s.contextWindow == 0 {
		s.contextWindow = ctxWin
	}
}

// emitUsage publishes turn token and context-window snapshots combining ACP
// usage_update/prompt results with native conversation SQLite metadata.
func (a *Adapter) emitUsage(s *session, p map[string]any) error {
	usage, hasStreamUsage := p["usage"].(map[string]any)
	s.mu.Lock()
	model := s.model
	readonly := s.readonly
	acpUsed := s.lastUsedTokens
	acpWin := s.contextWindow
	s.mu.Unlock()

	payload := map[string]any{
		"agent_id":          a.config.AgentID,
		"session_id":        s.id,
		"provider":          "antigravity",
		"model":             model,
		"context_available": false,
	}

	hasMetric := false
	hasCumulative := false
	if hasStreamUsage {
		for src, dst := range map[string]string{
			"input_tokens":      "input_tokens",
			"inputTokens":       "input_tokens",
			"output_tokens":     "output_tokens",
			"outputTokens":      "output_tokens",
			"thinking_tokens":   "reasoning_tokens",
			"thoughtTokens":     "reasoning_tokens",
			"cache_read_tokens": "cached_tokens",
			"cachedReadTokens":  "cached_tokens",
			"total_tokens":      "total_tokens",
			"totalTokens":       "total_tokens",
		} {
			raw, exists := usage[src]
			if !exists {
				continue
			}
			n, ok := int64FromAny(raw)
			if !ok {
				return fmt.Errorf("invalid Antigravity usage counter %s", src)
			}
			payload[dst] = n
			hasMetric = true
			hasCumulative = true
		}
	}

	if acpUsed > 0 || acpWin > 0 {
		payload["context_available"] = true
		payload["source"] = "acp_usage_update"
		if acpUsed > 0 {
			payload["last_input_tokens"] = acpUsed
		}
		if acpWin > 0 {
			payload["context_window"] = acpWin
		}
		hasMetric = true
	}

	var native map[string]any
	if validID(s.id) && !s.control {
		timeout := 5 * time.Second
		if a.sshStarter != nil {
			timeout = 12 * time.Second
		}
		nCtx, cancel := context.WithTimeout(context.Background(), timeout)
		if res, err := a.readSessionUsage(nCtx, s.id, readonly); err == nil {
			native = res
		}
		cancel()
	}

	if native != nil {
		if nm := strings.TrimSpace(str(native, "model")); nm != "" && model == "" {
			model = nm
			s.mu.Lock()
			if s.model == "" {
				s.model = nm
			}
			s.mu.Unlock()
			payload["model"] = model
		} else if dm := strings.TrimSpace(str(native, "default_model")); dm != "" && model == "" {
			model = dm
			s.mu.Lock()
			if s.model == "" {
				s.model = dm
			}
			s.mu.Unlock()
			payload["model"] = model
		}

		if native["context_available"] == true {
			payload["context_available"] = true
			if payload["source"] == nil {
				payload["source"] = "native_last_request"
			}
			if lastIn, ok := int64FromAny(native["last_input_tokens"]); ok && acpUsed == 0 {
				payload["last_input_tokens"] = lastIn
			}
			if ctxWin, ok := int64FromAny(native["context_window"]); ok && acpWin == 0 {
				payload["context_window"] = ctxWin
			}
			for _, key := range []string{"input_tokens", "output_tokens", "cached_tokens", "reasoning_tokens", "total_tokens"} {
				if nv, ok := int64FromAny(native[key]); ok && nv > 0 {
					cur, _ := int64FromAny(payload[key])
					if nv >= cur || key == "input_tokens" {
						payload[key] = nv
					}
					hasMetric = true
					hasCumulative = true
				}
			}
			hasMetric = true
		}
	}

	if !hasMetric {
		return nil
	}
	if !hasCumulative {
		s.mu.Lock()
		if s.hasCumMetrics {
			payload["input_tokens"] = s.cumInputTokens
			payload["output_tokens"] = s.cumOutputTokens
			payload["cached_tokens"] = s.cumCachedTokens
			payload["reasoning_tokens"] = s.cumReasonTokens
			payload["total_tokens"] = s.cumTotalTokens
		}
		s.mu.Unlock()
	} else {
		s.cacheUsageMetrics(payload)
	}
	if payload["model"] == "" {
		payload["model"] = "default"
	}
	if a.config.ConnectionID != "" {
		payload["connection_id"] = a.config.ConnectionID
	}
	return a.emit(s.id, "token.usage", payload, "")
}

func (a *Adapter) emitSessionUsageSnapshot(ctx context.Context, s *session) {
	if s == nil || s.control || !validID(s.id) {
		return
	}
	s.mu.Lock()
	model := s.model
	readonly := s.readonly
	acpUsed := s.lastUsedTokens
	acpWin := s.contextWindow
	s.mu.Unlock()

	native, err := a.readSessionUsage(ctx, s.id, readonly)
	if (err != nil || native == nil) && acpUsed == 0 && acpWin == 0 {
		return
	}

	if native != nil {
		if nm := strings.TrimSpace(str(native, "model")); nm != "" && model == "" {
			model = nm
		} else if dm := strings.TrimSpace(str(native, "default_model")); dm != "" && model == "" {
			model = dm
		}
	}
	if model != "" {
		s.mu.Lock()
		if s.model == "" {
			s.model = model
		} else {
			model = s.model
		}
		s.mu.Unlock()
	}

	var total int64
	if native != nil {
		total, _ = int64FromAny(native["total_tokens"])
	}
	ctxAvail := (native != nil && native["context_available"] == true) || acpUsed > 0 || acpWin > 0
	if !ctxAvail && total <= 0 {
		return
	}

	if model == "" {
		model = "default"
	}
	payload := map[string]any{
		"agent_id":          a.config.AgentID,
		"session_id":        s.id,
		"provider":          "antigravity",
		"model":             model,
		"context_available": ctxAvail,
		"source":            "native_last_request",
		"last_input_tokens": int64(0),
	}
	if native != nil {
		for _, key := range []string{"last_input_tokens", "context_window", "input_tokens", "output_tokens", "cached_tokens", "reasoning_tokens", "total_tokens"} {
			if v, ok := int64FromAny(native[key]); ok {
				payload[key] = v
			}
		}
	}
	if acpUsed > 0 {
		payload["last_input_tokens"] = acpUsed
		payload["source"] = "acp_usage_update"
	}
	if acpWin > 0 {
		payload["context_window"] = acpWin
		payload["source"] = "acp_usage_update"
	}
	s.cacheUsageMetrics(payload)
	if a.config.ConnectionID != "" {
		payload["connection_id"] = a.config.ConnectionID
	}
	_ = a.emit(s.id, "token.usage", payload, "")
}
