package antigravity

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	core "astrorder.dev/session-daemon/internal/runtime"
)

type acpPermissionOption struct {
	OptionID string
	Name     string
	Kind     string
}

type pendingApproval struct {
	id       string
	rawID    any
	options  []acpPermissionOption
	toolCall map[string]any
	title    string
}

type acpToolState struct {
	itemID          string
	name            string
	title           string
	kind            string
	acpStatus       string
	rawInput        map[string]any
	extraArgs       map[string]any
	rawOutput       string
	rawError        string
	questionEmitted bool
}

func parseDataImageURL(raw string) (mimeType, b64Data string, ok bool) {
	if !strings.HasPrefix(raw, "data:") {
		return "", "", false
	}
	semi := strings.Index(raw, ";base64,")
	if semi <= len("data:") {
		return "", "", false
	}
	mime := strings.ToLower(strings.TrimSpace(raw[len("data:"):semi]))
	switch mime {
	case "image/png", "image/jpeg", "image/gif", "image/webp":
	default:
		return "", "", false
	}
	payload := strings.TrimSpace(raw[semi+len(";base64,"):])
	if payload == "" {
		return "", "", false
	}
	if _, err := base64.StdEncoding.DecodeString(payload); err != nil {
		return "", "", false
	}
	return mime, payload, true
}

func buildACPPromptBlocks(p map[string]any, allowImages bool) ([]map[string]any, error) {
	blocks := []map[string]any{}

	if rawInputs, hasInputs := p["input"].([]any); hasInputs && len(rawInputs) > 0 {
		for _, elem := range rawInputs {
			item, ok := elem.(map[string]any)
			if !ok {
				return nil, core.RejectBeforeDispatch("Antigravity input part is invalid")
			}
			typ := str(item, "type")
			switch typ {
			case "text":
				if txt := str(item, "text"); txt != "" {
					blocks = append(blocks, map[string]any{"type": "text", "text": txt})
				}
			case "image":
				if !allowImages {
					return nil, core.RejectBeforeDispatch("Antigravity ACP server does not advertise image prompt capability")
				}
				mime, b64, valid := parseDataImageURL(str(item, "url"))
				if !valid {
					return nil, core.RejectBeforeDispatch("Antigravity ACP requires base64 data-URI image input")
				}
				blocks = append(blocks, map[string]any{
					"type":     "image",
					"mimeType": mime,
					"data":     b64,
				})
			default:
				return nil, core.RejectBeforeDispatch(fmt.Sprintf("Antigravity ACP does not support %q input parts", typ))
			}
		}
	} else {
		txt := str(p, "text")
		if txt == "" {
			txt = str(p, "prompt")
		}
		if txt != "" {
			blocks = append(blocks, map[string]any{"type": "text", "text": txt})
		}
	}

	if rawAtts, hasAtts := p["attachments"].([]any); hasAtts && len(rawAtts) > 0 {
		for _, elem := range rawAtts {
			att, ok := elem.(map[string]any)
			if !ok {
				return nil, core.RejectBeforeDispatch("Antigravity attachment is invalid")
			}
			name := strings.TrimSpace(str(att, "name"))
			media := strings.ToLower(strings.TrimSpace(str(att, "media_type")))
			if name == "" || media == "" {
				return nil, core.RejectBeforeDispatch("Antigravity attachment metadata is incomplete")
			}
			if strings.HasPrefix(media, "image/") {
				if !allowImages {
					return nil, core.RejectBeforeDispatch("Antigravity ACP server does not advertise image prompt capability")
				}
				switch media {
				case "image/png", "image/jpeg", "image/gif", "image/webp":
				default:
					return nil, core.RejectBeforeDispatch("Antigravity image attachment must be PNG, JPEG, GIF, or WebP")
				}
				b64 := strings.TrimSpace(str(att, "content_base64"))
				if b64 == "" {
					return nil, core.RejectBeforeDispatch("Antigravity image attachment content is missing")
				}
				if _, err := base64.StdEncoding.DecodeString(b64); err != nil {
					return nil, core.RejectBeforeDispatch("Antigravity image attachment base64 is invalid")
				}
				blocks = append(blocks, map[string]any{
					"type":     "image",
					"mimeType": media,
					"data":     b64,
				})
				continue
			}
			if txt, hasText := att["text"].(string); hasText && txt != "" {
				blocks = append(blocks, map[string]any{
					"type": "text",
					"text": fmt.Sprintf("File: %s\n%s", name, txt),
				})
				continue
			}
			return nil, core.RejectBeforeDispatch("Antigravity ACP supports image and UTF-8 text attachments")
		}
	}

	if len(blocks) == 0 {
		return nil, core.RejectBeforeDispatch("Antigravity text or attachment input required")
	}
	return blocks, nil
}

