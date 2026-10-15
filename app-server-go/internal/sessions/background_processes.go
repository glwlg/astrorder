package sessions

import (
	pb "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
	"context"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"net/http"
	"time"
)

// A read must not attach a historical conversation or spawn a fresh shell.
func (h *Handler) handleBackgroundProcesses(w http.ResponseWriter, r *http.Request, sid string) {
	aid := r.URL.Query().Get("agent_id")
	if aid == "" {
		writeJSONError(w, 422, "agent_id is required")
		return
	}
	session, err := h.store.GetSession(r.Context(), aid, sid)
	if err != nil {
		writeJSONError(w, 500, err.Error())
		return
	}
	if session == nil {
		writeJSONError(w, 404, "Session was not found")
		return
	}
	if h.client == nil || h.client.Runtime == nil {
		writeJSONError(w, 503, "Daemon client is not configured")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	ref := buildSessionRef(aid, sid, stringValue(session, "connection_id"))
	if nativeID := stringValue(session, "source_session_id"); nativeID != "" {
		ref.SessionId = nativeID
	}
	response, err := h.client.Runtime.ListBackgroundProcesses(ctx, &pb.ListBackgroundProcessesRequest{Session: ref})
	if status.Code(err) == codes.NotFound {
		writeJSON(w, 200, map[string]any{"items": []any{}})
		return
	}
	if err != nil {
		writeJSONError(w, http.StatusBadGateway, "后台任务读取失败："+err.Error())
		return
	}
	items := []map[string]any{}
	for _, value := range response.Processes {
		var row map[string]any
		if err := parseNativeJSON(value, &row); err != nil {
			writeJSONError(w, 502, "后台任务数据无效")
			return
		}
		id, _ := row["session_id"].(string)
		if id == "" || row["status"] != "running" {
			continue
		}
		output, _ := row["output_tail"].(string)
		if output == "" {
			output, _ = row["output_preview"].(string)
		}
		now := time.Now().UTC().Format(time.RFC3339)
		command, _ := row["command"].(string)
		items = append(items, map[string]any{"id": "process:" + id, "agent_id": aid, "session_id": sid, "kind": "background", "status": "running", "title": command, "command": command, "target_id": id, "progress": map[string]any{"process_id": id, "output": output, "cwd": row["cwd"], "pid": row["pid"]}, "logs": []any{}, "created_at": now, "updated_at": now})
	}
	writeJSON(w, 200, map[string]any{"items": items})
}
