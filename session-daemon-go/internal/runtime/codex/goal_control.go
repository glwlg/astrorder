package codex

import (
	core "astrorder.dev/session-daemon/internal/runtime"
	"context"
	"fmt"
	"strings"
)

func (a *Adapter) handleGoal(ctx context.Context, s *adapterSession, r core.Request) (core.Result, error) {
	rt := s.runtime
	params := map[string]any{"threadId": r.SessionID}
	method := "thread/goal/clear"
	status, _ := r.Fields["status"].(string)
	objective, _ := r.Fields["objective"].(string)
	if r.Action == "session.goal.set" {
		if status != "" && status != "paused" && status != "active" {
			return core.Result{}, fmt.Errorf("goal status is invalid")
		}
		if status == "" && (strings.TrimSpace(objective) == "" || len(objective) > 65536) {
			return core.Result{}, fmt.Errorf("goal objective is invalid")
		}
		before, err := rt.request(ctx, "thread/goal/get", params)
		if err != nil {
			return core.Result{}, err
		}
		goal, ok := before["goal"].(map[string]any)
		if !ok || goal["threadId"] != r.SessionID {
			return core.Result{}, fmt.Errorf("native goal no longer exists")
		}
		params["objective"] = objective
		params["status"] = goal["status"]
		if status != "" {
			params["objective"] = goal["objective"]
			params["status"] = status
			objective, _ = goal["objective"].(string)
		}
		params["origin"] = "user"
		params["tokenBudget"] = goal["tokenBudget"]
		method = "thread/goal/set"
	}
	if _, err := rt.request(ctx, method, params); err != nil {
		return core.Result{}, err
	}
	verified, err := rt.request(ctx, "thread/goal/get", map[string]any{"threadId": r.SessionID})
	if err != nil {
		return core.Result{}, fmt.Errorf("goal changed but readback failed: %w", err)
	}
	raw, present := verified["goal"]
	if !present {
		return core.Result{}, fmt.Errorf("native goal readback omitted goal")
	}
	eventMethod := "thread/goal/cleared"
	eventParams := map[string]any{"threadId": r.SessionID}
	if r.Action == "session.goal.set" {
		goal, ok := raw.(map[string]any)
		if !ok || goal["threadId"] != r.SessionID || goal["objective"] != objective || (status != "" && goal["status"] != status) {
			return core.Result{}, fmt.Errorf("native goal update was not confirmed")
		}
		eventMethod = "thread/goal/updated"
		eventParams["goal"] = goal
	} else if raw != nil {
		return core.Result{}, fmt.Errorf("native goal clear was not confirmed")
	}
	if a.emit != nil {
		if err := a.emit(r.SessionID, "codex.notification", map[string]any{"agent_id": rt.config.AgentID, "session_id": r.SessionID, "frame": map[string]any{"method": eventMethod, "params": eventParams}}, rt.Status()); err != nil {
			return core.Result{}, err
		}
	}
	return core.Result{SessionID: r.SessionID, Status: rt.Status(), Payload: map[string]any{"accepted": true, "goal": raw}}, nil
}