func extractACPContentText(raw any) string {
	switch v := raw.(type) {
	case string:
		return v
	case map[string]any:
		if txt, ok := v["text"].(string); ok {
			return txt
		}
		if inner, ok := v["content"]; ok {
			return extractACPContentText(inner)
		}
	case []any:
		var b strings.Builder
		for _, item := range v {
			b.WriteString(extractACPContentText(item))
		}
		return b.String()
	}
	return ""
}

func inferToolName(explicitName, kind, title string, rawInput map[string]any) string {
	if explicitName != "" {
		return explicitName
	}
	switch kind {
	case "execute":
		return "run_command"
	case "read":
		return "read_file"
	case "edit":
		return "edit_file"
	case "search":
		return "grep_search"
	case "fetch":
		return "read_url_content"
	}
	if firstNonEmptyStr(rawInput, "command", "cmd", "CommandLine", "Command") != "" {
		return "run_command"
	}
	if firstNonEmptyStr(rawInput, "Target", "Replacement", "old_string", "new_string") != "" {
		return "edit_file"
	}
	if firstNonEmptyStr(rawInput, "Path", "path", "AbsolutePath") != "" {
		return "read_file"
	}
	if _, hasQuestions := rawInput["questions"]; hasQuestions {
		return "ask_question"
	}
	if title != "" {
		return title
	}
	return "tool"
}

func extractToolOutputString(raw any) string {
	switch v := raw.(type) {
	case nil:
		return ""
	case string:
		return v
	case map[string]any:
		for _, k := range []string{"output", "stdout", "content", "text", "result"} {
			if s, ok := v[k].(string); ok && s != "" {
				return s
			}
		}
		if stderr, ok := v["stderr"].(string); ok && stderr != "" {
			return stderr
		}
		b, err := json.Marshal(v)
		if err == nil {
			return string(b)
		}
	case []any:
		var parts []string
		for _, elem := range v {
			if s := extractToolOutputString(elem); s != "" {
				parts = append(parts, s)
			}
		}
		return strings.Join(parts, "\n")
	default:
		return fmt.Sprint(v)
	}
	return ""
}

func (a *Adapter) routeNotification(c *acpClient, method string, params map[string]any) {
	if method != "session/update" || params == nil {
		return
	}
	sid := str(params, "sessionId")
	update, _ := params["update"].(map[string]any)
	if sid == "" || update == nil {
		return
	}
	a.mu.Lock()
	s := a.sessions[sid]
	if s == nil && !a.closed {
		if len(a.earlyUpdates) < 32 && len(a.earlyUpdates[sid]) < 64 {
			a.earlyUpdates[sid] = append(a.earlyUpdates[sid], update)
		}
		a.mu.Unlock()
		return
	}
	a.mu.Unlock()
	if s == nil {
		return
	}
	s.enqueue(func() {
		s.mu.Lock()
		sameClient := s.client == c && !s.closed
		suppress := s.suppressReplay
		s.mu.Unlock()
		if !sameClient {
			return
		}
		if suppress {
			// During a session/load fallback, still capture non-transcript state updates.
			switch str(update, "sessionUpdate") {
			case "available_commands_update", "usage_update", "current_mode_update", "config_option_update":
			default:
				return
			}
		}
		a.handleSessionUpdate(s, update)
	})
}

func (a *Adapter) routeServerRequest(c *acpClient, rawID any, method string, params map[string]any) {
	if method != "session/request_permission" {
		_ = c.respondError(rawID, -32601, "Method not supported by Astrorder ACP client")
		return
	}
	sid := str(params, "sessionId")
	a.mu.Lock()
	s := a.sessions[sid]
	a.mu.Unlock()
	if s == nil {
		_ = c.respond(rawID, map[string]any{"outcome": map[string]any{"outcome": "cancelled"}})
		return
	}
	s.enqueue(func() {
		a.handlePermissionRequest(s, c, rawID, params)
	})
}

