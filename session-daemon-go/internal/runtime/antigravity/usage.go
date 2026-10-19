package antigravity

import (
	"fmt"
	"math"
)

// Stream results contain cumulative counters for this native process. Publish
// snapshots, never sum them or treat absent metrics as measured zero.
func (a *Adapter) emitUsage(s *session, p map[string]any) error {
	usage, ok := p["usage"].(map[string]any)
	if !ok {
		return nil
	}
	payload := map[string]any{"agent_id": a.config.AgentID, "session_id": s.id, "provider": "antigravity", "context_available": false}
	s.mu.Lock()
	payload["model"] = s.model
	s.mu.Unlock()
	for src, dst := range map[string]string{"input_tokens": "input_tokens", "output_tokens": "output_tokens", "thinking_tokens": "reasoning_tokens", "cache_read_tokens": "cached_tokens", "total_tokens": "total_tokens"} {
		raw, exists := usage[src]
		if !exists {
			continue
		}
		n, ok := raw.(float64)
		if !ok || n < 0 || n >= float64(math.MaxInt64) || n != math.Trunc(n) {
			return fmt.Errorf("invalid Antigravity usage counter %s", src)
		}
		payload[dst] = int64(n)
	}
	if len(payload) == 5 {
		return nil
	}
	return a.emit(s.id, "token.usage", payload, "")
}
