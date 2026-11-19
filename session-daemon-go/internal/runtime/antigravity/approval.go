package antigravity

import (
	"context"
	"fmt"
	"sort"

	core "astrorder.dev/session-daemon/internal/runtime"
)

func fromACPMode(acpMode string) string {
	switch acpMode {
	case "default", "plan":
		return "manual"
	case "auto", "accept-edits":
		return "auto"
	default:
		return ""
	}
}

func toACPMode(mode string) string {
	switch mode {
	case "manual":
		return "default"
	case "auto", "full_access":
		return "auto"
	default:
		return ""
	}
}

func parseRequestedApprovalMode(p map[string]any) (string, error) {
	if explicit := str(p, "mode"); explicit != "" {
		if explicit != "manual" && explicit != "auto" && explicit != "full_access" {
			return "", core.RejectBeforeDispatch("Antigravity approval mode is invalid")
		}
		return explicit, nil
	}
	if perm := str(p, "permission_mode"); perm != "" {
		switch perm {
		case "request-review", "strict", "manual", "default":
			return "manual", nil
		case "auto", "accept-edits":
			return "auto", nil
		case "always-proceed", "full_access":
			return "full_access", nil
		default:
			return "", core.RejectBeforeDispatch("Antigravity permission mode is invalid")
		}
	}
	return "", nil
}

func (a *Adapter) applyModeOnOpen(ctx context.Context, s *session, c *acpClient, mode string) error {
	acpMode := toACPMode(mode)
	if acpMode == "" {
		return core.RejectBeforeDispatch("Antigravity approval mode is invalid")
	}
	if _, err := c.call(ctx, "session/set_mode", map[string]any{
		"sessionId": s.id,
		"modeId":    acpMode,
	}); err != nil {
		return mapRPCErrorToPreDispatch(err)
	}
	s.mu.Lock()
	s.mode = mode
	s.mu.Unlock()
	return nil
}

func (a *Adapter) readApproval(s *session) (core.Result, error) {
	if s.deleteOnly || s.transportExited() {
		return core.Result{}, core.ErrSessionNotOwned
	}
	result := a.result(s)
	s.mu.Lock()
	mode := s.mode
	unsupported := s.control || s.readonly || s.closed
	pending := make([]map[string]any, 0, len(s.approvals))
	for id, p := range s.approvals {
		pending = append(pending, map[string]any{
			"approval_id": id,
			"method":      "session/request_permission",
			"title":       p.title,
		})
	}
	s.mu.Unlock()
	if unsupported || (mode != "manual" && mode != "auto" && mode != "full_access") {
		return core.Result{}, fmt.Errorf("Antigravity native approval mode is unavailable: %q", mode)
	}
	sort.Slice(pending, func(i, j int) bool {
		return fmt.Sprint(pending[i]["approval_id"]) < fmt.Sprint(pending[j]["approval_id"])
	})
	result.Payload["mode"] = mode
	result.Payload["pending"] = pending
	return result, nil
}

func (a *Adapter) switchApproval(ctx context.Context, s *session, p map[string]any) (core.Result, error) {
	mode := str(p, "mode")
	if mode != "manual" && mode != "auto" && mode != "full_access" {
		return core.Result{}, core.RejectBeforeDispatch("Antigravity approval mode must be manual, auto, or full_access")
	}
	if _, err := a.readApproval(s); err != nil {
		return core.Result{}, core.RejectBeforeDispatch(err.Error())
	}

	s.mu.Lock()
	if s.control || s.readonly || s.deleteOnly || s.closed || s.status != "idle" || s.promptRunning {
		s.mu.Unlock()
		return core.Result{}, core.RejectBeforeDispatch("Antigravity permission switch requires an idle conversation")
	}
	c := s.client
	curMode := s.mode
	s.mu.Unlock()

	if c == nil || c.exited() {
		return core.Result{}, core.ErrSessionNotOwned
	}
	if curMode == mode {
		res := a.result(s)
		res.Payload["mode"] = mode
		res.Payload["accepted"] = true
		return res, nil
	}

	acpMode := toACPMode(mode)
	if _, err := c.call(ctx, "session/set_mode", map[string]any{
		"sessionId": s.id,
		"modeId":    acpMode,
	}); err != nil {
		return core.Result{}, mapRPCErrorToPreDispatch(err)
	}

	s.mu.Lock()
	s.mode = mode
	s.mu.Unlock()

	result := a.result(s)
	result.Payload["mode"] = mode
	result.Payload["accepted"] = true
	return result, nil
}

func (a *Adapter) respondApproval(s *session, p map[string]any) (core.Result, error) {
	approvalID := str(p, "approval_id")
	if approvalID == "" {
		return core.Result{}, core.RejectBeforeDispatch("Antigravity approval_id is required")
	}
	decision := str(p, "decision")
	choice := str(p, "choice")
	cancelled, _ := p["cancelled"].(bool)

	s.mu.Lock()
	if s.control || s.readonly || s.deleteOnly || s.closed {
		s.mu.Unlock()
		return core.Result{}, core.RejectBeforeDispatch("Antigravity session cannot respond to approvals")
	}
	c := s.client
	appr := s.approvals[approvalID]
	if appr == nil {
		s.mu.Unlock()
		return core.Result{}, core.RejectBeforeDispatch("Antigravity approval request is not pending")
	}
	if c == nil || c.exited() {
		s.mu.Unlock()
		return core.Result{}, core.ErrSessionNotOwned
	}
	delete(s.approvals, approvalID)
	if len(s.approvals) == 0 && s.promptRunning {
		s.status = "running"
	} else if len(s.approvals) == 0 && !s.promptRunning {
		s.status = "idle"
	}
	nextStatus := s.status
	s.mu.Unlock()

	var rpcOutcome map[string]any
	resolvedState := "rejected"
	if cancelled {
		rpcOutcome = map[string]any{"outcome": "cancelled"}
	} else if decision == "accept" {
		optID := pickAllowOptionID(appr.options, choice == "session")
		rpcOutcome = map[string]any{
			"outcome":  "selected",
			"optionId": optID,
		}
		resolvedState = "approved"
	} else {
		rejOpt := pickRejectOptionID(appr.options)
		if rejOpt != "" {
			rpcOutcome = map[string]any{
				"outcome":  "selected",
				"optionId": rejOpt,
			}
		} else {
			rpcOutcome = map[string]any{"outcome": "cancelled"}
		}
	}

	if err := c.respond(appr.rawID, map[string]any{"outcome": rpcOutcome}); err != nil {
		return core.Result{}, err
	}

	_ = a.event(s, "approval.upsert", map[string]any{
		"id":         approvalID,
		"agent_id":   a.config.AgentID,
		"session_id": s.id,
		"state":      resolvedState,
		"native_id":  appr.rawID,
	}, nextStatus)
	_ = a.announce(s)

	res := a.result(s)
	res.Payload["accepted"] = true
	return res, nil
}