func parsePermissionOptions(raw any) []acpPermissionOption {
	list, _ := raw.([]any)
	out := make([]acpPermissionOption, 0, len(list))
	for _, elem := range list {
		m, ok := elem.(map[string]any)
		if !ok {
			continue
		}
		optID := firstNonEmptyStr(m, "optionId", "id")
		if optID == "" {
			continue
		}
		out = append(out, acpPermissionOption{
			OptionID: optID,
			Name:     firstNonEmptyStr(m, "name", "label"),
			Kind:     firstNonEmptyStr(m, "kind"),
		})
	}
	return out
}

func pickAllowOptionID(options []acpPermissionOption, sessionScope bool) string {
	if sessionScope {
		for _, opt := range options {
			if opt.Kind == "allow_always" || opt.OptionID == "allow_always" {
				return opt.OptionID
			}
		}
	}
	for _, opt := range options {
		if opt.Kind == "allow_once" || opt.OptionID == "allow_once" {
			return opt.OptionID
		}
	}
	for _, opt := range options {
		if strings.HasPrefix(opt.Kind, "allow") || strings.HasPrefix(opt.OptionID, "allow") {
			return opt.OptionID
		}
	}
	if len(options) > 0 {
		return options[0].OptionID
	}
	return "allow_once"
}

func pickRejectOptionID(options []acpPermissionOption) string {
	for _, opt := range options {
		if opt.Kind == "reject_once" || opt.OptionID == "reject_once" {
			return opt.OptionID
		}
	}
	for _, opt := range options {
		if strings.HasPrefix(opt.Kind, "reject") || strings.HasPrefix(opt.OptionID, "reject") || strings.HasPrefix(opt.OptionID, "deny") {
			return opt.OptionID
		}
	}
	return ""
}

func (a *Adapter) handlePermissionRequest(s *session, c *acpClient, rawID any, params map[string]any) {
	options := parsePermissionOptions(params["options"])
	var toolCall map[string]any
	if tc, ok := params["toolCall"].(map[string]any); ok {
		toolCall = tc
	} else if tc, ok := params["tool_call"].(map[string]any); ok {
		toolCall = tc
	}

	s.mu.Lock()
	if s.closed || s.client != c {
		s.mu.Unlock()
		_ = c.respond(rawID, map[string]any{"outcome": map[string]any{"outcome": "cancelled"}})
		return
	}
	autoFull := s.mode == "full_access"
	s.mu.Unlock()

	if autoFull {
		optID := pickAllowOptionID(options, false)
		_ = c.respond(rawID, map[string]any{
			"outcome": map[string]any{
				"outcome":  "selected",
				"optionId": optID,
			},
		})
		return
	}

	title := "Antigravity 请求操作授权"
	detail := "session/request_permission"
	if toolCall != nil {
		if t := strings.TrimSpace(str(toolCall, "title")); t != "" {
			title = t
		} else if n := strings.TrimSpace(str(toolCall, "name")); n != "" {
			title = "允许执行 " + n
		}
		if rawIn, ok := toolCall["rawInput"].(map[string]any); ok {
			if cmdStr := firstNonEmptyStr(rawIn, "command", "cmd", "CommandLine"); cmdStr != "" {
				detail = cmdStr
			} else if pathStr := firstNonEmptyStr(rawIn, "path", "Path", "AbsolutePath"); pathStr != "" {
				detail = pathStr
			}
		}
	}

	approvalID := fmt.Sprintf("antigravity:%s:%v", s.id, rawID)
	s.mu.Lock()
	if s.approvals == nil {
		s.approvals = make(map[string]*pendingApproval)
	}
	s.approvals[approvalID] = &pendingApproval{
		id:       approvalID,
		rawID:    rawID,
		options:  options,
		toolCall: toolCall,
		title:    title,
	}
	s.status = "waiting_approval"
	s.mu.Unlock()

	appData := map[string]any{
		"id":         approvalID,
		"agent_id":   a.config.AgentID,
		"session_id": s.id,
		"title":      title,
		"detail":     detail,
		"state":      "pending",
		"target_id":  approvalID,
		"native_id":  rawID,
		"method":     "session/request_permission",
		"options":    params["options"],
	}
	if toolCall != nil {
		appData["tool_call"] = toolCall
	}
	_ = a.event(s, "approval.upsert", appData, "waiting_approval")
	_ = a.announce(s)
}

