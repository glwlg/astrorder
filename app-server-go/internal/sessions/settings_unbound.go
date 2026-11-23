package sessions

import (
	pb "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
	"context"
	"fmt"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"net/http"
	"strings"
	"time"
)

func normalizeAntigravityModel(model string) string {
	if isAntigravityInternalWorkerModel(model) {
		return "gemini-pro-orchestrator"
	}
	return model
}

// backfillAntigravityModel populates result["model"] from persisted metrics or
// native SQLite metadata when runtime binding or settings lack a selected model.
func (h *Handler) backfillAntigravityModel(ctx context.Context, target *pb.RuntimeTarget, aid string, row map[string]any, readonly bool, result map[string]any) {
	if m := stringValue(result, "model"); m != "" {
		result["model"] = normalizeAntigravityModel(m)
		return
	}
	sid := stringValue(row, "id")
	if metrics, mErr := h.store.ListTokenMetrics(ctx, aid, sid); mErr == nil && len(metrics) > 0 {
		if m := stringValue(metrics[0], "model"); m != "" && m != "probe" {
			result["model"] = normalizeAntigravityModel(m)
			return
		}
	}
	if target != nil && h.client != nil && h.client.Runtime != nil {
		nativeID := stringValue(row, "source_session_id")
		if nativeID == "" {
			nativeID = sid
		}
		uCtx, uCancel := context.WithTimeout(ctx, 4*time.Second)
		resp, qErr := h.client.Runtime.QueryNative(uCtx, &pb.NativeQueryRequest{
			Target: target,
			Query: &pb.NativeQueryRequest_AntigravitySessionUsage{
				AntigravitySessionUsage: &pb.AntigravitySessionUsageQuery{
					ConversationId: nativeID,
					Readonly:       &readonly,
				},
			},
		})
		uCancel()
		if qErr == nil && resp != nil {
			var usageData map[string]any
			if parseNativeJSON(resp.Result, &usageData) == nil && usageData != nil {
				if m := stringValue(usageData, "model"); m != "" {
					result["model"] = normalizeAntigravityModel(m)
					return
				}
				if dm := stringValue(usageData, "default_model"); dm != "" {
					result["model"] = normalizeAntigravityModel(dm)
					return
				}
			}
		}
	}
	result["model"] = "default"
}

// Read-only menus must not require ownership of the user's Codex thread.
// A control probe belongs to the same runtime target and never resumes it.
func (h *Handler) unboundSettings(ctx context.Context, w http.ResponseWriter, r *http.Request, row map[string]any, bindErr error) (*pb.SessionRef, bool) {
	busy := strings.Contains(bindErr.Error(), "already has an active writer")
	if !busy && status.Code(bindErr) != codes.NotFound && status.Code(bindErr) != codes.Unavailable {
		return nil, false
	}
	aid := stringValue(row, "agent_id")
	agent, err := h.store.GetAgent(ctx, aid)
	if err != nil || agent == nil {
		return nil, false
	}
	kind := stringValue(agent, "kind")
	if kind == "antigravity-ssh" {
		kind = "antigravity"
	}
	if kind != "codex" && kind != "antigravity" {
		return nil, false
	}
	target, err := h.resolveRuntimeTarget(ctx, agent)
	if err != nil {
		return nil, false
	}
	suffix := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
	if kind == "antigravity" {
		readonly := stringValue(row, "control_state") == "readonly"
		switch suffix {
		case "model":
			sid := stringValue(row, "id")
			saved, _ := h.store.GetSessionModelBinding(ctx, aid, sid)
			result := map[string]any{
				"provider":            "antigravity",
				"read_only":           readonly,
				"model_read_only":     readonly,
				"reasoning_read_only": true,
			}
			if saved != nil {
				if m := stringValue(saved, "model"); m != "" {
					result["model"] = m
				}
				if eff := stringValue(saved, "effort"); eff != "" {
					result["effort"] = eff
				}
			}
			h.backfillAntigravityModel(ctx, target, aid, row, readonly, result)
			if inferred := antigravityEffortFromModel(stringValue(result, "model")); inferred != "" {
				result["effort"] = inferred
			}
			if readonly {
				result["detail"] = "此会话来自 Antigravity 桌面版，可读取历史；agy CLI 无法续聊，请新建 Antigravity 会话。"
			}
			writeJSON(w, http.StatusOK, result)
			return nil, true
		case "approval-mode":
			mode := stringValue(row, "selected_approval_mode")
			if mode == "" {
				mode = "manual"
			}
			writeJSON(w, http.StatusOK, map[string]any{
				"mode":            mode,
				"available_modes": []string{"manual", "auto", "full_access"},
			})
			return nil, true
		case "agent-commands":
			writeJSON(w, http.StatusOK, map[string]any{"items": []any{}, "supported": true, "read_only": false, "detail": "会话连接后可读取官方 agy acp 动态命令列表。"})
			return nil, true
		case "agent-mentions":
			writeJSON(w, http.StatusOK, map[string]any{"items": []any{}})
			return nil, true
		case "models":
			conn := stringValue(row, "connection_id")
			label := conn
			if label == "" {
				label = "local"
			}
			ref := buildSessionRef(aid, fmt.Sprintf("probe-antigravity-%s", label), conn)
			receipt, err := h.client.Session.AttachSession(ctx, &pb.AttachSessionRequest{Session: ref, Target: target, RuntimeControl: true})
			if err != nil || receipt == nil || !receipt.Attached || receipt.Session == nil || receipt.Session.SessionId != ref.SessionId || receipt.Session.GetConnectionId() != conn {
				return nil, false
			}
			return ref, true
		default:
			return nil, false
		}
	}
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
