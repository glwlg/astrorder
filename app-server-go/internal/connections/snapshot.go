package connections

import (
	"context"
	"fmt"

	sessiondv1 "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
)

// Connection state comes from daemon ownership plus the Agent projection. A
// registered adapter alone proves availability, not an attached native process.
func (h *Handler) localConnection(ctx context.Context, kind string) (map[string]any, error) {
	result := map[string]any{
		"kind": kind, "state": "offline", "available": false, "version": nil,
		"agent_id": nil, "source_id": nil, "profile_name": "default", "runtime_id": nil,
		"session_id": nil, "detail": "未发现可用的本机运行时。", "daemon_mode": true,
	}
	if kind == "codex" {
		result["state"] = "disconnected"
		result["session_count"] = 0
		result["auth_required"] = false
	}
	if h.client == nil {
		result["state"] = "error"
		result["detail"] = "Session daemon client is unavailable"
		return result, nil
	}
	status, err := h.client.Daemon.GetStatus(ctx, &sessiondv1.GetStatusRequest{})
	if err != nil || status == nil || status.GetDaemonId() != h.client.DaemonID() {
		result["state"] = "error"
		result["detail"] = "无法确认小内核连接状态。"
		return result, nil
	}
	runtimeKind := runtimeTarget(kind, nil, nil).GetRuntime()
	var runtime *sessiondv1.RuntimeStatus
	for _, candidate := range status.Runtimes {
		if candidate.GetRuntime() == runtimeKind && candidate.GetRegistered() {
			runtime = candidate
			break
		}
	}
	if runtime == nil {
		return result, nil
	}
	result["available"] = true
	if kind == "hermes" {
		result["state"] = "installed"
	}
	result["detail"] = "本机运行时已注册；可显式连接。"
	agents, err := h.store.ListAgents(ctx)
	if err != nil {
		return nil, fmt.Errorf("read connection Agent projection: %w", err)
	}
	var localAgent map[string]any
	for _, agent := range agents {
		connectionID, _ := agent["connection_id"].(string)
		if agent["kind"] == kind && (connectionID == "" || connectionID == "local") {
			if localAgent == nil || agent["status"] == "ready" {
				localAgent = agent
			}
		}
	}
	if localAgent != nil {
		result["agent_id"] = localAgent["id"]
		for _, key := range []string{"source_id", "profile_name", "runtime_id"} {
			if value := localAgent[key]; value != nil && value != "" {
				result[key] = value
			}
		}
	}
	if kind == "codex" {
		result["session_count"] = runtime.GetOwnedSessions()
	}
	if runtime.GetOwnedSessions() > 0 {
		result["state"] = "connecting"
		result["detail"] = "小内核持有本机运行时；正在等待 Agent 就绪投影。"
		if kind == "codex" || localAgent != nil && localAgent["status"] == "ready" {
			result["state"] = "connected"
			result["detail"] = "本机运行时已连接。"
		}
		controlID := "probe-" + kind + "-local"
		for _, session := range status.Sessions {
			if session.GetSession().GetSessionId() == controlID {
				result["session_id"] = controlID
				if session.GetStatus() == sessiondv1.SessionStatus_SESSION_STATUS_ERROR || session.GetStatus() == sessiondv1.SessionStatus_SESSION_STATUS_UNSPECIFIED {
					result["state"] = "error"
					result["detail"] = "本机控制通道异常；原生状态需要核对。"
				}
				break
			}
		}
	}
	return result, nil
}