func (a *Adapter) markLiveActivity(s *session, kind string, update map[string]any) {
	var startMonitor bool
	var gen uint64
	var c *acpClient

	s.mu.Lock()
	s.lastUpdateAt = time.Now()
	if !s.suppressReplay && !s.closed && !s.control && !s.readonly && !s.deleteOnly &&
		s.status == "idle" && !s.promptRunning && !s.reactiveWaiting && s.client != nil && !s.client.exited() {
		skipWake := false
		if kind == "tool_call" || kind == "tool_call_update" {
			uStatus := firstNonEmptyStr(update, "status")
			uName := inferToolName(firstNonEmptyStr(update, "name"), firstNonEmptyStr(update, "kind"), firstNonEmptyStr(update, "title"), nil)
			if toolID := firstNonEmptyStr(update, "toolCallId", "id"); toolID != "" && s.toolItems != nil {
				if existing := s.toolItems[toolID]; existing != nil && existing.name != "" {
					uName = existing.name
				}
			}
			if uName == "ask_question" && (uStatus == "pending" || uStatus == "completed" || uStatus == "done") {
				skipWake = true
			}
		}
		if !skipWake {
			s.status = "running"
			s.reactiveWaiting = true
			s.reactiveMonitorGen++
			gen = s.reactiveMonitorGen
			c = s.client
			startMonitor = true
		}
	}
	s.mu.Unlock()

	if startMonitor {
		_ = a.announce(s)
		_ = a.emitOwner(s, "running", "")
		go a.monitorReactiveCompletion(s, c, gen)
	}
}

