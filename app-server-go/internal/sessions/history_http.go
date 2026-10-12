package sessions

import (
	pb "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

func (h *Handler) handleListMessages(w http.ResponseWriter, r *http.Request, sid string) {
	ctx := r.Context()
	aid := r.URL.Query().Get("agent_id")
	if aid == "" {
		writeJSONError(w, 422, "agent_id is required")
		return
	}
	row, err := h.store.GetSession(ctx, aid, sid)
	if err != nil {
		writeJSONError(w, 500, "Session storage unavailable")
		return
	}
	if row == nil {
		writeJSONError(w, 404, "Session was not found")
		return
	}
	limit := 50
	if value := r.URL.Query().Get("limit"); value != "" {
		limit, err = strconv.Atoi(value)
		if err != nil || limit < 1 || limit > 200 {
			writeJSONError(w, 422, "limit must be between 1 and 200")
			return
		}
	}
	before := r.URL.Query().Get("before")
	if len(before) > 512 {
		writeJSONError(w, 422, "Invalid history cursor")
		return
	}
	if row["history_state"] == "local" && row["control_state"] == "unknown" {
		items, next, err := h.store.ListMessages(ctx, aid, sid, before, limit)
		if err != nil {
			writeJSONError(w, 400, "Invalid history cursor")
			return
		}
		var cursor any
		if next != "" {
			cursor = next
		}
		writeJSON(w, 200, map[string]any{"items": items, "next_cursor": cursor})
		return
	}

	fallbackToStore := func() bool {
		items, next, storeErr := h.store.ListMessages(ctx, aid, sid, before, limit)
		if storeErr == nil {
			var cursor any
			if next != "" {
				cursor = next
			}
			writeJSON(w, 200, map[string]any{"items": items, "next_cursor": cursor})
			return true
		}
		return false
	}

	if h.client == nil {
		if fallbackToStore() {
			return
		}
		writeJSONError(w, 503, "Native history is unavailable")
		return
	}
	nativeID := stringValue(row, "source_session_id")
	if nativeID == "" {
		nativeID = sid
	}
	// Reading a Codex transcript does not need the daemon to own the thread, and
	// owning it can be refused (another client is the active writer, or the
	// thread is archived). A cursor from the unbound path stays on that path.
	if strings.HasPrefix(before, unboundCursorPrefix) {
		items, next, err := h.readCodexHistoryUnbound(ctx, row, nativeID, before, limit)
		if err != nil {
			writeJSONError(w, 503, "Native history read failed")
			return
		}
		writeJSON(w, 200, map[string]any{"items": items, "next_cursor": next})
		return
	}
	if err := h.ensureNativeBinding(ctx, row); err != nil {
		if items, next, readErr := h.readCodexHistoryUnbound(ctx, row, nativeID, before, limit); readErr == nil {
			writeJSON(w, 200, map[string]any{"items": items, "next_cursor": next})
			return
		} else {
			logUnboundHistory(row, err, readErr)
		}
		if fallbackToStore() {
			return
		}
		writeJSONError(w, 503, "Native session binding unavailable: "+err.Error())
		return
	}
	request := &pb.HistoryPageRequest{Session: buildSessionRef(aid, nativeID, stringValue(row, "connection_id")), Limit: uint32(limit)}
	if before != "" {
		request.Before = &pb.NullableString{State: &pb.NullableString_Value{Value: before}}
	}
	page, err := h.client.Runtime.GetHistoryPage(ctx, request)
	if err != nil {
		if items, next, readErr := h.readCodexHistoryUnbound(ctx, row, nativeID, before, limit); readErr == nil {
			writeJSON(w, 200, map[string]any{"items": items, "next_cursor": next})
			return
		} else {
			logUnboundHistory(row, err, readErr)
		}
		if fallbackToStore() {
			return
		}
		writeJSONError(w, 503, "Native history read failed")
		return
	}
	if page == nil || page.Session == nil || page.Session.SessionId != nativeID || page.Session.GetAgentId() != aid || page.Session.GetConnectionId() != request.Session.GetConnectionId() {
		if fallbackToStore() {
			return
		}
		writeJSONError(w, 502, "Native history identity mismatch")
		return
	}
	source := stringValue(row, "source_id")
	if source == "" {
		source = aid
	}
	agent, err := h.store.GetAgent(ctx, aid)
	if err != nil {
		writeJSONError(w, 500, "Agent storage unavailable")
		return
	}
	kind := stringValue(agent, "kind")
	items := []map[string]any{}
	for _, native := range page.Items {
		var item map[string]any
		if err := parseNativeJSON(native, &item); err != nil {
			writeJSONError(w, 502, "Invalid native history item")
			return
		}
		items = append(items, projectHistoryItem(item, aid, sid, nativeID, source, kind)...)
	}
	for _, item := range items {
		previous, err := h.store.GetMessage(ctx, aid, sid, stringValue(item, "id"))
		if err != nil {
			writeJSONError(w, 500, "History projection read failed")
			return
		}
		if previous != nil {
			item["attachments"] = previous["attachments"]
			item["command_id"] = previous["command_id"]
		}
	}
	if _, err = h.store.UpsertMessages(ctx, items); err != nil {
		writeJSONError(w, 500, "History projection failed")
		return
	}
	var next any
	if page.NextCursor != nil {
		if value, ok := page.NextCursor.State.(*pb.NullableString_Value); ok {
			next = value.Value
		}
	}
	writeJSON(w, 200, map[string]any{"items": items, "next_cursor": next})
}
func historyText(value any) string {
	if text, ok := value.(string); ok {
		return text
	}
	if value == nil {
		return ""
	}
	if parts, ok := value.([]any); ok {
		var b strings.Builder
		for _, part := range parts {
			if obj, ok := part.(map[string]any); ok {
				b.WriteString(stringValue(obj, "text"))
			} else if text, ok := part.(string); ok {
				b.WriteString(text)
			}
		}
		return b.String()
	}
	raw, _ := json.Marshal(value)
	return string(raw)
}
func projectHistoryItem(item map[string]any, aid, sid, nativeID, source, agentKind string) []map[string]any {
	if nested, ok := item["item"].(map[string]any); ok {
		item = nested
	}
	role := stringValue(item, "role")
	kind := "message"
	if role == "" {
		switch stringValue(item, "type") {
		case "userMessage":
			role = "user"
		case "reasoning":
			role = "assistant"
			kind = "thinking"
		case "agentMessage":
			role = "assistant"
		case "commandExecution", "fileChange", "mcpToolCall", "webSearch":
			role = "tool"
			kind = "tool"
		default:
			role = "system"
		}
	}
	identity := item["row_id"]
	if identity == nil {
		identity = item["message_id"]
	}
	if identity == nil {
		identity = item["id"]
	}
	if identity == nil {
		return nil
	}
	key := fmt.Sprint(identity)
	if agentKind == "hermes" {
		sum := sha256.Sum256([]byte(source + "\x00" + nativeID + "\x00" + key))
		key = "history-message-" + hex.EncodeToString(sum[:])[:48]
	}
	content := item["text"]
	if content == nil {
		content = item["content"]
	}
	if content == nil {
		content = item["context"]
	}
	text := historyText(content)
	if strings.HasPrefix(text, "\x00json:") {
		var decoded any
		d := json.NewDecoder(strings.NewReader(text[6:]))
		d.UseNumber()
		if d.Decode(&decoded) == nil {
			text = historyText(decoded)
		}
	}
	if role == "user" {
		text = visibleHandoffUserText(text)
		if agentKind == "hermes" {
			text = visibleHermesUserText(text)
		}
	}
	if role == "tool" {
		kind = "tool"
	}
	stamp := item["created_at"]
	if stamp == nil {
		stamp = item["timestamp"]
	}
	created := "1970-01-01T00:00:00Z"
	if value, ok := stamp.(string); ok && value != "" {
		created = value
	} else if value, ok := stamp.(json.Number); ok {
		if seconds, err := strconv.ParseFloat(value.String(), 64); err == nil {
			created = time.UnixMicro(int64(seconds * 1e6)).UTC().Format(time.RFC3339Nano)
		}
	}
	row := map[string]any{"id": key, "agent_id": aid, "session_id": sid, "role": role, "kind": kind, "text": text, "attachments": []any{}, "created_at": created, "command_id": nil, "tool": item["tool"]}
	if kind == "tool" {
		if tool, output := codexToolFromItem(item); tool != nil {
			row["tool"] = tool
			if text == "" {
				row["text"] = output
			}
		}
	}
	if kind == "tool" && row["tool"] == nil {
		row["tool"] = map[string]any{"name": item["name"], "arguments": item["args"]}
	}
	return []map[string]any{row}
}
