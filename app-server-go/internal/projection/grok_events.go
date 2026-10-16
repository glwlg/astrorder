package projection

import (
	"astrorder.dev/app-server/internal/store"
	sessiondv1 "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

func handleGrokNotificationTx(ctx context.Context, tx *sql.Tx, streamID string, payload map[string]any) ([]store.Event, error) {
	agentID, _ := payload["agent_id"].(string)
	sid, _ := payload["session_id"].(string)
	if sid == "" {
		sid = streamID
	}
	ident, err := resolveSessionIdentity(ctx, tx, streamID, agentID, sid)
	if err != nil {
		return nil, err
	}
	if ident != nil {
		agentID = ident.AgentID
		sid = ident.SessionID
	}

	frame, _ := payload["frame"].(map[string]any)
	if frame == nil {
		frame = payload
	}

	method, _ := frame["method"].(string)
	params, _ := frame["params"].(map[string]any)
	if params == nil {
		params = map[string]any{}
	}

	var events []store.Event

	// 1. Check for standard ACP session/request_permission or permission approval methods
	if method == "session/request_permission" || method == "item/permissions/requestApproval" ||
		method == "item/commandExecution/requestApproval" || method == "item/fileChange/requestApproval" {
		if agentID != "" && sid != "" {
			if err := updateSessionStatusTx(ctx, tx, agentID, sid, "waiting_approval"); err != nil {
				return nil, fmt.Errorf("update grok waiting approval: %w", err)
			}
			sessRows, err := queryRows(ctx, tx, "SELECT * FROM sessions WHERE agent_id=? AND id=?", agentID, sid)
			if err == nil && len(sessRows) > 0 {
				events = append(events, sessionUpsertEvent(agentID, sid, sessRows[0]))
			}
		}

		appID, _ := payload["approval_id"].(string)
		if appID == "" {
			if rawID, ok := frame["id"]; ok && rawID != nil {
				appID = "grok:" + nativeRequestID(rawID)
			}
		}
		if appID == "" {
			return nil, fmt.Errorf("%w: grok approval request missing approval_id", ErrCorruptedFrame)
		}

		title := "Grok 请求授权"
		var toolCall map[string]any
		if tc, ok := params["toolCall"].(map[string]any); ok {
			toolCall = tc
		} else if tc, ok := params["tool_call"].(map[string]any); ok {
			toolCall = tc
		}
		if toolCall != nil {
			if t, ok := toolCall["title"].(string); ok && t != "" {
				title = t
			}
		}

		appData := map[string]any{
			"id":         appID,
			"agent_id":   agentID,
			"session_id": sid,
			"title":      title,
			"detail":     method,
			"state":      "pending",
			"target_id":  appID,
			"native_id":  frame["id"],
			"method":     method,
			"options":    params["options"],
		}
		if toolCall != nil {
			appData["tool_call"] = toolCall
		}

		events = append(events, store.Event{
			ID:        fmt.Sprintf("approval-%s-%d", appID, time.Now().UnixNano()),
			Type:      "approval.upsert",
			AgentID:   agentID,
			SessionID: sid,
			Data:      appData,
		})
	}

	// 2. Check for session/update notifications
	update, _ := params["update"].(map[string]any)
	if update != nil && agentID != "" && sid != "" {
		kind, _ := update["sessionUpdate"].(string)

		switch kind {
		case "turn_completed":
			turnErr := update["error"]
			isFailed := turnErr != nil
			if stopReason, _ := update["stop_reason"].(string); stopReason == "error" || stopReason == "interrupted" {
				isFailed = true
			}
			newStatus := "idle"
			toolEndStatus := "completed"
			if isFailed {
				newStatus = "error"
				toolEndStatus = "failed"
			}

			if err := updateSessionStatusTx(ctx, tx, agentID, sid, newStatus); err != nil {
				return nil, fmt.Errorf("update grok session status: %w", err)
			}

			// Cleanup running tool tasks only; never mark unfinished plan tasks as completed!
			_, err = tx.ExecContext(ctx, `UPDATE tasks SET status=?,updated_at=datetime('now') WHERE agent_id=? AND session_id=? AND kind='tool' AND status='running'`, toolEndStatus, agentID, sid)
			if err != nil {
				return nil, fmt.Errorf("update grok tool tasks: %w", err)
			}

			// Cleanup pending approvals on completion
			approvalEvents, err := cleanupPendingApprovalsTx(ctx, tx, agentID, sid, "rejected")
			if err != nil {
				return nil, fmt.Errorf("cleanup pending approvals: %w", err)
			}
			events = append(events, approvalEvents...)

			// Process usage if available in turn_completed
			if usage, ok := update["usage"].(map[string]any); ok {
				usageEvs := projectGrokUsageMetricsTx(ctx, tx, sid, agentID, usage, update["model"])
				events = append(events, usageEvs...)
			}

			sessRows, err := queryRows(ctx, tx, "SELECT * FROM sessions WHERE agent_id=? AND id=?", agentID, sid)
			if err != nil {
				return nil, fmt.Errorf("query session: %w", err)
			}
			if len(sessRows) > 0 {
				events = append(events, sessionUpsertEvent(agentID, sid, sessRows[0]))
			}

		case "tool_call", "tool_call_update":
			toolEvs, err := projectGrokToolCallTx(ctx, tx, sid, agentID, frame, params, update)
			if err != nil {
				return nil, err
			}
			events = append(events, toolEvs...)

		case "plan":
			entries, _ := update["entries"].([]any)
			if entries == nil {
				entries, _ = update["steps"].([]any)
			}
			if entries == nil {
				entries, _ = update["todos"].([]any)
			}
			if entries != nil {
				planEvs, err := projectNativePlanTx(ctx, tx, sid, agentID, "grok", map[string]any{"todos": entries})
				if err != nil {
					return nil, err
				}
				events = append(events, planEvs...)
			}

		case "usage_update":
			usageEvs := projectGrokUsageUpdateTx(ctx, tx, sid, agentID, update)
			events = append(events, usageEvs...)

		case "session_title", "title":
			if title, ok := update["title"].(string); ok && strings.TrimSpace(title) != "" {
				// Respect manual non-placeholder titles
				var existingTitle string
				err := tx.QueryRowContext(ctx, "SELECT title FROM sessions WHERE agent_id=? AND id=?", agentID, sid).Scan(&existingTitle)
				if err == nil {
					if !isPlaceholderTitle(existingTitle, sid) && isPlaceholderTitle(title, sid) {
						title = existingTitle
					}
					if title != existingTitle {
						if _, execErr := tx.ExecContext(ctx, "UPDATE sessions SET title=?, updated_at=datetime('now') WHERE agent_id=? AND id=?", title, agentID, sid); execErr != nil {
							return nil, fmt.Errorf("update session title: %w", execErr)
						}
						sessRows, queryErr := queryRows(ctx, tx, "SELECT * FROM sessions WHERE agent_id=? AND id=?", agentID, sid)
						if queryErr != nil {
							return nil, fmt.Errorf("query updated session: %w", queryErr)
						}
						if len(sessRows) > 0 {
							events = append(events, sessionUpsertEvent(agentID, sid, sessRows[0]))
						}
					}
				}
			}

		case "agent_message_chunk", "agent_thought_chunk", "user_message_chunk":
			text := extractGrokText(update["content"])
			if text != "" {
				role := "assistant"
				itemType := "message"
				if kind == "agent_thought_chunk" {
					itemType = "thinking"
				} else if kind == "user_message_chunk" {
					role = "user"
				}

				msgID := extractGrokMessageID(frame, params, update, agentID, sid, kind)
				var activeCmdID sql.NullString
				_ = tx.QueryRowContext(ctx, `SELECT id FROM commands WHERE agent_id=? AND session_id=? AND state='running' ORDER BY created_at DESC LIMIT 1`, agentID, sid).Scan(&activeCmdID)
				var cmdIDParam any
				if activeCmdID.Valid && activeCmdID.String != "" {
					cmdIDParam = activeCmdID.String
				}
				events = append(events, liveMessageEvent(agentID, sid, msgID, role, itemType, text, cmdIDParam, true))
			}
		}
	}

	events = append(events, store.Event{
		ID:        fmt.Sprintf("grok-notif-%s-%d", sid, time.Now().UnixNano()),
		Type:      "grok.notification",
		AgentID:   agentID,
		SessionID: sid,
		Data:      payload,
	})
	return events, nil
}

func projectGrokApprovalResolvedTx(ctx context.Context, tx *sql.Tx, streamID string, payload map[string]any, envStatus sessiondv1.SessionStatus) ([]store.Event, error) {
	agentID, _ := payload["agent_id"].(string)
	sid, _ := payload["session_id"].(string)
	if sid == "" {
		sid = streamID
	}
	ident, err := resolveSessionIdentity(ctx, tx, streamID, agentID, sid)
	if err != nil {
		return nil, err
	}
	if ident != nil {
		agentID = ident.AgentID
		sid = ident.SessionID
	}

	approvalID, _ := payload["approval_id"].(string)
	decision, _ := payload["decision"].(string)
	state := "approved"
	if decision == "decline" || decision == "declined" || decision == "rejected" || decision == "cancel" {
		state = "rejected"
	}
	if approvalID == "" {
		if nativeID, ok := payload["native_id"]; ok && nativeID != nil {
			approvalID, _ = findMatchingApprovalID(ctx, tx, agentID, sid, nativeID)
		}
	}
	if approvalID == "" {
		return nil, fmt.Errorf("%w: grok approval resolution missing approval_id", ErrCorruptedFrame)
	}

	appData := map[string]any{
		"id":         approvalID,
		"agent_id":   agentID,
		"session_id": sid,
		"state":      state,
	}
	if nativeID, ok := payload["native_id"]; ok && nativeID != nil {
		appData["native_id"] = nativeID
	}

	events := []store.Event{{
		ID:        fmt.Sprintf("approval-%s-%d", approvalID, time.Now().UnixNano()),
		Type:      "approval.upsert",
		AgentID:   agentID,
		SessionID: sid,
		Data:      appData,
	}}

	// Determine resulting session status
	currentStatus := "running"
	switch envStatus {
	case sessiondv1.SessionStatus_SESSION_STATUS_RUNNING:
		currentStatus = "running"
	case sessiondv1.SessionStatus_SESSION_STATUS_IDLE:
		currentStatus = "idle"
	case sessiondv1.SessionStatus_SESSION_STATUS_WAITING_APPROVAL:
		currentStatus = "waiting_approval"
	case sessiondv1.SessionStatus_SESSION_STATUS_ERROR:
		currentStatus = "error"
	default:
		// Check remaining pending approvals
		pendingRows, err := queryRows(ctx, tx, `SELECT value FROM workspace_preferences WHERE instr("key", 'approval:') = 1`)
		if err != nil {
			return nil, err
		}
		hasOtherPending := false
		for _, row := range pendingRows {
			if v, ok := row["value"].(map[string]any); ok {
				if v["agent_id"] == agentID && v["session_id"] == sid && v["state"] == "pending" && v["id"] != approvalID {
					hasOtherPending = true
					break
				}
			}
		}
		if hasOtherPending {
			currentStatus = "waiting_approval"
		}
	}

	if err := updateSessionStatusTx(ctx, tx, agentID, sid, currentStatus); err != nil {
		return nil, err
	}
	rows, err := queryRows(ctx, tx, "SELECT * FROM sessions WHERE agent_id=? AND id=?", agentID, sid)
	if err != nil {
		return nil, err
	}
	if len(rows) > 0 {
		events = append(events, sessionUpsertEvent(agentID, sid, rows[0]))
	}

	return events, nil
}

func projectGrokToolCallTx(ctx context.Context, tx *sql.Tx, sid, aid string, frame, params, update map[string]any) ([]store.Event, error) {
	toolCallId, _ := update["toolCallId"].(string)
	if toolCallId == "" {
		toolCallId, _ = update["id"].(string)
	}
	if toolCallId == "" {
		return nil, nil
	}

	taskID := "grok:tool:" + toolCallId

	// Retrieve previous task state to prevent incremental tool_call_update from wiping out title, input, or output
	var prevTitle, prevProgressRaw, prevCommand sql.NullString
	_ = tx.QueryRowContext(ctx, "SELECT title, progress, command FROM tasks WHERE agent_id=? AND session_id=? AND id=?", aid, sid, taskID).Scan(&prevTitle, &prevProgressRaw, &prevCommand)
	var prevProg map[string]any
	if prevProgressRaw.Valid && prevProgressRaw.String != "" {
		prevProg = decodeTaskProgress(prevProgressRaw.String)
	}

	kind, _ := update["kind"].(string)
	title, _ := update["title"].(string)
	if title == "" && prevTitle.Valid && prevTitle.String != "" {
		title = prevTitle.String
	}
	if title == "" {
		title = kind
	}
	if title == "" {
		title = toolCallId
	}

	rawStatus, _ := update["status"].(string)
	if rawStatus == "" {
		if prevProg != nil {
			if s, ok := prevProg["native_status"].(string); ok && s != "" {
				rawStatus = s
			}
		}
		if rawStatus == "" {
			rawStatus = "in_progress"
		}
	}
	taskStatus := nativeTaskStatus(strings.ToLower(rawStatus))

	var outputText string
	var diffs []any
	if contentList, ok := update["content"].([]any); ok {
		for _, item := range contentList {
			if itemMap, ok := item.(map[string]any); ok {
				switch itemMap["type"] {
				case "text":
					if t, ok := itemMap["text"].(string); ok {
						if outputText != "" {
							outputText += "\n"
						}
						outputText += t
					}
				case "content":
					outputText += extractGrokText(itemMap["content"])
				case "diff":
					diffs = append(diffs, itemMap)
				}
			}
		}
	}
	if outputText == "" {
		if rawOut := update["rawOutput"]; rawOut != nil {
			if s, ok := rawOut.(string); ok {
				outputText = s
			} else if outMap, ok := rawOut.(map[string]any); ok {
				if t, ok := outMap["output"].(string); ok {
					outputText = t
				}
			}
		}
	}
	// Fallback to previous output if incremental update omitted output
	if outputText == "" && prevProg != nil {
		if pOut, ok := prevProg["output"].(string); ok {
			outputText = pOut
		}
	}
	if len(diffs) == 0 && prevProg != nil {
		if pDiffs, ok := prevProg["diff"].([]any); ok {
			diffs = pDiffs
		}
	}

	rawInput := update["rawInput"]
	if rawInput == nil && prevProg != nil {
		rawInput = prevProg["arguments"]
		if rawInput == nil {
			rawInput = prevProg["input"]
		}
	}

	var cmdStr any
	if inputMap, ok := rawInput.(map[string]any); ok {
		if c, ok := inputMap["command"].(string); ok {
			cmdStr = c
		}
	}
	if cmdStr == nil && prevCommand.Valid && prevCommand.String != "" {
		cmdStr = prevCommand.String
	}

	locations := update["locations"]
	if locations == nil && prevProg != nil {
		locations = prevProg["locations"]
	}

	rawOutput := update["rawOutput"]
	if rawOutput == nil && prevProg != nil {
		rawOutput = prevProg["result"]
	}

	progress := map[string]any{
		"source":        "grok",
		"native_status": rawStatus,
		"tool_call_id":  toolCallId,
		"arguments":     rawInput,
		"input":         rawInput,
		"output":        outputText,
		"result":        rawOutput,
		"diff":          diffs,
		"locations":     locations,
	}

	taskPayload := map[string]any{
		"id":       taskID,
		"kind":     "tool",
		"title":    title,
		"status":   taskStatus,
		"command":  cmdStr,
		"progress": progress,
		"logs":     []any{},
	}

	events, err := projectTaskUpsertTx(ctx, tx, sid, aid, taskPayload)
	if err != nil {
		return nil, err
	}

	// Emit live tool message event
	msgID := extractGrokMessageID(frame, params, update, aid, sid, "tool_"+toolCallId)
	toolMeta := map[string]any{
		"id":        toolCallId,
		"name":      title,
		"title":     title,
		"kind":      kind,
		"status":    taskStatus,
		"input":     rawInput,
		"output":    outputText,
		"result":    rawOutput,
		"diff":      diffs,
		"locations": locations,
	}
	msgEv := liveMessageEvent(aid, sid, msgID, "tool", "tool", outputText, nil, false)
	msgEv.Data["tool"] = toolMeta
	events = append(events, msgEv)

	return events, nil
}

func extractGrokMessageID(frame, params, update map[string]any, aid, sid, suffix string) string {
	// First priority: _meta.astrorderItemId (runtime negotiated ID)
	for _, m := range []map[string]any{params, update, frame} {
		if meta, ok := m["_meta"].(map[string]any); ok {
			if id, ok := meta["astrorderItemId"].(string); ok && id != "" {
				return id
			}
		}
	}
	// Second priority: _meta.eventId (e.g. 01a14374-e0f2-7471-a9ab-2121af087123-2)
	for _, m := range []map[string]any{params, update, frame} {
		if meta, ok := m["_meta"].(map[string]any); ok {
			if evID, ok := meta["eventId"].(string); ok && evID != "" {
				return evID
			}
		}
	}
	// Third priority: promptId
	token := ""
	for _, m := range []map[string]any{params, update, frame} {
		if meta, ok := m["_meta"].(map[string]any); ok {
			if pID, ok := meta["promptId"].(string); ok && pID != "" {
				token = pID
				break
			}
		}
	}
	if token != "" {
		return fmt.Sprintf("grok_%s_%s", token, suffix)
	}
	return fmt.Sprintf("grok_%s_%s_%d", sid, suffix, time.Now().UnixMilli())
}

func projectGrokUsageUpdateTx(ctx context.Context, tx *sql.Tx, sid, aid string, update map[string]any) []store.Event {
	usageData := update
	if nested, ok := update["usage"].(map[string]any); ok {
		usageData = nested
	}
	return projectGrokUsageMetricsTx(ctx, tx, sid, aid, usageData, update["model"])
}

func projectGrokUsageMetricsTx(ctx context.Context, tx *sql.Tx, sid, aid string, data map[string]any, modelVal any) []store.Event {
	getInt := func(keys ...string) (int64, bool) {
		for _, k := range keys {
			if val, ok := data[k]; ok && val != nil {
				switch n := val.(type) {
				case int:
					return int64(n), true
				case int64:
					return n, true
				case float64:
					return int64(n), true
				case json.Number:
					parsed, err := n.Int64()
					if err == nil {
						return parsed, true
					}
				}
			}
		}
		return 0, false
	}

	inTokens, hasIn := getInt("inputTokens", "input_tokens")
	outTokens, hasOut := getInt("outputTokens", "output_tokens")
	totalTokens, hasTot := getInt("totalTokens", "total_tokens")
	cachedTokens, _ := getInt("cachedReadTokens", "cache_read_input_tokens", "cached_tokens")
	reasoningTokens, _ := getInt("reasoningTokens", "reasoning_tokens")

	if !hasIn && !hasOut && !hasTot {
		return nil
	}

	model, _ := modelVal.(string)
	if model == "" {
		model, _ = data["model"].(string)
	}
	if model == "" {
		model = "grok"
	}

	if totalTokens == 0 && (hasIn || hasOut) {
		totalTokens = inTokens + outTokens
	}

	metricsPayload := map[string]any{
		"session_id":       sid,
		"agent_id":         aid,
		"model":            model,
		"provider":         "xai",
		"input_tokens":     inTokens,
		"output_tokens":    outTokens,
		"total_tokens":     totalTokens,
		"cached_tokens":    cachedTokens,
		"reasoning_tokens": reasoningTokens,
	}

	evs, err := projectTokenMetricsTx(ctx, tx, sid, aid, metricsPayload)
	if err != nil {
		return nil
	}
	return evs
}

func cleanupPendingApprovalsTx(ctx context.Context, tx *sql.Tx, aid, sid, state string) ([]store.Event, error) {
	rows, err := queryRows(ctx, tx, `SELECT value FROM workspace_preferences WHERE instr("key", 'approval:') = 1`)
	if err != nil {
		return nil, err
	}
	var events []store.Event
	for _, row := range rows {
		val, ok := row["value"].(map[string]any)
		if !ok {
			continue
		}
		if val["agent_id"] == aid && val["session_id"] == sid && val["state"] == "pending" {
			appID, _ := val["id"].(string)
			if appID == "" {
				continue
			}
			resolvedData := map[string]any{
				"id":         appID,
				"agent_id":   aid,
				"session_id": sid,
				"state":      state,
			}
			if nid, ok := val["native_id"]; ok {
				resolvedData["native_id"] = nid
			}
			events = append(events, store.Event{
				ID:        fmt.Sprintf("approval-%s-%d", appID, time.Now().UnixNano()),
				Type:      "approval.upsert",
				AgentID:   aid,
				SessionID: sid,
				Data:      resolvedData,
			})
		}
	}
	return events, nil
}

func nativeRequestID(raw any) string {
	return fmt.Sprint(raw)
}