func (a *Adapter) handleSessionUpdate(s *session, update map[string]any) {
	kind := str(update, "sessionUpdate")
	switch kind {
	case "agent_message_chunk":
		delta := extractACPContentText(update["content"])
		if delta == "" {
			return
		}
		a.markLiveActivity(s, kind, update)
		s.mu.Lock()
		if s.activeMsgID == "" {
			s.activeMsgID = s.nextLiveIDLocked()
		}
		s.activeText.WriteString(delta)
		msgID := s.activeMsgID
		fullText := s.activeText.String()
		thinking := s.activeThinking.String()
		s.mu.Unlock()

		item := map[string]any{
			"id":          msgID,
			"role":        "assistant",
			"kind":        "message",
			"text":        fullText,
			"attachments": []any{},
		}
		if thinking != "" {
			item["thinking"] = thinking
		}
		_ = a.event(s, "message.upsert", item, "")

	case "agent_thought_chunk":
		delta := extractACPContentText(update["content"])
		if delta == "" {
			return
		}
		a.markLiveActivity(s, kind, update)
		s.mu.Lock()
		if s.activeText.Len() > 0 {
			s.activeMsgID = ""
			s.activeText.Reset()
			s.activeThinking.Reset()
		}
		if s.activeMsgID == "" {
			s.activeMsgID = s.nextLiveIDLocked()
		}
		s.activeThinking.WriteString(delta)
		msgID := s.activeMsgID
		fullText := s.activeText.String()
		thinking := s.activeThinking.String()
		s.mu.Unlock()

		_ = a.event(s, "message.upsert", map[string]any{
			"id":          msgID,
			"role":        "assistant",
			"kind":        "message",
			"text":        fullText,
			"thinking":    thinking,
			"attachments": []any{},
		}, "")

	case "tool_call", "tool_call_update":
		a.markLiveActivity(s, kind, update)
		a.handleToolCallUpdate(s, update)

	case "plan":
		entries, _ := update["entries"].([]any)
		for i, raw := range entries {
			entry, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			content := strings.TrimSpace(firstNonEmptyStr(entry, "content", "title"))
			if content == "" {
				continue
			}
			st := str(entry, "status")
			taskStatus := "pending"
			switch st {
			case "in_progress", "running":
				taskStatus = "running"
			case "completed", "done":
				taskStatus = "completed"
			case "failed", "cancelled":
				taskStatus = "failed"
			}
			_ = a.event(s, "task.upsert", map[string]any{
				"id":         fmt.Sprintf("agy:plan:%s:%d", s.id, i),
				"agent_id":   a.config.AgentID,
				"session_id": s.id,
				"kind":       "plan",
				"title":      content,
				"status":     taskStatus,
				"progress":   map[string]any{"source": "antigravity", "task_index": i},
			}, "")
		}

	case "usage_update":
		used, hasUsed := int64FromAny(update["used"])
		size, hasSize := int64FromAny(update["size"])
		if !hasUsed && !hasSize {
			return
		}
		s.mu.Lock()
		if hasUsed && used > 0 {
			s.lastUsedTokens = used
		}
		if hasSize && size > 0 {
			s.contextWindow = size
		}
		if s.isResumed && used == 0 && s.lastUsedTokens == 0 && !s.hasCumMetrics {
			s.mu.Unlock()
			return
		}
		model := s.model
		win := s.contextWindow
		lastIn := s.lastUsedTokens
		hasCum := s.hasCumMetrics
		cIn := s.cumInputTokens
		cOut := s.cumOutputTokens
		cCache := s.cumCachedTokens
		cReason := s.cumReasonTokens
		cTotal := s.cumTotalTokens
		s.mu.Unlock()

		if model == "" {
			model = "default"
		}
		payload := map[string]any{
			"agent_id":          a.config.AgentID,
			"session_id":        s.id,
			"provider":          "antigravity",
			"model":             model,
			"context_available": win > 0 || lastIn > 0,
			"source":            "acp_usage_update",
			"last_input_tokens": lastIn,
			"context_window":    win,
		}
		if hasCum {
			payload["input_tokens"] = cIn
			payload["output_tokens"] = cOut
			payload["cached_tokens"] = cCache
			payload["reasoning_tokens"] = cReason
			payload["total_tokens"] = cTotal
		}
		if a.config.ConnectionID != "" {
			payload["connection_id"] = a.config.ConnectionID
		}
		_ = a.emit(s.id, "token.usage", payload, "")

	case "available_commands_update":
		rawList, _ := update["availableCommands"].([]any)
		cmds := make([]map[string]any, 0, len(rawList))
		for _, raw := range rawList {
			m, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			name := strings.TrimPrefix(strings.TrimSpace(str(m, "name")), "/")
			if name == "" {
				continue
			}
			row := map[string]any{"name": name}
			if desc := strings.TrimSpace(str(m, "description")); desc != "" {
				row["description"] = desc
			}
			if inp, ok := m["input"].(map[string]any); ok {
				if hint := strings.TrimSpace(str(inp, "hint")); hint != "" {
					row["input_hint"] = hint
				}
			}
			cmds = append(cmds, row)
		}
		s.mu.Lock()
		s.availableCommands = cmds
		s.mu.Unlock()

	case "current_mode_update":
		modeID := str(update, "currentModeId")
		if modeID != "" {
			s.mu.Lock()
			if !(s.mode == "full_access" && modeID == "auto") {
				if mapped := fromACPMode(modeID); mapped != "" {
					s.mode = mapped
				}
			}
			s.mu.Unlock()
		}

	case "config_option_update":
		if opts, ok := update["configOptions"].([]any); ok {
			s.mu.Lock()
			s.applyConfigOptionsLocked(opts)
			s.mu.Unlock()
		}
	}
}

