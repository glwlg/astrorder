package antigravity

import (
	core "astrorder.dev/session-daemon/internal/runtime"
	"context"
	"fmt"
)

// switchModel replaces only an idle CLI transport, preserving native identity.
func (a *Adapter) switchModel(ctx context.Context, s *session, p map[string]any) (core.Result, error) {
	model, provider := str(p, "model"), str(p, "provider")
	if model == "" || provider != "" && provider != "antigravity" {
		return core.Result{}, core.RejectBeforeDispatch("invalid Antigravity model selection")
	}
	items, err := a.models(ctx)
	if err != nil {
		return core.Result{}, core.RejectBeforeDispatch(err.Error())
	}
	found := false
	for _, item := range items {
		if item["id"] == model {
			found = true
			break
		}
	}
	if !found {
		return core.Result{}, core.RejectBeforeDispatch("unknown Antigravity model")
	}
	s.mu.Lock()
	if s.control || s.readonly || s.closed || s.status != "idle" {
		s.mu.Unlock()
		return core.Result{}, core.RejectBeforeDispatch("Antigravity model switch requires an idle conversation")
	}
	cwd, effort, permission := s.cwd, s.effort, s.permission
	s.status = "running"
	s.closed = true
	s.mu.Unlock()
	if err = s.owner.Close(); err != nil {
		a.fail(s, "Antigravity model switch could not close transport")
		return core.Result{}, err
	}
	select {
	case <-s.done:
	case <-ctx.Done():
		a.fail(s, "Antigravity model switch transport exit unconfirmed")
		return core.Result{}, ctx.Err()
	}
	result, err := a.openReplacing(ctx, s.id, map[string]any{"cwd": cwd, "model": model, "effort": effort, "permission_mode": permission}, s)
	if err != nil {
		a.fail(s, "Antigravity model switch failed to confirm transport")
		return core.Result{}, fmt.Errorf("Antigravity model switch failed: %w", err)
	}
	result.Payload["provider"] = "antigravity"
	result.Payload["accepted"] = true
	return result, nil
}
