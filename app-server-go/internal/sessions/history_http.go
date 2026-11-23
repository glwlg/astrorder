package sessions

import (
	"astrorder.dev/app-server/internal/projection"
	"astrorder.dev/app-server/internal/store"
	pb "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

func writeMessagesJSON(w http.ResponseWriter, r *http.Request, payload map[string]any) {
	raw, err := json.Marshal(payload)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "Failed to encode messages")
		return
	}
	sum := sha256.Sum256(raw)
	etag := `W/"` + hex.EncodeToString(sum[:8]) + `"`
	w.Header().Set("ETag", etag)
	w.Header().Set("Cache-Control", "private, no-cache")
	if strings.TrimSpace(r.Header.Get("If-None-Match")) == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(raw)
}

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
	defer h.repairSessionTitles(ctx, []map[string]any{row})
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
	if h.client == nil || h.client.Runtime == nil {
		writeJSONError(w, 503, "Native history is unavailable")
		return
	}
	nativeID := stringValue(row, "source_session_id")
	if nativeID == "" {
		nativeID = sid
	}
	agent, err := h.store.GetAgent(ctx, aid)
	if err != nil {
		writeJSONError(w, 500, "Agent storage unavailable")
		return
	}
	// Codex history reads must never resume a dormant thread.
	unbound := strings.HasPrefix(before, unboundCursorPrefix)
	if stringValue(agent, "kind") == "codex" && before == "" {
		_, readErr := h.client.Runtime.ReadModel(ctx, &pb.ReadModelRequest{Session: buildSessionRef(aid, nativeID, stringValue(row, "connection_id"))})
		unbound = readErr != nil
	}
	if unbound {
		items, next, err := h.readCodexHistoryUnbound(ctx, row, nativeID, before, limit)
		if err != nil {
			writeJSONError(w, 503, "Native history read failed")
			return
		}
		writeMessagesJSON(w, r, map[string]any{"items": items, "next_cursor": next})
		return
	}
	if stringValue(agent, "kind") != "codex" {
		if err := h.ensureNativeBinding(ctx, row); err != nil {
			writeJSONError(w, 503, "Native session binding unavailable: "+err.Error())
			return
		}
	}
	request := &pb.HistoryPageRequest{Session: buildSessionRef(aid, nativeID, stringValue(row, "connection_id")), Limit: uint32(limit)}
	if before != "" {
		request.Before = &pb.NullableString{State: &pb.NullableString_Value{Value: before}}
	}
	page, err := h.client.Runtime.GetHistoryPage(ctx, request)
	if err != nil {
		if items, next, readErr := h.readCodexHistoryUnbound(ctx, row, nativeID, before, limit); readErr == nil {
			writeMessagesJSON(w, r, map[string]any{"items": items, "next_cursor": next})
			return
		} else {
			logUnboundHistory(row, err, readErr)
		}
		writeJSONError(w, 503, "Native history read failed")
		return
	}
	if page == nil || page.Session == nil || page.Session.SessionId != nativeID || page.Session.GetAgentId() != aid || page.Session.GetConnectionId() != request.Session.GetConnectionId() {
		writeJSONError(w, 502, "Native history identity mismatch")
		return
	}
	source := stringValue(row, "source_id")
	if source == "" {
		source = aid
	}
	kind := stringValue(agent, "kind")
	if kind == "codex" {
		// History is chronological; restore newest child snapshots first.
		for i := len(page.Items) - 1; i >= 0; i-- {
			var item map[string]any
			if err := parseNativeJSON(page.Items[i], &item); err != nil {
				writeJSONError(w, 502, "Invalid native history item")
				return
			}
			if err := h.restoreNativeTaskItem(ctx, aid, sid, item); err != nil {
				writeJSONError(w, 500, "Native task projection failed")
				return
			}
		}
	}
	items := []map[string]any{}
	for _, native := range page.Items {
		var item map[string]any
		if err := parseNativeJSON(native, &item); err != nil {
			writeJSONError(w, 502, "Invalid native history item")
			return
		}
		items = append(items, projectHistoryItem(item, aid, sid, nativeID, source, kind)...)
	}
	if kind == "antigravity" || kind == "antigravity-ssh" {
		h.enrichAntigravityCommandAttachments(ctx, aid, sid, items, before == "" && page.NextCursor == nil)
	}
	for _, item := range items {
		if item["role"] == "user" {
			h.ensureSessionTitle(ctx, row, stringValue(item, "text"))
			break
		}
	}
	var next any
	if page.NextCursor != nil {
		if value, ok := page.NextCursor.State.(*pb.NullableString_Value); ok {
			next = value.Value
		}
	}
	writeMessagesJSON(w, r, map[string]any{"items": items, "next_cursor": next})
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
	turnID := stringValue(item, "turnId")
	var startedAt, completedAt string
	if agentKind == "codex" || agentKind == "codex-ssh" {
		startedAt = codexMilliseconds(item["startedAtMs"])
		completedAt = codexMilliseconds(item["completedAtMs"])
	}
	if nested, ok := item["item"].(map[string]any); ok {
		item = nested
	}
	role := stringValue(item, "role")
	kind := "message"
	if agentKind == "grok" || agentKind == "grok-ssh" || agentKind == "antigravity" || agentKind == "antigravity-ssh" {
		if nativeKind := stringValue(item, "kind"); nativeKind != "" {
			kind = nativeKind
		}
	}
	if agentKind == "codex" || role == "" {
		switch stringValue(item, "type") {
		case "userMessage":
			role = "user"
		case "reasoning":
			role = "assistant"
			kind = "thinking"
		case "agentMessage", "plan":
			role = "assistant"
		case "commandExecution", "fileChange", "mcpToolCall", "webSearch", "dynamicToolCall", "collabAgentToolCall", "subAgentActivity":
			role = "tool"
			kind = "tool"
		default:
			if role == "" {
				role = "system"
			}
		}
	}
	identity := item["row_id"]
	if identity == nil {
		identity = item["message_id"]
	}
	if identity == nil {
		identity = item["id"]
	}
	if identity == nil && agentKind == "hermes" && role == "tool" {
		if callID := stringValue(item, "tool_call_id"); callID != "" {
			identity = "tool-call:" + callID
		}
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
	if startedAt != "" {
		created = startedAt
	} else if completedAt != "" {
		created = completedAt
	}
	row := map[string]any{"id": key, "agent_id": aid, "session_id": sid, "role": role, "kind": kind, "text": text, "attachments": []any{}, "created_at": created, "command_id": nil, "tool": item["tool"]}
	if (agentKind == "codex" || agentKind == "codex-ssh" || agentKind == "antigravity" || agentKind == "antigravity-ssh") && role == "assistant" {
		row["delivery"] = item["delivery"]
		row["questions"] = item["questions"]
		if item["answered"] != nil {
			row["answered"] = item["answered"]
		}
	}
	if startedAt != "" || completedAt != "" {
		row["timestamp_source"] = "native"
	}
	if completedAt != "" {
		row["completed_at"] = completedAt
	}
	if turnID != "" && (agentKind == "codex" || agentKind == "codex-ssh") {
		row["turn_id"] = turnID
	}
	if (agentKind == "codex" || agentKind == "codex-ssh") && role == "user" {
		row["attachments"] = projection.CodexUserAttachments(item["content"], sid, key)
	}
	if (agentKind == "antigravity" || agentKind == "antigravity-ssh") && role == "user" {
		if nativeAtts := sanitizeNativeHistoryAttachments(item["attachments"]); len(nativeAtts) > 0 {
			row["attachments"] = nativeAtts
		}
	}
	if agentKind == "codex" || agentKind == "codex-ssh" {
		if images := projection.CodexGeneratedImages(item, sid, key); len(images) > 0 {
			row["role"], row["kind"], row["attachments"] = "assistant", "message", images
			return []map[string]any{row}
		}
	}
	if kind == "tool" {
		if tool, output := codexToolFromItem(item); tool != nil {
			row["tool"] = tool
			if text == "" {
				row["text"] = output
			}
		} else if existingTool, ok := row["tool"].(map[string]any); ok {
			if existingTool["arguments"] == nil {
				if rawIn, hasIn := existingTool["input"]; hasIn && rawIn != nil {
					switch v := rawIn.(type) {
					case map[string]any:
						existingTool["arguments"] = v
					case string:
						trimmed := strings.TrimSpace(v)
						if strings.HasPrefix(trimmed, "{") {
							var parsed map[string]any
							dec := json.NewDecoder(strings.NewReader(trimmed))
							dec.UseNumber()
							if dec.Decode(&parsed) == nil && parsed != nil {
								existingTool["arguments"] = parsed
							}
						}
					}
				}
			}
			if row["text"] == "" {
				if out := historyText(existingTool["output"]); out != "" {
					row["text"] = out
				} else if errMsg := historyText(existingTool["error"]); errMsg != "" {
					row["text"] = errMsg
				}
			}
		}
	}
	if kind == "tool" && row["tool"] == nil {
		name := item["name"]
		if agentKind == "hermes" && name == nil {
			name = item["tool_name"]
		}
		row["tool"] = map[string]any{"name": name, "arguments": item["args"]}
	}
	if (agentKind == "antigravity" || agentKind == "antigravity-ssh") && role == "assistant" && kind == "message" {
		if thinking := stringValue(item, "thinking"); thinking != "" {
			thought := map[string]any{}
			for key, value := range row {
				thought[key] = value
			}
			thought["id"] = key + "-thinking"
			thought["kind"] = "thinking"
			thought["text"] = thinking
			return []map[string]any{thought, row}
		}
	}
	return []map[string]any{row}
}

