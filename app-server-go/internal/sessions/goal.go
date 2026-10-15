package sessions

import (
	pb "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
	"net/http"
	"strings"
)

func (h *Handler) handleSessionGoal(w http.ResponseWriter, r *http.Request, sid string) {
	var body struct {
		Objective string `json:"objective"`
		Status string `json:"status"`
	}
	if r.Method == http.MethodPut {
		if err := decodeJSON(r.Body, &body); err != nil || (body.Status == "" && (strings.TrimSpace(body.Objective) == "" || len(body.Objective) > 65536)) || (body.Status != "" && body.Status != "paused" && body.Status != "active") || (body.Status != "" && body.Objective != "") {
			writeJSONError(w, http.StatusUnprocessableEntity, "目标不能为空，且不能超过 65536 字节")
			return
		}
	}
	aid := r.URL.Query().Get("agent_id")
	agent, err := h.store.GetAgent(r.Context(), aid)
	if err != nil {
		writeJSONError(w, 500, "Failed to get agent")
		return
	}
	kind, _ := agent["kind"].(string)
	if kind != "codex" && kind != "hermes" {
		writeJSONError(w, http.StatusUnprocessableEntity, "此 Agent 尚未提供原生目标管理接口")
		return
	}
	ref, _, session, ok := h.settingsRef(r.Context(), w, r, sid)
	if !ok {
		return
	}
	if nativeID := stringValue(session, "source_session_id"); nativeID != "" {
		ref.SessionId = nativeID
	}
	var receipt *pb.CommandReceipt
	if r.Method == http.MethodDelete {
		receipt, err = h.client.Runtime.ClearGoal(r.Context(), &pb.ClearGoalRequest{Meta: newCommandMeta("", ""), Session: ref})
	} else {
		receipt, err = h.client.Runtime.SetGoal(r.Context(), &pb.SetGoalRequest{Meta: newCommandMeta("", ""), Session: ref, Objective: body.Objective, Status: body.Status})
	}
	if err == nil {
		err = confirmedReceipt(receipt)
	}
	if err != nil {
		writeJSONError(w, http.StatusBadGateway, "原生目标操作失败："+err.Error())
		return
	}
	result := receipt.GetCompleted()
	var native map[string]any
	if result == nil || result.Native == nil || parseNativeJSON(result.Native, &native) != nil {
		writeJSONError(w, http.StatusBadGateway, "目标操作缺少原生确认结果")
		return
	}
	goal, present := native["goal"]
	if !present {
		writeJSONError(w, http.StatusBadGateway, "目标操作缺少原生回读结果")
		return
	}
	if r.Method == http.MethodDelete && goal != nil {
		writeJSONError(w, http.StatusBadGateway, "目标删除未得到确认")
		return
	}
	if r.Method == http.MethodPut {
		if _, ok := goal.(map[string]any); !ok {
			writeJSONError(w, http.StatusBadGateway, "目标修改未得到确认")
			return
		}
		if body.Status != "" && goal.(map[string]any)["status"] != body.Status {
			writeJSONError(w, http.StatusBadGateway, "目标状态切换未得到确认")
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"goal": goal, "source": kind})
}
