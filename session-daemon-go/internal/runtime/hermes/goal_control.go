package hermes

import (
	core "astrorder.dev/session-daemon/internal/runtime"
	"context"
	"fmt"
	"strings"
)

func (a *Adapter) handleGoal(ctx context.Context, session *SessionState, req core.Request) (core.Result, error) {
	objective, _ := req.Fields["objective"].(string)
	setting := req.Action == "session.goal.set"
	state, _ := req.Fields["status"].(string)
	if state != "" && state != "paused" && state != "active" {
		return core.Result{}, fmt.Errorf("goal status is invalid")
	}
	editing := setting && state == ""
	resuming := setting && state == "active"
	if editing && (strings.TrimSpace(objective) == "" || len(objective) > 65536) {
		return core.Result{}, fmt.Errorf("goal objective is invalid")
	}
	a.mu.Lock()
	if (editing || resuming) && (session.sending || session.Status == "running" || session.Status == "waiting_approval") {
		a.mu.Unlock()
		return core.Result{}, fmt.Errorf("请先停止当前执行，再修改 Hermes 目标")
	}
	if editing || resuming {
		session.sending = true
	}
	a.mu.Unlock()
	if editing || resuming {
		defer func() { a.mu.Lock(); session.sending = false; a.mu.Unlock() }()
	}
	handle, err := a.ensureHandle(ctx, session)
	if err != nil {
		return core.Result{}, err
	}
	if setting {
		before, err := a.rpc(ctx, "session.control.read", map[string]any{"session_id": handle})
		if err != nil {
			return core.Result{}, err
		}
		control, _ := before["control"].(map[string]any)
		if _, ok := control["goal"].(map[string]any); !ok {
			return core.Result{}, fmt.Errorf("native goal no longer exists")
		}
	}
	var dispatch map[string]any
	if editing {
		dispatch, err = a.rpc(ctx, "command.dispatch", map[string]any{"session_id": handle, "name": "goal", "arg": "-- " + strings.TrimSpace(objective)})
	} else {
		action := "goal.clear"
		if setting {
			action = "goal.pause"
			if resuming {
				action = "goal.resume"
			}
		}
		var result map[string]any
		result, err = a.rpc(ctx, "session.control", map[string]any{"session_id": handle, "action": action})
		dispatch, _ = result["dispatch"].(map[string]any)
	}
	if err != nil {
		return core.Result{}, err
	}
	verified, err := a.rpc(ctx, "session.control.read", map[string]any{"session_id": handle})
	if err != nil {
		return core.Result{}, fmt.Errorf("goal changed but readback failed: %w", err)
	}
	control, ok := verified["control"].(map[string]any)
	if !ok {
		return core.Result{}, fmt.Errorf("native control readback missing")
	}
	goal, present := control["goal"]
	if !present {
		return core.Result{}, fmt.Errorf("native goal readback missing")
	}
	if setting {
		nativeGoal, ok := goal.(map[string]any)
		if !ok || (state != "" && nativeGoal["status"] != state) {
			return core.Result{}, fmt.Errorf("native goal update was not confirmed")
		}
	}
	if editing || resuming {
		prompt, _ := dispatch["message"].(string)
		if dispatch["type"] != "send" || prompt == "" {
			return core.Result{}, fmt.Errorf("native goal kickoff was not returned")
		}
		a.mu.Lock()
		session.Status = "running"
		session.CommandID = ""
		a.mu.Unlock()
		if a.config.Emit != nil {
			if err := a.config.Emit(session.SessionID, "session.active", map[string]any{"agent_id": a.config.AgentID, "session_id": session.SessionID}, "running"); err != nil {
				return core.Result{}, err
			}
		}
		if _, err := a.rpc(ctx, "prompt.submit", map[string]any{"session_id": handle, "text": prompt, "surface": "hud"}); err != nil {
			a.mu.Lock()
			session.Status = "error"
			a.mu.Unlock()
			return core.Result{}, fmt.Errorf("goal updated but kickoff failed: %w", err)
		}
	} else if !setting && goal != nil {
		return core.Result{}, fmt.Errorf("native goal clear was not confirmed")
	}
	a.handleGatewayEvent(map[string]any{"type": "session.control.update", "session_id": handle, "payload": map[string]any{"control": control}})
	a.mu.RLock()
	current := session.Status
	a.mu.RUnlock()
	return core.Result{SessionID: session.SessionID, Status: current, Payload: map[string]any{"accepted": true, "goal": goal}}, nil
}