func (a *Adapter) handleToolCallUpdate(s *session, update map[string]any) {
	toolCallID := firstNonEmptyStr(update, "toolCallId", "id")
	if toolCallID == "" {
		return
	}

	s.mu.Lock()
	// Split assistant streaming messages around tool calls.
	s.activeMsgID = ""
	s.activeText.Reset()
	s.activeThinking.Reset()

	if s.toolItems == nil {
		s.toolItems = make(map[string]*acpToolState)
	}
	st := s.toolItems[toolCallID]
	if st == nil {
		st = &acpToolState{
			itemID:    s.nextLiveIDLocked(),
			rawInput:  map[string]any{},
			extraArgs: map[string]any{},
		}
		s.toolItems[toolCallID] = st
	}

	if k := firstNonEmptyStr(update, "kind"); k != "" {
		st.kind = k
	}
	if t := firstNonEmptyStr(update, "title"); t != "" {
		st.title = t
	}
	if stName := firstNonEmptyStr(update, "name"); stName != "" {
		st.name = stName
	}
	if status := firstNonEmptyStr(update, "status"); status != "" {
		st.acpStatus = status
	}

	for _, key := range []string{"rawInput", "args", "parameters"} {
		if rawIn, exists := update[key]; exists && rawIn != nil {
			switch typed := rawIn.(type) {
			case map[string]any:
				for mk, mv := range typed {
					st.rawInput[mk] = mv
				}
			case string:
				var parsed map[string]any
				if json.Unmarshal([]byte(typed), &parsed) == nil && parsed != nil {
					for mk, mv := range parsed {
						st.rawInput[mk] = mv
					}
				}
			}
		}
	}

	if locs, ok := update["locations"].([]any); ok && len(locs) > 0 {
		if loc0, ok := locs[0].(map[string]any); ok {
			if p := firstNonEmptyStr(loc0, "path", "uri"); p != "" {
				st.extraArgs["path"] = p
			}
			if lineNum, hasLine := intVal(loc0["line"]); hasLine && lineNum > 0 {
				st.extraArgs["start_line"] = lineNum
			}
		}
	}

	if rawOut, exists := update["rawOutput"]; exists && rawOut != nil {
		if outStr := extractToolOutputString(rawOut); outStr != "" {
			st.rawOutput = outStr
		}
		if outMap, ok := rawOut.(map[string]any); ok {
			if exitCode, hasExit := intVal(outMap["exitCode"]); hasExit {
				st.extraArgs["exit_code"] = exitCode
			}
			if errStr := firstNonEmptyStr(outMap, "error"); errStr != "" && st.rawError == "" {
				st.rawError = errStr
			}
		}
	}

	if contentList, ok := update["content"].([]any); ok && len(contentList) > 0 {
		var textParts []string
		for _, elem := range contentList {
			cm, ok := elem.(map[string]any)
			if !ok {
				continue
			}
			switch str(cm, "type") {
			case "diff":
				diffPath := firstNonEmptyStr(cm, "path")
				oldText, _ := cm["oldText"].(string)
				newText, _ := cm["newText"].(string)
				if diffPath != "" {
					st.extraArgs["path"] = diffPath
				}
				if oldText != "" || newText != "" {
					st.extraArgs["old_string"] = oldText
					st.extraArgs["new_string"] = newText
				}
			case "content":
				if txt := extractACPContentText(cm["content"]); txt != "" {
					textParts = append(textParts, txt)
				}
			default:
				if txt := extractACPContentText(cm); txt != "" {
					textParts = append(textParts, txt)
				}
			}
		}
		if len(textParts) > 0 && st.rawOutput == "" {
			st.rawOutput = strings.Join(textParts, "\n")
		}
	}

	if errVal, exists := update["error"]; exists && errVal != nil {
		switch ev := errVal.(type) {
		case string:
			st.rawError = ev
		case map[string]any:
			st.rawError = firstNonEmptyStr(ev, "message", "detail", "error")
		}
	}

	st.name = inferToolName(st.name, st.kind, st.title, st.rawInput)

	normStatus := "running"
	taskStatus := "running"
	switch st.acpStatus {
	case "completed", "done":
		normStatus = "completed"
		taskStatus = "completed"
	case "failed", "cancelled", "canceled", "error":
		normStatus = "error"
		taskStatus = "failed"
		if st.rawError == "" && st.rawOutput != "" {
			st.rawError = st.rawOutput
		}
	}

	rawInputCopy := make(map[string]any, len(st.rawInput))
	for k, v := range st.rawInput {
		rawInputCopy[k] = v
	}
	extraCopy := make(map[string]any, len(st.extraArgs))
	for k, v := range st.extraArgs {
		extraCopy[k] = v
	}
	itemID := st.itemID
	toolName := st.name
	toolTitle := st.title
	acpStatus := st.acpStatus
	rawOut := st.rawOutput
	rawErr := st.rawError
	alreadyEmittedQuestion := st.questionEmitted
	s.mu.Unlock()

	normalizedTool, messageText := normalizeAntigravityTool(itemID, toolName, normStatus, rawInputCopy, rawOut, rawErr, extraCopy, nil)
	if exitCode, ok := intVal(extraCopy["exit_code"]); ok {
		normalizedTool["exit_code"] = exitCode
	}

	displayTitle := toolTitle
	if displayTitle == "" {
		displayTitle = toolName
	}

	if toolName == "ask_question" && normStatus != "error" {
		tArgs, _ := normalizedTool["arguments"].(map[string]any)
		questions, summaryText := extractAntigravityQuestions(tArgs)
		if acpStatus == "pending" && len(questions) > 0 {
			var reactiveCmdID string
			var finishReactiveForQuestion bool
			s.mu.Lock()
			if cur := s.toolItems[toolCallID]; cur != nil {
				cur.questionEmitted = true
			}
			if s.reactiveWaiting && !s.promptRunning {
				s.reactiveWaiting = false
				s.cancelRequested = false
				s.reactiveMonitorGen++
				reactiveCmdID = s.command
				s.command = ""
				s.status = "idle"
				finishReactiveForQuestion = true
			}
			s.mu.Unlock()

			_ = a.event(s, "message.upsert", map[string]any{
				"id":          itemID,
				"role":        "assistant",
				"kind":        "message",
				"delivery":    "async",
				"questions":   questions,
				"answered":    false,
				"text":        summaryText,
				"attachments": []any{},
			}, "")
			_ = a.event(s, "task.upsert", map[string]any{
				"id":         "agy:tool:" + toolCallID,
				"agent_id":   a.config.AgentID,
				"session_id": s.id,
				"kind":       "tool",
				"title":      displayTitle,
				"status":     "completed",
				"progress": map[string]any{
					"source":        "antigravity",
					"tool":          toolName,
					"native_status": acpStatus,
				},
			}, "")
			if finishReactiveForQuestion {
				if reactiveCmdID != "" {
					_ = a.event(s, "command.upsert", map[string]any{
						"id":             reactiveCmdID,
						"state":          "completed",
						"error":          nil,
						"awaiting_input": true,
					}, "")
				}
				_ = a.announce(s)
				_ = a.emitOwner(s, "idle", "")
			}
			return
		}
		if normStatus == "completed" {
			if !alreadyEmittedQuestion && len(questions) > 0 {
				_ = a.event(s, "message.upsert", map[string]any{
					"id":          itemID,
					"role":        "assistant",
					"kind":        "message",
					"delivery":    "async",
					"questions":   questions,
					"answered":    true,
					"text":        summaryText,
					"attachments": []any{},
				}, "")
			}
			_ = a.event(s, "task.upsert", map[string]any{
				"id":         "agy:tool:" + toolCallID,
				"agent_id":   a.config.AgentID,
				"session_id": s.id,
				"kind":       "tool",
				"title":      displayTitle,
				"status":     "completed",
				"progress": map[string]any{
					"source":        "antigravity",
					"tool":          toolName,
					"native_status": acpStatus,
				},
			}, "")
			return
		}
		// While ask_question is still in_progress before entering pending, avoid
		// flashing a raw tool JSON block in the activity pack.
		_ = a.event(s, "task.upsert", map[string]any{
			"id":         "agy:tool:" + toolCallID,
			"agent_id":   a.config.AgentID,
			"session_id": s.id,
			"kind":       "tool",
			"title":      displayTitle,
			"status":     taskStatus,
			"progress": map[string]any{
				"source":        "antigravity",
				"tool":          toolName,
				"native_status": acpStatus,
			},
		}, "")
		return
	}

	_ = a.event(s, "message.upsert", map[string]any{
		"id":          itemID,
		"role":        "tool",
		"kind":        "tool",
		"tool":        normalizedTool,
		"text":        messageText,
		"attachments": []any{},
	}, "")

	_ = a.event(s, "task.upsert", map[string]any{
		"id":         "agy:tool:" + toolCallID,
		"agent_id":   a.config.AgentID,
		"session_id": s.id,
		"kind":       "tool",
		"title":      displayTitle,
		"status":     taskStatus,
		"progress": map[string]any{
			"source":        "antigravity",
			"tool":          toolName,
			"native_status": acpStatus,
		},
	}, "")
}
