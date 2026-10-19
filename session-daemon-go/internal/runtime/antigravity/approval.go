package antigravity

import (
	core "astrorder.dev/session-daemon/internal/runtime"
	"context"
	"fmt"
)

func approvalMode(permission string) string {
	switch permission {
	case "request-review", "strict":
		return "manual"
	case "always-proceed":
		return "full_access"
	default:
		return ""
	}
}

func (a *Adapter) readApproval(s *session) (core.Result, error) {
	if s.transportExited() {
		return core.Result{}, core.ErrSessionNotOwned
	}
	result := a.result(s)
	s.mu.Lock()
	permission := s.permission
	unsupported := s.control || s.readonly || s.closed
	s.mu.Unlock()
	if unsupported || approvalMode(permission) == "" {
		return core.Result{}, fmt.Errorf("Antigravity native permission mode is unavailable: %q", permission)
	}
	result.Payload["mode"] = approvalMode(permission)
	return result, nil
}

func (a *Adapter) switchApproval(ctx context.Context, s *session, p map[string]any) (core.Result, error) {
	mode := str(p, "mode")
	if mode != "manual" && mode != "full_access" {
		return core.Result{}, core.RejectBeforeDispatch("Antigravity supports native rules or full access; automatic review is unavailable")
	}
	current, err := a.readApproval(s)
	if err != nil {
		return core.Result{}, core.RejectBeforeDispatch(err.Error())
	}
	s.mu.Lock()
	if s.control || s.readonly || s.closed || s.status != "idle" {
		s.mu.Unlock()
		return core.Result{}, core.RejectBeforeDispatch("Antigravity permission switch requires an idle conversation")
	}
	if current.Payload["mode"] == mode {
		s.mu.Unlock()
		current.Payload["accepted"] = true
		return current, nil
	}
	cwd, model, effort := s.cwd, s.model, s.effort
	s.status = "running"
	s.closed = true
	s.mu.Unlock()
	if err = s.owner.Close(); err != nil {
		a.fail(s, "Antigravity permission switch could not close transport")
		return core.Result{}, err
	}
	select {
	case <-s.done:
	case <-ctx.Done():
		a.fail(s, "Antigravity permission switch transport exit unconfirmed")
		return core.Result{}, ctx.Err()
	}
	permission := "request-review"
	if mode == "full_access" {
		permission = "always-proceed"
	}
	result, err := a.openReplacing(ctx, s.id, map[string]any{"cwd": cwd, "model": model, "effort": effort, "permission_mode": permission}, s)
	if err != nil {
		a.fail(s, "Antigravity permission switch failed native readback")
		return core.Result{}, err
	}
	result.Payload["accepted"] = true
	return result, nil
}