func (h *Handler) restoreNativeTaskItem(ctx context.Context, aid, sid string, item map[string]any) error {
	var events []store.Event
	err := h.store.WithTx(ctx, func(tx *sql.Tx) error {
		var err error
		events, err = projection.RestoreNativeTaskItemTx(ctx, tx, sid, aid, item)
		return err
	})
	if err != nil {
		return err
	}
	for _, event := range events {
		h.publishEvent(ctx, event.Type, event.Data)
	}
	return nil
}

func sanitizeNativeHistoryAttachments(raw any) []any {
	var slice []any
	switch typed := raw.(type) {
	case []any:
		slice = typed
	case []map[string]any:
		slice = make([]any, len(typed))
		for i, m := range typed {
			slice[i] = m
		}
	default:
		return nil
	}
	if len(slice) == 0 {
		return nil
	}
	out := make([]any, 0, len(slice))
	for _, elem := range slice {
		m, ok := elem.(map[string]any)
		if !ok {
			continue
		}
		id := strings.TrimSpace(stringValue(m, "id"))
		name := strings.TrimSpace(stringValue(m, "name"))
		media := strings.ToLower(strings.TrimSpace(stringValue(m, "media_type")))
		href := strings.TrimSpace(stringValue(m, "url"))
		if id == "" || name == "" || media == "" || href == "" {
			continue
		}
		if !strings.HasPrefix(href, "/api/v1/attachments/") && !strings.HasPrefix(href, "data:image/") {
			continue
		}
		out = append(out, map[string]any{
			"id":         id,
			"name":       name,
			"media_type": media,
			"url":        href,
		})
	}
	return out
}

