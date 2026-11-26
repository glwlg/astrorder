package codex

import (
	"context"
	"fmt"
	"strings"

	core "astrorder.dev/session-daemon/internal/runtime"
)

func threadHasActiveWork(thread map[string]any, sid string) bool {
	if thread == nil || thread["id"] != sid {
		return true
	}
	turns, ok := thread["turns"].([]any)
	if !ok {
		return false
	}
	for _, raw := range turns {
		turn, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if turn["status"] == "inProgress" {
			return true
		}
		items, ok := turn["items"].([]any)
		if !ok {
			continue
		}
		for _, rawItem := range items {
			item, ok := rawItem.(map[string]any)
			if !ok {
				continue
			}
			if item["status"] == "inProgress" || item["status"] == "running" || item["status"] == "pending" {
				return true
			}
		}
	}
	return false
}

func (a *Adapter) confirmMoveIdle(ctx context.Context, rt *Runtime, sid string) error {
	rt.mu.Lock()
	done, confirmed := rt.processDone, rt.exitConfirmed
	rt.mu.Unlock()
	if done != nil && confirmed {
		select {
		case <-done:
			return nil
		default:
		}
	}
	if rt.Status() != "idle" {
		return core.RejectBeforeDispatch("Codex session must be idle before moving workspace")
	}
	response, err := rt.request(ctx, "thread/read", map[string]any{"threadId": sid, "includeTurns": true})
	if err != nil {
		return fmt.Errorf("confirm idle work: %w", err)
	}
	thread, _ := response["thread"].(map[string]any)
	if threadHasActiveWork(thread, sid) {
		return core.RejectBeforeDispatch("Codex session has active native work")
	}
	return nil
}

func (a *Adapter) moveSessionWorkspace(ctx context.Context, s *adapterSession, rt *Runtime, sessionID string, fields map[string]any) (core.Result, error) {
	rawWorkspace, _ := fields["workspace"].(string)
	if strings.TrimSpace(rawWorkspace) == "" {
		rawWorkspace, _ = fields["cwd"].(string)
	}
	if strings.TrimSpace(rawWorkspace) == "" {
		return core.Result{}, core.RejectBeforeDispatch("Codex target workspace is required")
	}
	targetCwd, err := rt.workspace(rawWorkspace)
	if err != nil {
		return core.Result{}, core.RejectBeforeDispatch(err.Error())
	}

	s.mu.Lock()
	if s.control {
		s.mu.Unlock()
		return core.Result{}, core.RejectBeforeDispatch("Codex control session is not a native chat session")
	}
	if s.mutating || s.inputUnconfirmed || rt.Status() != "idle" {
		s.mu.Unlock()
		return core.Result{}, core.RejectBeforeDispatch("Codex session must be idle before moving workspace")
	}
	s.mutating = true
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.mutating = false
		s.mu.Unlock()
	}()

	if err := a.confirmMoveIdle(ctx, rt, sessionID); err != nil {
		return core.Result{}, err
	}

	if err := rt.closeIdleTransport(ctx); err != nil {
		return core.Result{}, err
	}
	rt.mu.Lock()
	done, remote := rt.processDone, rt.sshSession != nil
	rt.mu.Unlock()
	if done != nil {
		select {
		case <-done:
		case <-ctx.Done():
			return core.Result{}, ctx.Err()
		}
		if remote {
			rt.mu.Lock()
			confirmed := rt.exitConfirmed
			rt.mu.Unlock()
			if !confirmed {
				return core.Result{}, fmt.Errorf("Codex remote exit was not confirmed")
			}
		}
	}
	select {
	case <-rt.eventDone:
	case <-ctx.Done():
		return core.Result{}, ctx.Err()
	}

	a.mu.Lock()
	if a.sessions[sessionID] == s {
		delete(a.sessions, sessionID)
	}
	a.mu.Unlock()

	verifyCfg := a.config
	verifyCfg.OnNotification = nil
	verifyCfg.OnNotifications = nil
	var verifyRT *Runtime
	if a.starter != nil {
		verifyRT = NewWithSSHStarter(verifyCfg, a.starter)
	} else {
		verifyRT = New(verifyCfg)
	}
	defer verifyRT.Close()

	if _, err := verifyRT.Resume(ctx, Request{Cwd: targetCwd, SessionID: sessionID}); err != nil {
		return core.Result{}, fmt.Errorf("Codex failed to verify target workspace: %w", err)
	}
	verifyRT.Close()

	return core.Result{
		SessionID: sessionID,
		Status:    "idle",
		Payload: map[string]any{
			"workspace": targetCwd,
			"moved":     true,
			"closed":    true,
		},
	}, nil
}
