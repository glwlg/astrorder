package sessions

import (
	pb "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
	"context"
	"fmt"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"net/http"
	"strings"
)

// Read-only menus must not require ownership of the user's Codex thread.
// A control probe belongs to the same runtime target and never resumes it.
func (h *Handler) unboundSettings(ctx context.Context, w http.ResponseWriter, r *http.Request, row map[string]any, bindErr error) (*pb.SessionRef, bool) {
	busy := strings.Contains(bindErr.Error(), "already has an active writer")
	if !busy && status.Code(bindErr) != codes.NotFound && status.Code(bindErr) != codes.Unavailable {
		return nil, false
	}
	aid := stringValue(row, "agent_id")
	agent, err := h.store.GetAgent(ctx, aid)
	if err != nil || stringValue(agent, "kind") != "codex" {
		return nil, false
	}
	target, err := h.resolveRuntimeTarget(ctx, agent)
	if err != nil {
		return nil, false
	}
	suffix := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
	if suffix == "model" {
		nativeID := stringValue(row, "source_session_id")
		if nativeID == "" {
			nativeID = stringValue(row, "id")
		}
		includeTurns := false
		response, err := h.client.Runtime.QueryNative(ctx, &pb.NativeQueryRequest{Target: target, Query: &pb.NativeQueryRequest_CodexThreadRead{CodexThreadRead: &pb.CodexThreadReadQuery{ThreadId: nativeID, IncludeTurns: &includeTurns}}})
		if err != nil {
			return nil, false
		}
		var data struct {
			Thread map[string]any `json:"thread"`
		}
		if parseNativeJSON(response.Result, &data) != nil || data.Thread == nil {
			return nil, false
		}
		result := map[string]any{"read_only": busy, "model_read_only": busy}
		if busy {
			result["detail"] = "该会话已有其他 Codex 写入者；当前仅可读取。"
		}
		for from, to := range map[string]string{"model": "model", "modelProvider": "provider", "reasoningEffort": "effort"} {
			if value, ok := data.Thread[from]; ok {
				result[to] = value
			}
		}
		if git, ok := data.Thread["gitInfo"].(map[string]any); ok {
			if branch, ok := git["branch"]; ok {
				result["branch"] = branch
			}
		}
		writeJSON(w, http.StatusOK, result)
		return nil, true
	}
	if suffix == "agent-commands" {
		writeJSON(w, http.StatusOK, map[string]any{"items": []any{}, "supported": false, "read_only": true, "detail": "Codex 未提供原生命令列表接口。"})
		return nil, true
	}
	if suffix == "approval-mode" {
		if mode := stringValue(row, "selected_approval_mode"); mode != "" {
			writeJSON(w, http.StatusOK, map[string]any{"mode": mode})
			return nil, true
		}
		return nil, false
	}
	if suffix != "models" && suffix != "agent-mentions" {
		return nil, false
	}
	conn := stringValue(row, "connection_id")
	label := conn
	if label == "" {
		label = "local"
	}
	ref := buildSessionRef(aid, fmt.Sprintf("probe-codex-%s", label), conn)
	receipt, err := h.client.Session.AttachSession(ctx, &pb.AttachSessionRequest{Session: ref, Target: target, RuntimeControl: true})
	if err != nil || receipt == nil || !receipt.Attached || receipt.Session == nil || receipt.Session.SessionId != ref.SessionId || receipt.Session.GetConnectionId() != conn {
		return nil, false
	}
	return ref, true
}