func parseHistoryFlexTime(raw any) (time.Time, bool) {
	if t, ok := raw.(time.Time); ok {
		return t.UTC(), !t.IsZero()
	}
	s := strings.TrimSpace(fmt.Sprint(raw))
	if s == "" || s == "<nil>" {
		return time.Time{}, false
	}
	for _, layout := range []string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02 15:04:05.999999999Z07:00",
		"2006-01-02 15:04:05.999999999",
		"2006-01-02 15:04:05",
	} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC(), true
		}
	}
	return time.Time{}, false
}

func (h *Handler) enrichAntigravityCommandAttachments(ctx context.Context, aid, sid string, items []map[string]any, singlePage bool) {
	if h.store == nil || len(items) == 0 {
		return
	}
	hasUser := false
	for _, it := range items {
		if it["role"] == "user" {
			hasUser = true
			break
		}
	}
	if !hasUser {
		return
	}
	cmdRows, err := h.store.QueryRows(ctx,
		`SELECT id, action, state, text, attachments, created_at, updated_at
		 FROM commands
		 WHERE agent_id=? AND session_id=? AND attachments IS NOT NULL AND attachments != '' AND attachments != '[]'
		 ORDER BY row_id ASC`,
		aid, sid,
	)
	if err != nil || len(cmdRows) == 0 {
		return
	}

	type cmdCand struct {
		id          string
		state       string
		normText    string
		attachments []any
		createdAt   time.Time
		hasCreated  bool
		updatedAt   time.Time
		hasUpdated  bool
		used        bool
	}
	cands := make([]cmdCand, 0, len(cmdRows))
	for _, r := range cmdRows {
		act := stringValue(r, "action")
		if act != "send" && act != "enqueue" {
			continue
		}
		st := stringValue(r, "state")
		if st == "queued" {
			continue
		}
		atts := sanitizeNativeHistoryAttachments(r["attachments"])
		if len(atts) == 0 {
			continue
		}
		cTime, hasC := parseHistoryFlexTime(r["created_at"])
		uTime, hasU := parseHistoryFlexTime(r["updated_at"])
		cands = append(cands, cmdCand{
			id:          stringValue(r, "id"),
			state:       st,
			normText:    strings.TrimSpace(visibleHandoffUserText(stringValue(r, "text"))),
			attachments: atts,
			createdAt:   cTime,
			hasCreated:  hasC,
			updatedAt:   uTime,
			hasUpdated:  hasU,
		})
	}
	if len(cands) == 0 {
		return
	}

	nowUTC := time.Now().UTC()
	for _, it := range items {
		if it["role"] != "user" {
			continue
		}
		msgText := strings.TrimSpace(stringValue(it, "text"))
		msgTime, hasMsgTime := parseHistoryFlexTime(it["created_at"])
		validMsgTime := hasMsgTime && msgTime.Year() > 1970

		bestIdx := -1
		var bestDiff time.Duration
		bestFailed := true

		for idx := range cands {
			c := &cands[idx]
			if c.used || c.normText != msgText {
				continue
			}
			isFailed := c.state == "failed"
			if validMsgTime && c.hasCreated {
				winStart := c.createdAt.Add(-90 * time.Second)
				endBase := c.createdAt
				if c.hasUpdated && c.updatedAt.After(endBase) {
					endBase = c.updatedAt
				}
				if (c.state == "running" || c.state == "accepted" || c.state == "received") && nowUTC.After(endBase) {
					endBase = nowUTC
				}
				winEnd := endBase.Add(90 * time.Second)
				if msgTime.Before(winStart) || msgTime.After(winEnd) {
					continue
				}
				diff := msgTime.Sub(c.createdAt)
				if diff < 0 {
					diff = -diff
				}
				if bestIdx == -1 || (bestFailed && !isFailed) || (bestFailed == isFailed && diff < bestDiff) {
					bestIdx = idx
					bestDiff = diff
					bestFailed = isFailed
				}
			} else if !validMsgTime && singlePage {
				if bestIdx == -1 || (bestFailed && !isFailed) {
					bestIdx = idx
					bestFailed = isFailed
				}
			}
		}

		if bestIdx >= 0 {
			cands[bestIdx].used = true
			it["attachments"] = cands[bestIdx].attachments
			if it["command_id"] == nil {
				it["command_id"] = cands[bestIdx].id
			}
		}
	}
}
