package agentapi

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"astrorder.dev/app-server/internal/blackboard"
	sessiondv1 "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
)

func newCommandMeta(operationID, commandID string) *sessiondv1.CommandMeta {
	if operationID == "" {
		b := make([]byte, 16)
		_, _ = rand.Read(b)
		operationID = "op-" + hex.EncodeToString(b)
	}
	meta := &sessiondv1.CommandMeta{
		OperationId:      operationID,
		Canonicalization: sessiondv1.CanonicalizationVersion_CANONICALIZATION_VERSION_LEGACY_JSON_V1,
	}
	if commandID != "" {
		meta.CommandId = &commandID
	}
	return meta
}

func buildSessionRef(agentID, sessionID, connectionID string) *sessiondv1.SessionRef {
	ref := &sessiondv1.SessionRef{
		SessionId: sessionID,
	}
	if agentID != "" {
		ref.AgentId = &agentID
	}
	if connectionID != "" && connectionID != "local" {
		ref.ConnectionId = &connectionID
	}
	return ref
}

func (h *Handler) resolveRuntimeTarget(ctx context.Context, agent map[string]any) (*sessiondv1.RuntimeTarget, error) {
	aid, _ := agent["id"].(string)
	kind, _ := agent["kind"].(string)
	connID, _ := agent["connection_id"].(string)

	target := &sessiondv1.RuntimeTarget{
		AgentId: &aid,
	}
	if connID != "" && connID != "local" {
		target.ConnectionId = &connID
	}

	isSSH := connID != "" && connID != "local"
	switch kind {
	case "codex":
		if isSSH {
			target.Runtime = sessiondv1.RuntimeKind_RUNTIME_KIND_CODEX_SSH
		} else {
			target.Runtime = sessiondv1.RuntimeKind_RUNTIME_KIND_CODEX
		}
	case "hermes":
		if isSSH {
			target.Runtime = sessiondv1.RuntimeKind_RUNTIME_KIND_SSH_HERMES
		} else {
			target.Runtime = sessiondv1.RuntimeKind_RUNTIME_KIND_HERMES
		}
	case "grok":
		if isSSH {
			target.Runtime = sessiondv1.RuntimeKind_RUNTIME_KIND_GROK_SSH
		} else {
			target.Runtime = sessiondv1.RuntimeKind_RUNTIME_KIND_GROK
		}
	default:
		return nil, fmt.Errorf("unsupported agent kind: %s", kind)
	}

	if isSSH {
		sshRow, err := h.options.Store.GetSSHConnection(ctx, connID)
		if err != nil || sshRow == nil {
			return nil, fmt.Errorf("ssh connection %s not found: %w", connID, err)
		}
		settings, _ := sshRow["settings"].(map[string]any)
		host, _ := settings["host"].(string)
		portNum := 22
		if pVal, ok := settings["port"]; ok {
			switch p := pVal.(type) {
			case int:
				portNum = p
			case int64:
				if p < 1 || p > 65535 {
					return nil, fmt.Errorf("invalid SSH port")
				}
				portNum = int(p)
			case json.Number:
				if n, err := strconv.Atoi(p.String()); err == nil {
					portNum = n
				} else {
					return nil, fmt.Errorf("invalid SSH port")
				}
			case string:
				if n, err := strconv.Atoi(p); err == nil {
					portNum = n
				} else {
					return nil, fmt.Errorf("invalid SSH port")
				}
			default:
				return nil, fmt.Errorf("invalid SSH port")
			}
		}
		if portNum < 1 || portNum > 65535 {
			return nil, fmt.Errorf("invalid SSH port")
		}
		sshConf := &sessiondv1.SshConnectConfig{
			Host: host,
			Port: uint32(portNum),
		}
		if u, ok := settings["user"].(string); ok && u != "" {
			sshConf.User = &u
		}
		if a, ok := settings["ssh_config_alias"].(string); ok && a != "" {
			sshConf.SshConfigAlias = &a
		}
		if idf, ok := settings["identity_file"].(string); ok && idf != "" {
			sshConf.IdentityFile = &idf
		}
		if ws, ok := settings["workspace"].(string); ok && ws != "" {
			sshConf.Workspace = &ws
		} else {
			defaultWs := "/home/luwei"
			sshConf.Workspace = &defaultWs
		}
		if ce, ok := settings["codex_executable"].(string); ok && ce != "" {
			sshConf.CodexExecutable = &ce
		} else if kind == "codex" {
			defaultCodex := "/home/luwei/.vite-plus/bin/codex"
			sshConf.CodexExecutable = &defaultCodex
		}
		if ge, ok := settings["grok_executable"].(string); ok && ge != "" {
			sshConf.GrokExecutable = &ge
		} else if kind == "grok" {
			defaultGrok := "/home/luwei/.local/bin/grok"
			sshConf.GrokExecutable = &defaultGrok
		}
		if hp, ok := settings["hermes_path"].(string); ok && hp != "" {
			sshConf.HermesPath = &hp
		} else if kind == "hermes" {
			defaultHermes := "/home/luwei/.local/bin/hermes"
			sshConf.HermesPath = &defaultHermes
		}
		target.Ssh = sshConf
	}
	return target, nil
}

// SessionSender sends instructions to sessions.
func (h *Handler) Send(ctx context.Context, sessionKey, text string) error {
	_, err := h.invokeSessionsSend(ctx, map[string]any{
		"key":  sessionKey,
		"text": text,
	})
	return err
}

func parseSessionKey(payload map[string]any) (string, string, error) {
	if k, ok := payload["key"].(string); ok && strings.Contains(k, "::") {
		parts := strings.SplitN(k, "::", 2)
		aid, sid := parts[0], parts[1]
		if aid != "" && sid != "" {
			return aid, sid, nil
		}
	}
	aid, _ := payload["agent_id"].(string)
	sid, _ := payload["session_id"].(string)
	if aid != "" && sid != "" {
		return aid, sid, nil
	}
	return "", "", NewAgentApiError("invalid_input", "Provide key (agent_id::session_id) or agent_id and session_id")
}

func sessionKey(session map[string]any) string {
	aid, _ := session["agent_id"].(string)
	id, _ := session["id"].(string)
	return aid + "::" + id
}

func publicSession(session map[string]any) map[string]any {
	var parentKey any
	parentAid, _ := session["parent_agent_id"].(string)
	parentSid, _ := session["parent_session_id"].(string)
	if parentAid != "" && parentSid != "" {
		parentKey = parentAid + "::" + parentSid
	} else if parentSid != "" {
		aid, _ := session["agent_id"].(string)
		parentKey = aid + "::" + parentSid
	}

	title, _ := session["title"].(string)
	ephemeral, _ := session["ephemeral"].(bool)

	return map[string]any{
		"key":                sessionKey(session),
		"id":                 session["id"],
		"agent_id":           session["agent_id"],
		"title":              title,
		"status":             session["status"],
		"updated_at":         session["updated_at"],
		"project_name":       session["project_name"],
		"parent_session_key": parentKey,
		"parent_session_id":  session["parent_session_id"],
		"parent_agent_id":    session["parent_agent_id"],
		"ephemeral":          ephemeral,
	}
}

func publicAgent(agent map[string]any) map[string]any {
	caps, _ := agent["capabilities"].([]any)
	if caps == nil {
		caps = []any{}
	}
	return map[string]any{
		"id":            agent["id"],
		"kind":          agent["kind"],
		"name":          agent["name"],
		"status":        agent["status"],
		"source_id":     agent["source_id"],
		"connection_id": agent["connection_id"],
		"runtime_id":    agent["runtime_id"],
		"capabilities":  caps,
	}
}

func publicMessage(item map[string]any) map[string]any {
	text, _ := item["text"].(string)
	runes := []rune(text)
	if len(runes) > 8000 {
		text = string(runes[:8000]) + "... (truncated)"
	}
	return map[string]any{
		"id":         item["id"],
		"role":       item["role"],
		"kind":       item["kind"],
		"text":       text,
		"created_at": item["created_at"],
	}
}

// currentAgents implements current_agents filtering:
// filters out agents if connection_id is set but not in list_ssh_connections,
// status is 'disconnected', and agent id is not used in any session.
func (h *Handler) currentAgents(ctx context.Context) ([]map[string]any, error) {
	agents, err := h.options.Store.ListAgents(ctx)
	if err != nil {
		return nil, err
	}
	connections, err := h.options.Store.ListSSHConnections(ctx)
	if err != nil {
		return nil, err
	}
	connIDs := make(map[string]bool, len(connections))
	for _, c := range connections {
		if id, ok := c["id"].(string); ok {
			connIDs[id] = true
		}
	}

	allSessions, err := h.options.Store.ListSessions(ctx, "")
	if err != nil {
		return nil, err
	}
	usedAgentIDs := make(map[string]bool, len(allSessions))
	for _, s := range allSessions {
		if aid, ok := s["agent_id"].(string); ok {
			usedAgentIDs[aid] = true
		}
	}

	var result []map[string]any
	for _, a := range agents {
		aid, _ := a["id"].(string)
		cid, _ := a["connection_id"].(string)
		status, _ := a["status"].(string)

		if cid != "" && !connIDs[cid] && status == "disconnected" && !usedAgentIDs[aid] {
			continue
		}
		result = append(result, a)
	}
	return result, nil
}

// ---------------- Handlers for 40 capabilities ---------------- //

func (h *Handler) invokeCatalogList(ctx context.Context, payload map[string]any) (any, error) {
	return map[string]any{"items": Capabilities}, nil
}

func (h *Handler) invokeSessionsList(ctx context.Context, payload map[string]any) (any, error) {
	var agentIDFilter string
	if aid, ok := payload["agent_id"]; ok && aid != nil {
		s, ok := aid.(string)
		if !ok {
			return nil, NewAgentApiError("invalid_input", "agent_id must be a string")
		}
		agentIDFilter = s
	}

	limit := 30
	if rawLimit, ok := payload["limit"]; ok && rawLimit != nil {
		n, err := toInt(rawLimit)
		if err != nil {
			return nil, NewAgentApiError("invalid_input", "limit must be an integer")
		}
		limit = n
	}
	if limit < 1 {
		limit = 1
	} else if limit > 200 {
		limit = 200
	}

	offset := 0
	if rawOffset, ok := payload["offset"]; ok && rawOffset != nil {
		n, err := toInt(rawOffset)
		if err != nil {
			return nil, NewAgentApiError("invalid_input", "offset must be an integer")
		}
		offset = n
	}
	if offset < 0 {
		offset = 0
	}

	var ephemeralFilter *bool
	if rawEph, ok := payload["ephemeral"]; ok && rawEph != nil {
		switch v := rawEph.(type) {
		case bool:
			ephemeralFilter = &v
		case string:
			low := strings.ToLower(v)
			if low == "true" || low == "1" {
				b := true
				ephemeralFilter = &b
			} else if low == "false" || low == "0" {
				b := false
				ephemeralFilter = &b
			} else {
				return nil, NewAgentApiError("invalid_input", "ephemeral must be a boolean")
			}
		default:
			return nil, NewAgentApiError("invalid_input", "ephemeral must be a boolean")
		}
	}

	rows, err := h.options.Store.ListSessions(ctx, agentIDFilter)
	if err != nil {
		return nil, err
	}

	var filtered []map[string]any
	for _, r := range rows {
		if ephemeralFilter != nil {
			eph, _ := r["ephemeral"].(bool)
			if eph != *ephemeralFilter {
				continue
			}
		}
		filtered = append(filtered, r)
	}

	total := len(filtered)
	sliced := []map[string]any{}
	if offset < total {
		end := offset + limit
		if end > total {
			end = total
		}
		sliced = filtered[offset:end]
	}

	items := make([]map[string]any, len(sliced))
	for i, r := range sliced {
		items[i] = publicSession(r)
	}

	return map[string]any{
		"items":    items,
		"total":    total,
		"offset":   offset,
		"limit":    limit,
		"has_more": total > (offset + limit),
	}, nil
}

func (h *Handler) invokeSessionsSearch(ctx context.Context, payload map[string]any) (any, error) {
	q, _ := payload["q"].(string)
	if strings.TrimSpace(q) == "" {
		return nil, NewAgentApiError("invalid_input", "q is required")
	}
	needle := strings.ToLower(strings.TrimSpace(q))

	var agentIDFilter string
	if aid, ok := payload["agent_id"].(string); ok {
		agentIDFilter = aid
	}

	limit := 20
	if rawLimit, ok := payload["limit"]; ok && rawLimit != nil {
		n, err := toInt(rawLimit)
		if err != nil {
			return nil, NewAgentApiError("invalid_input", "limit must be an integer")
		}
		limit = n
	}
	if limit < 1 {
		limit = 1
	} else if limit > 50 {
		limit = 50
	}

	rows, err := h.options.Store.ListSessions(ctx, agentIDFilter)
	if err != nil {
		return nil, err
	}

	var matched []map[string]any
	for _, r := range rows {
		hay := strings.ToLower(fmt.Sprintf("%v %v %v %v %v %v",
			r["title"], r["id"], r["agent_id"], r["workspace"], r["project_name"], r["source_id"]))
		if strings.Contains(hay, needle) {
			matched = append(matched, publicSession(r))
		}
	}

	total := len(matched)
	sliced := matched
	if len(sliced) > limit {
		sliced = sliced[:limit]
	}

	return map[string]any{
		"items":    sliced,
		"q":        strings.TrimSpace(q),
		"total":    total,
		"has_more": total > limit,
	}, nil
}

func (h *Handler) invokeSessionsRead(ctx context.Context, payload map[string]any) (any, error) {
	agentID, sessionID, err := parseSessionKey(payload)
	if err != nil {
		return nil, err
	}

	sess, err := h.options.Store.GetSession(ctx, agentID, sessionID)
	if err != nil {
		return nil, err
	}
	if sess == nil {
		return nil, NewAgentApiError("not_found", "Session was not found")
	}

	limit := 20
	if rawLimit, ok := payload["limit"]; ok && rawLimit != nil {
		n, err := toInt(rawLimit)
		if err != nil {
			return nil, NewAgentApiError("invalid_input", "limit must be an integer")
		}
		limit = n
	}
	if limit < 1 {
		limit = 1
	} else if limit > 50 {
		limit = 50
	}

	var beforeStr *string
	if b, ok := payload["before"].(string); ok && strings.TrimSpace(b) != "" {
		trimmed := strings.TrimSpace(b)
		beforeStr = &trimmed
	}

	var items []map[string]any
	var nextCursor any

	// Try reading via gRPC Runtime.GetHistoryPage if Client is present
	if h.options.Client != nil && h.options.Client.Runtime != nil {
		var reqBefore *sessiondv1.NullableString
		if beforeStr != nil {
			reqBefore = &sessiondv1.NullableString{State: &sessiondv1.NullableString_Value{Value: *beforeStr}}
		}
		resp, rErr := h.options.Client.Runtime.GetHistoryPage(ctx, &sessiondv1.HistoryPageRequest{
			Session: &sessiondv1.SessionRef{
				SessionId: sessionID,
				AgentId:   &agentID,
			},
			Limit:  uint32(limit),
			Before: reqBefore,
		})
		if rErr == nil && resp != nil {
			for _, it := range resp.Items {
				if it != nil && it.Utf8Json != nil {
					var itemMap map[string]any
					if json.Unmarshal(it.Utf8Json, &itemMap) == nil {
						items = append(items, publicMessage(itemMap))
					}
				}
			}
			if resp.NextCursor != nil && resp.NextCursor.GetValue() != "" {
				nextCursor = resp.NextCursor.GetValue()
			}
		}
	}

	// Fallback to store.ListMessages
	if len(items) == 0 {
		b := ""
		if beforeStr != nil {
			b = *beforeStr
		}
		stored, cursor, lErr := h.options.Store.ListMessages(ctx, agentID, sessionID, b, limit)
		if lErr == nil {
			for _, m := range stored {
				items = append(items, publicMessage(m))
			}
			if cursor != "" {
				nextCursor = cursor
			}
		}
	}

	return map[string]any{
		"session":     publicSession(sess),
		"items":       items,
		"next_cursor": nextCursor,
		"has_more":    nextCursor != nil && nextCursor != "",
		"count":       len(items),
	}, nil
}

func (h *Handler) invokeAgentsList(ctx context.Context, payload map[string]any) (any, error) {
	agents, err := h.currentAgents(ctx)
	if err != nil {
		return nil, err
	}
	items := make([]map[string]any, len(agents))
	for i, a := range agents {
		items[i] = publicAgent(a)
	}
	return map[string]any{"items": items}, nil
}

func (h *Handler) invokeMachinesList(ctx context.Context, payload map[string]any) (any, error) {
	local := map[string]any{
		"id":           "local",
		"kind":         "local",
		"display_name": "本机",
		"state":        "ready",
	}

	var remote []map[string]any
	conns, err := h.options.Store.ListSSHConnections(ctx)
	if err == nil {
		for _, row := range conns {
			settings, _ := row["settings"].(map[string]any)
			var host any
			if settings != nil {
				host = settings["host"]
			}
			remote = append(remote, map[string]any{
				"id":           row["id"],
				"kind":         "ssh",
				"display_name": row["display_name"],
				"state":        row["state"],
				"host":         host,
				"agent_id":     row["agent_id"],
			})
		}
	}

	items := []map[string]any{local}
	items = append(items, remote...)
	return map[string]any{"items": items}, nil
}

func (h *Handler) invokeProjectsList(ctx context.Context, payload map[string]any) (any, error) {
	projects, err := h.options.Store.ListProjects(ctx)
	if err != nil {
		return nil, err
	}
	items := make([]map[string]any, len(projects))
	for i, row := range projects {
		items[i] = map[string]any{
			"id":            row["id"],
			"project_name":  row["project_name"],
			"workspace":     row["workspace"],
			"agent_id":      row["agent_id"],
			"source_id":     row["source_id"],
			"connection_id": row["connection_id"],
			"session_count": row["session_count"],
		}
	}
	return map[string]any{"items": items}, nil
}

func (h *Handler) invokeSessionsCreate(ctx context.Context, payload map[string]any) (any, error) {
	rawAid, _ := payload["agent_id"].(string)
	agentID := rawAid
	if agentID == "" {
		return nil, NewAgentApiError("invalid_input", "agent_id is required")
	}

	agent, err := h.options.Store.GetAgent(ctx, agentID)
	if err != nil {
		return nil, err
	}
	if agent == nil {
		return nil, NewAgentApiError("not_found", fmt.Sprintf("Agent '%s' was not found", agentID))
	}

	title, _ := payload["title"].(string)
	if title == "" {
		title = "新会话"
	}
	workspace, _ := payload["workspace"].(string)
	if workspace == "" {
		workspace = h.options.DefaultWorkspace
	}
	if workspace == "" {
		return nil, NewAgentApiError("invalid_input", "workspace is required and no default workspace configured")
	}

	var parentAid, parentSid string
	if parentKey, ok := payload["parent_key"].(string); ok && strings.Contains(parentKey, "::") {
		parts := strings.SplitN(parentKey, "::", 2)
		parentAid, parentSid = parts[0], parts[1]
	} else {
		parentAid, _ = payload["parent_agent_id"].(string)
		parentSid, _ = payload["parent_session_id"].(string)
	}

	ephemeral := false
	if rawEph, ok := payload["ephemeral"]; ok && rawEph != nil {
		switch v := rawEph.(type) {
		case bool:
			ephemeral = v
		case string:
			low := strings.ToLower(v)
			ephemeral = (low == "true" || low == "1")
		}
	}

	sid := ""
	sourceSessionID := ""
	status := "idle"
	controlState := "owned"

	if h.options.Client != nil && h.options.Client.Session != nil {
		target, rErr := h.resolveRuntimeTarget(ctx, agent)
		if rErr != nil {
			return nil, NewAgentApiError("runtime_error", rErr.Error())
		}
		createReq := &sessiondv1.CreateSessionRequest{
			Meta:   newCommandMeta("", ""),
			Target: target,
		}
		var pSidPtr *string
		if parentSid != "" {
			pSidPtr = &parentSid
		}
		kind, _ := agent["kind"].(string)
		switch kind {
		case "codex":
			createReq.Options = &sessiondv1.CreateSessionRequest_Codex{
				Codex: &sessiondv1.CodexCreateOptions{
					Cwd:             workspace,
					ParentSessionId: pSidPtr,
				},
			}
		case "hermes":
			createReq.Options = &sessiondv1.CreateSessionRequest_Hermes{
				Hermes: &sessiondv1.HermesCreateOptions{
					ParentSessionId: pSidPtr,
				},
			}
		case "grok":
			createReq.Options = &sessiondv1.CreateSessionRequest_Grok{
				Grok: &sessiondv1.GrokCreateOptions{
					Cwd: workspace,
				},
			}
		}

		receipt, cErr := h.options.Client.Session.CreateSession(ctx, createReq)
		if cErr != nil {
			return nil, NewAgentApiError("command_rejected", cErr.Error())
		}
		if receipt == nil || !receipt.Accepted || receipt.Session == nil || receipt.Session.SessionId == "" {
			return nil, NewAgentApiError("command_rejected", "daemon rejected session creation")
		}
		if receipt.Session.AgentId != nil && *receipt.Session.AgentId != "" && *receipt.Session.AgentId != agentID {
			return nil, NewAgentApiError("command_rejected", fmt.Sprintf("daemon returned foreign agent_id '%s' for requested '%s'", *receipt.Session.AgentId, agentID))
		}
		sid = receipt.Session.SessionId
		sourceSessionID = sid
		switch receipt.Status {
		case sessiondv1.SessionStatus_SESSION_STATUS_RUNNING:
			status = "running"
		case sessiondv1.SessionStatus_SESSION_STATUS_IDLE:
			status = "idle"
		case sessiondv1.SessionStatus_SESSION_STATUS_WAITING_APPROVAL:
			status = "waiting_approval"
		case sessiondv1.SessionStatus_SESSION_STATUS_ERROR:
			status = "error"
		default:
			return nil, NewAgentApiError("runtime_error", fmt.Sprintf("unknown daemon session status: %v", receipt.Status))
		}
	} else {
		return nil, NewAgentApiError("unavailable", "daemon client is required for session creation")
	}

	sourceID, _ := agent["source_id"].(string)
	if sourceID == "" {
		sourceID = agentID
	}

	connID, _ := agent["connection_id"].(string)
	kind, _ := agent["kind"].(string)

	sessData := map[string]any{
		"id":                sid,
		"agent_id":          agentID,
		"title":             title,
		"workspace":         workspace,
		"status":            status,
		"source_id":         sourceID,
		"connection_id":     connID,
		"native_kind":       kind,
		"source_session_id": sourceSessionID,
		"history_state":     "available",
		"control_state":     controlState,
		"updated_at":        time.Now().UTC().Format(time.RFC3339Nano),
		"ephemeral":         ephemeral,
	}

	if parentSid != "" {
		sessData["parent_session_id"] = parentSid
		sessData["handoff_from_session_id"] = parentSid
	}
	if parentAid != "" {
		sessData["parent_agent_id"] = parentAid
		sessData["handoff_from_agent_id"] = parentAid
	}

	canonical, err := h.options.Store.UpsertSession(ctx, sessData)
	if err != nil {
		return nil, err
	}

	sessionNamespace := fmt.Sprintf("session:%s::%s", agentID, sid)
	if parentSid != "" {
		rootAid := parentAid
		if rootAid == "" {
			rootAid = agentID
		}
		rootSid := parentSid
		visited := make(map[string]bool)
		for {
			rk := rootAid + "::" + rootSid
			if visited[rk] {
				break
			}
			visited[rk] = true
			parent, _ := h.options.Store.GetSession(ctx, rootAid, rootSid)
			if parent == nil || parent["parent_session_id"] == nil || parent["parent_session_id"] == "" {
				break
			}
			pAid, _ := parent["parent_agent_id"].(string)
			if pAid != "" {
				rootAid = pAid
			}
			rootSid, _ = parent["parent_session_id"].(string)
		}
		sessionNamespace = fmt.Sprintf("swarm:%s::%s", rootAid, rootSid)
		_ = h.options.Store.SetPreference(ctx, fmt.Sprintf("blackboard_scope:%s::%s", rootAid, rootSid), sessionNamespace, true)
		if parentAid != "" {
			_ = h.options.Store.SetPreference(ctx, fmt.Sprintf("blackboard_scope:%s::%s", parentAid, parentSid), sessionNamespace, true)
		}
	}
	_ = h.options.Store.SetPreference(ctx, fmt.Sprintf("blackboard_scope:%s::%s", agentID, sid), sessionNamespace, true)

	if h.options.Publisher != nil {
		_ = h.options.Publisher.Publish(ctx, "session.upsert", canonical)
	}

	return map[string]any{"session": publicSession(canonical)}, nil
}

func (h *Handler) invokeSessionsDelete(ctx context.Context, payload map[string]any) (any, error) {
	var targets [][2]string
	if rawKeys, ok := payload["keys"].([]any); ok {
		for _, k := range rawKeys {
			if str, ok := k.(string); ok && strings.Contains(str, "::") {
				parts := strings.SplitN(str, "::", 2)
				targets = append(targets, [2]string{strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])})
			}
		}
	} else if payload["key"] != nil || (payload["agent_id"] != nil && payload["session_id"] != nil) {
		aid, sid, err := parseSessionKey(payload)
		if err != nil {
			return nil, err
		}
		targets = append(targets, [2]string{aid, sid})
	} else {
		return nil, NewAgentApiError("invalid_input", "key, keys, or agent_id+session_id is required")
	}

	var deleted []map[string]any
	var failed []map[string]any

	for _, target := range targets {
		aid, sid := target[0], target[1]
		if aid == "" || sid == "" {
			continue
		}
		existing, err := h.options.Store.GetSession(ctx, aid, sid)
		if err != nil {
			failed = append(failed, map[string]any{"agent_id": aid, "session_id": sid, "error": err.Error()})
			continue
		}
		if existing == nil {
			failed = append(failed, map[string]any{"agent_id": aid, "session_id": sid, "error": "not_found"})
			continue
		}

		// Daemon client is required for native session deletion
		if h.options.Client == nil || h.options.Client.Session == nil {
			failed = append(failed, map[string]any{"agent_id": aid, "session_id": sid, "error": "unavailable: daemon client is required for session deletion"})
			continue
		}
		connID, _ := existing["connection_id"].(string)
		delReceipt, delErr := h.options.Client.Session.DeleteSession(ctx, &sessiondv1.DeleteSessionRequest{
			Meta:    newCommandMeta("", ""),
			Session: buildSessionRef(aid, sid, connID),
		})
		if delErr != nil {
			failed = append(failed, map[string]any{"agent_id": aid, "session_id": sid, "error": delErr.Error()})
			continue
		}
		if delReceipt == nil || delReceipt.OperationState != sessiondv1.OperationState_OPERATION_STATE_COMPLETED || delReceipt.GetCompleted() == nil {
			failed = append(failed, map[string]any{"agent_id": aid, "session_id": sid, "error": "deletion rejected or uncertain by runtime daemon"})
			continue
		}

		ok, dErr := h.options.Store.DeleteSession(ctx, aid, sid)
		if dErr != nil {
			failed = append(failed, map[string]any{"agent_id": aid, "session_id": sid, "error": dErr.Error()})
		} else if ok {
			deleted = append(deleted, map[string]any{"agent_id": aid, "session_id": sid, "key": aid + "::" + sid})
			if h.options.Publisher != nil {
				_ = h.options.Publisher.Publish(ctx, "session.delete", map[string]any{
					"agent_id":   aid,
					"session_id": sid,
					"id":         sid,
				})
			}
		} else {
			failed = append(failed, map[string]any{"agent_id": aid, "session_id": sid, "error": "delete_failed"})
		}
	}

	return map[string]any{
		"ok":      len(failed) == 0,
		"deleted": deleted,
		"failed":  failed,
		"count":   len(deleted),
	}, nil
}

func (h *Handler) invokeSessionsTree(ctx context.Context, payload map[string]any) (any, error) {
	rootKey, _ := payload["key"].(string)

	rows, err := h.options.Store.ListSessions(ctx, "")
	if err != nil {
		return nil, err
	}

	sessions := make([]map[string]any, len(rows))
	byKey := make(map[string]map[string]any, len(rows))
	childrenMap := make(map[string][]map[string]any)

	for i, r := range rows {
		pub := publicSession(r)
		sessions[i] = pub
		k, _ := pub["key"].(string)
		byKey[k] = pub
	}

	for _, s := range sessions {
		if pkey, ok := s["parent_session_key"].(string); ok && pkey != "" {
			childrenMap[pkey] = append(childrenMap[pkey], s)
		}
	}

	var attachChildren func(node map[string]any, depth int) map[string]any
	attachChildren = func(node map[string]any, depth int) map[string]any {
		k, _ := node["key"].(string)
		children := childrenMap[k]
		var subNodes []map[string]any
		if depth < 10 {
			for _, c := range children {
				copyC := make(map[string]any, len(c))
				for ck, cv := range c {
					copyC[ck] = cv
				}
				subNodes = append(subNodes, attachChildren(copyC, depth+1))
			}
		}
		if subNodes == nil {
			subNodes = []map[string]any{}
		}
		node["children"] = subNodes
		node["satellite_count"] = len(children)
		return node
	}

	if rootKey != "" {
		root, exists := byKey[rootKey]
		if !exists {
			return nil, NewAgentApiError("not_found", fmt.Sprintf("Session '%s' was not found", rootKey))
		}
		copyRoot := make(map[string]any, len(root))
		for k, v := range root {
			copyRoot[k] = v
		}
		return map[string]any{
			"ok":   true,
			"tree": attachChildren(copyRoot, 0),
		}, nil
	}

	var roots []map[string]any
	for _, s := range sessions {
		pkey, _ := s["parent_session_key"].(string)
		if pkey == "" || byKey[pkey] == nil {
			roots = append(roots, s)
		}
	}

	var trees []map[string]any
	var swarmTrees []map[string]any
	for _, r := range roots {
		copyR := make(map[string]any, len(r))
		for k, v := range r {
			copyR[k] = v
		}
		tree := attachChildren(copyR, 0)
		trees = append(trees, tree)
		if sat, ok := tree["satellite_count"].(int); ok && sat > 0 {
			swarmTrees = append(swarmTrees, tree)
		}
	}

	return map[string]any{
		"ok":          true,
		"trees":       trees,
		"swarm_trees": swarmTrees,
		"total_trees": len(trees),
		"swarm_count": len(swarmTrees),
	}, nil
}

func (h *Handler) invokeSessionsSend(ctx context.Context, payload map[string]any) (any, error) {
	agentID, sessionID, err := parseSessionKey(payload)
	if err != nil {
		return nil, err
	}

	sess, err := h.options.Store.GetSession(ctx, agentID, sessionID)
	if err != nil {
		return nil, err
	}
	if sess == nil {
		return nil, NewAgentApiError("not_found", "Session was not found")
	}

	text, _ := payload["text"].(string)
	if text == "" {
		return nil, NewAgentApiError("invalid_input", "text is required")
	}

	commandID := ""
	if cid, ok := payload["command_id"].(string); ok && cid != "" {
		commandID = cid
	} else {
		b := make([]byte, 16)
		_, _ = rand.Read(b)
		commandID = "cmd-" + hex.EncodeToString(b)
	}

	operationID := ""
	if opid, ok := payload["operation_id"].(string); ok && opid != "" {
		operationID = opid
	}

	// If shared CommandSubmit callback is provided by parent, use it
	if h.options.CommandSubmit != nil {
		if operationID != "" && operationID != commandID {
			return nil, NewAgentApiError("invalid_input", "operation_id conflicts with command_id")
		}
		submitPayload := map[string]any{
			"id":             commandID,
			"agent_id":       agentID,
			"session_id":     sessionID,
			"action":         "send",
			"text":           text,
			"attachment_ids": []any{},
			"target_id":      nil,
		}
		res, sErr := h.options.CommandSubmit(ctx, submitPayload)
		if sErr != nil {
			return nil, sErr
		}
		cmdState := "accepted"
		if st, ok := res["state"].(string); ok && st != "" {
			cmdState = st
		}
		retID := commandID
		if cid, ok := res["id"].(string); ok && cid != "" {
			retID = cid
		}
		return map[string]any{
			"command_id":  retID,
			"state":       cmdState,
			"session_key": sessionKey(sess),
		}, nil
	}

	cmdPayload := map[string]any{
		"id":             commandID,
		"agent_id":       agentID,
		"session_id":     sessionID,
		"action":         "send",
		"text":           text,
		"attachment_ids": []any{},
		"target_id":      nil,
	}

	// Persist command before dispatching
	existingCmd, created, cErr := h.options.Store.CreateCommand(ctx, cmdPayload, "received")
	if cErr != nil {
		return nil, cErr
	}
	if !created {
		// Command was already submitted earlier, do not re-dispatch to daemon
		existingState := "received"
		if existingCmd != nil {
			if st, ok := existingCmd["state"].(string); ok && st != "" {
				existingState = st
			}
		}
		return map[string]any{
			"command_id":  commandID,
			"state":       existingState,
			"session_key": sessionKey(sess),
		}, nil
	}

	if h.options.Client == nil || h.options.Client.Session == nil {
		return nil, NewAgentApiError("unavailable", "daemon client is required to send instruction to session")
	}

	connID, _ := sess["connection_id"].(string)
	if connID == "" {
		if agentRow, aErr := h.options.Store.GetAgent(ctx, agentID); aErr == nil && agentRow != nil {
			connID, _ = agentRow["connection_id"].(string)
		}
	}

	opID := operationID
	if opID == "" {
		opID = commandID
	}

	receipt, sendErr := h.options.Client.Session.Send(ctx, &sessiondv1.SendRequest{
		Meta:    newCommandMeta(opID, commandID),
		Session: buildSessionRef(agentID, sessionID, connID),
		Input: []*sessiondv1.InputPart{
			{Part: &sessiondv1.InputPart_Text{Text: &sessiondv1.TextInput{Text: text}}},
		},
	})
	if sendErr != nil {
		_, _ = h.options.Store.SetCommandState(ctx, agentID, sessionID, commandID, "unknown", sendErr.Error(), true)
		if h.options.Publisher != nil {
			_ = h.options.Publisher.Publish(ctx, "command.committed", map[string]any{
				"agent_id":   agentID,
				"session_id": sessionID,
				"command_id": commandID,
				"state":      "unknown",
				"error":      sendErr.Error(),
			})
		}
		return nil, NewAgentApiError("command_rejected", sendErr.Error())
	}
	if receipt != nil && (!receipt.Accepted || receipt.OperationState == sessiondv1.OperationState_OPERATION_STATE_UNCERTAIN) {
		_, _ = h.options.Store.SetCommandState(ctx, agentID, sessionID, commandID, "rejected", "runtime daemon rejected send command or outcome is uncertain", true)
		return nil, NewAgentApiError("command_rejected", "runtime daemon rejected send command or outcome is uncertain")
	}

	state := "accepted"
	if receipt != nil && receipt.Status == sessiondv1.SessionStatus_SESSION_STATUS_RUNNING {
		state = "running"
	}

	updatedCmd, scErr := h.options.Store.SetCommandState(ctx, agentID, sessionID, commandID, state, nil, true)
	if scErr != nil {
		return nil, scErr
	}
	if updatedCmd != nil {
		if st, ok := updatedCmd["state"].(string); ok && st != "" {
			state = st
		}
	}

	if h.options.Publisher != nil {
		_ = h.options.Publisher.Publish(ctx, "command.committed", map[string]any{
			"agent_id":   agentID,
			"session_id": sessionID,
			"command_id": commandID,
			"state":      state,
		})
	}

	return map[string]any{
		"command_id":  commandID,
		"state":       state,
		"session_key": sessionKey(sess),
	}, nil
}

func (h *Handler) invokeSessionsStop(ctx context.Context, payload map[string]any) (any, error) {
	agentID, sessionID, err := parseSessionKey(payload)
	if err != nil {
		return nil, err
	}

	sess, err := h.options.Store.GetSession(ctx, agentID, sessionID)
	if err != nil {
		return nil, err
	}
	if sess == nil {
		return nil, NewAgentApiError("not_found", "Session was not found")
	}

	commandID := ""
	if cid, ok := payload["command_id"].(string); ok && cid != "" {
		commandID = cid
	} else {
		b := make([]byte, 16)
		_, _ = rand.Read(b)
		commandID = "cmd-" + hex.EncodeToString(b)
	}

	// If shared CommandSubmit callback is provided by parent, use it
	if h.options.CommandSubmit != nil {
		submitPayload := map[string]any{
			"id":             commandID,
			"agent_id":       agentID,
			"session_id":     sessionID,
			"action":         "stop",
			"text":           "",
			"attachment_ids": []any{},
			"target_id":      sessionID,
		}
		res, sErr := h.options.CommandSubmit(ctx, submitPayload)
		if sErr != nil {
			return nil, sErr
		}
		cmdState := "accepted"
		if st, ok := res["state"].(string); ok && st != "" {
			cmdState = st
		}
		retID := commandID
		if cid, ok := res["id"].(string); ok && cid != "" {
			retID = cid
		}
		return map[string]any{
			"command_id":  retID,
			"state":       cmdState,
			"session_key": sessionKey(sess),
		}, nil
	}

	if h.options.Client == nil || h.options.Client.Session == nil {
		return nil, NewAgentApiError("unavailable", "daemon client is required to stop session")
	}

	agentRow, _ := h.options.Store.GetAgent(ctx, agentID)
	var connID string
	if agentRow != nil {
		connID, _ = agentRow["connection_id"].(string)
	}

	var turnID string
	if tid, ok := payload["turn_id"].(string); ok && tid != "" {
		turnID = tid
	} else {
		if actTurn, aErr := h.options.Store.ActiveTurn(ctx, agentID, sessionID); aErr == nil && actTurn != "" {
			turnID = actTurn
		}
	}

	cmdPayload := map[string]any{
		"id":             commandID,
		"agent_id":       agentID,
		"session_id":     sessionID,
		"action":         "stop",
		"text":           "",
		"attachment_ids": []any{},
		"target_id":      sessionID,
	}

	_, _, cErr := h.options.Store.CreateCommand(ctx, cmdPayload, "received")
	if cErr != nil {
		return nil, cErr
	}

	intReq := &sessiondv1.InterruptRequest{
		Meta:    newCommandMeta("", commandID),
		Session: buildSessionRef(agentID, sessionID, connID),
	}
	if turnID != "" {
		intReq.TurnId = &turnID
	}

	receipt, intErr := h.options.Client.Session.Interrupt(ctx, intReq)
	if intErr != nil {
		return nil, NewAgentApiError("command_rejected", intErr.Error())
	}
	if receipt != nil && (!receipt.Accepted || receipt.OperationState == sessiondv1.OperationState_OPERATION_STATE_UNCERTAIN) {
		return nil, NewAgentApiError("command_rejected", "runtime daemon rejected interrupt command or outcome is uncertain")
	}

	state := "completed"
	if receipt != nil && receipt.OperationState != sessiondv1.OperationState_OPERATION_STATE_COMPLETED {
		state = "accepted"
	}

	updatedCmd, scErr := h.options.Store.SetCommandState(ctx, agentID, sessionID, commandID, state, nil, true)
	if scErr == nil && updatedCmd != nil {
		if st, ok := updatedCmd["state"].(string); ok && st != "" {
			state = st
		}
	}

	if h.options.Publisher != nil {
		_ = h.options.Publisher.Publish(ctx, "command.update", map[string]any{
			"agent_id":   agentID,
			"session_id": sessionID,
			"command_id": commandID,
			"state":      state,
		})
	}

	return map[string]any{
		"command_id":  commandID,
		"state":       state,
		"session_key": sessionKey(sess),
	}, nil
}

func (h *Handler) invokePluginsList(ctx context.Context, payload map[string]any) (any, error) {
	settings := make(map[string]any)
	pref, err := h.options.Store.GetPreference(ctx, "plugins:settings")
	if err == nil && pref.Exists {
		if m, ok := pref.Value.(map[string]any); ok {
			settings = m
		}
	}

	items := make([]map[string]any, len(KnownPlugins))
	for i, p := range KnownPlugins {
		pid, _ := p["id"].(string)
		override, _ := settings[pid].(map[string]any)
		enabled := true
		var config any = map[string]any{}
		if override != nil {
			if e, ok := override["enabled"].(bool); ok {
				enabled = e
			}
			if c, ok := override["config"]; ok && c != nil {
				config = c
			}
		}

		cp := make(map[string]any, len(p)+2)
		for k, v := range p {
			cp[k] = v
		}
		cp["enabled"] = enabled
		cp["config"] = config
		items[i] = cp
	}

	return map[string]any{"items": items, "count": len(items)}, nil
}

func (h *Handler) invokePluginsConfigure(ctx context.Context, payload map[string]any) (any, error) {
	rawPid, _ := payload["plugin_id"].(string)
	pluginID := strings.TrimSpace(rawPid)
	if pluginID == "" {
		return nil, NewAgentApiError("invalid_input", "plugin_id is required")
	}

	var known map[string]any
	for _, p := range KnownPlugins {
		if p["id"] == pluginID {
			known = p
			break
		}
	}
	if known == nil {
		return nil, NewAgentApiError("not_found", fmt.Sprintf("Plugin '%s' was not found in registered plugins", pluginID))
	}

	settings := make(map[string]any)
	err := h.options.Store.WithTx(ctx, func(tx *sql.Tx) error {
		row := tx.QueryRowContext(ctx, `SELECT value FROM workspace_preferences WHERE "key"='plugins:settings'`)
		var stored string
		if scanErr := row.Scan(&stored); scanErr == nil {
			_ = json.Unmarshal([]byte(stored), &settings)
		}

		current, _ := settings[pluginID].(map[string]any)
		if current == nil {
			current = map[string]any{"enabled": true, "config": map[string]any{}}
		}
		if rawEn, ok := payload["enabled"]; ok && rawEn != nil {
			if b, ok := rawEn.(bool); ok {
				current["enabled"] = b
			}
		}
		if cfg, ok := payload["config"].(map[string]any); ok {
			curCfg, _ := current["config"].(map[string]any)
			if curCfg == nil {
				curCfg = make(map[string]any)
			}
			for k, v := range cfg {
				curCfg[k] = v
			}
			current["config"] = curCfg
		}
		settings[pluginID] = current

		encoded, mErr := json.Marshal(settings)
		if mErr != nil {
			return mErr
		}
		_, execErr := tx.ExecContext(ctx, `INSERT INTO workspace_preferences("key", value) VALUES('plugins:settings', ?) ON CONFLICT("key") DO UPDATE SET value=excluded.value`, string(encoded))
		return execErr
	})

	if err != nil {
		return nil, NewAgentApiError("database_error", fmt.Sprintf("Failed to persist plugin settings: %v", err))
	}

	current, _ := settings[pluginID].(map[string]any)
	pluginRes := make(map[string]any, len(known)+2)
	for k, v := range known {
		pluginRes[k] = v
	}
	for k, v := range current {
		pluginRes[k] = v
	}

	return map[string]any{"plugin": pluginRes, "ok": true}, nil
}

func (h *Handler) invokePluginsOpen(ctx context.Context, payload map[string]any) (any, error) {
	rawPid, _ := payload["plugin_id"].(string)
	pluginID := strings.TrimSpace(rawPid)
	if pluginID == "" {
		return nil, NewAgentApiError("invalid_input", "plugin_id is required")
	}

	sessionKeyVal, _ := payload["session_key"].(string)
	var agentID, sessionID any
	if strings.Contains(sessionKeyVal, "::") {
		parts := strings.SplitN(sessionKeyVal, "::", 2)
		agentID, sessionID = parts[0], parts[1]
	} else if payload["session_id"] != nil {
		sessionID = fmt.Sprintf("%v", payload["session_id"])
		agentID = fmt.Sprintf("%v", payload["agent_id"])
	} else {
		aid := payload["agent_id"]
		if aid == nil {
			aid = payload["caller_agent_id"]
		}
		if aid != nil && fmt.Sprintf("%v", aid) != "" {
			agentID = fmt.Sprintf("%v", aid)
		}
	}

	var normPath any
	if rawPath, ok := payload["path"].(string); ok && rawPath != "" {
		normPath = strings.ReplaceAll(rawPath, "\\", "/")
	}

	urlVal := payload["url"]
	if normStr, ok := normPath.(string); ok && normStr != "" && urlVal == nil {
		low := strings.ToLower(normStr)
		if pluginID == "html" || strings.HasSuffix(low, ".html") || strings.HasSuffix(low, ".htm") {
			urlVal = "/api/v1/files/raw?path=" + normStr
		}
	}

	eventData := map[string]any{
		"action":     "open",
		"plugin_id":  pluginID,
		"agent_id":   agentID,
		"session_id": sessionID,
		"path":       normPath,
		"url":        urlVal,
		"title":      payload["title"],
	}

	if h.options.Publisher != nil {
		_ = h.options.Publisher.Publish(ctx, "sidecar.plugin.control", eventData)
	}

	return map[string]any{"ok": true, "event": eventData}, nil
}

func (h *Handler) invokePluginsClose(ctx context.Context, payload map[string]any) (any, error) {
	collapse, _ := payload["collapse"].(bool)
	eventData := map[string]any{
		"action":    "close",
		"plugin_id": payload["plugin_id"],
		"tab_id":    payload["tab_id"],
		"collapse":  collapse,
	}

	if h.options.Publisher != nil {
		_ = h.options.Publisher.Publish(ctx, "sidecar.plugin.control", eventData)
	}

	return map[string]any{"ok": true, "event": eventData}, nil
}

func (h *Handler) invokeMachinesDispatch(ctx context.Context, payload map[string]any) (any, error) {
	rawMid, _ := payload["machine_id"].(string)
	machineID := strings.TrimSpace(rawMid)
	if machineID == "" {
		return nil, NewAgentApiError("invalid_input", "machine_id is required")
	}

	var agentKind string
	if k, ok := payload["agent_kind"].(string); ok {
		agentKind = strings.ToLower(strings.TrimSpace(k))
	}

	agents, err := h.currentAgents(ctx)
	if err != nil {
		return nil, err
	}

	var candidate map[string]any
	if machineID == "local" {
		for _, a := range agents {
			idStr, _ := a["id"].(string)
			if !strings.HasPrefix(idStr, "ssh-") {
				kindStr, _ := a["kind"].(string)
				if agentKind == "" || kindStr == agentKind {
					candidate = a
					break
				}
			}
		}
	} else {
		for _, a := range agents {
			cid, _ := a["connection_id"].(string)
			aid, _ := a["id"].(string)
			if cid == machineID || aid == machineID || strings.HasSuffix(aid, machineID) {
				kindStr, _ := a["kind"].(string)
				if agentKind == "" || kindStr == agentKind {
					candidate = a
					break
				}
			}
		}
	}

	if candidate == nil {
		return nil, NewAgentApiError("not_found", fmt.Sprintf("No matching agent found on machine '%s'", machineID))
	}

	callerAid, _ := payload["caller_agent_id"].(string)
	callerSid, _ := payload["caller_session_id"].(string)
	parentSid, _ := payload["parent_session_id"].(string)
	if parentSid == "" {
		parentSid = callerSid
	}
	parentAid, _ := payload["parent_agent_id"].(string)
	if parentAid == "" {
		parentAid = callerAid
	}
	if pKey, ok := payload["parent_key"].(string); ok && strings.Contains(pKey, "::") {
		parts := strings.SplitN(pKey, "::", 2)
		parentAid, parentSid = parts[0], parts[1]
	}

	title, _ := payload["title"].(string)
	if title == "" {
		title = fmt.Sprintf("远程派生任务 (%s)", machineID)
	}

	candID, _ := candidate["id"].(string)
	createPayload := map[string]any{
		"agent_id":          candID,
		"title":             title,
		"workspace":         payload["workspace"],
		"parent_session_id": parentSid,
		"parent_agent_id":   parentAid,
		"parent_key":        payload["parent_key"],
	}

	createRes, err := h.invokeSessionsCreate(ctx, createPayload)
	if err != nil {
		return nil, err
	}

	var createdSession map[string]any
	if cm, ok := createRes.(map[string]any); ok {
		createdSession, _ = cm["session"].(map[string]any)
	}

	var sentResult any
	if prompt, ok := payload["prompt"].(string); ok && strings.TrimSpace(prompt) != "" {
		sKey, _ := createdSession["key"].(string)
		sentResult, _ = h.invokeSessionsSend(ctx, map[string]any{
			"key":  sKey,
			"text": strings.TrimSpace(prompt),
		})
	}

	return map[string]any{
		"ok":         true,
		"machine_id": machineID,
		"agent":      publicAgent(candidate),
		"session":    createdSession,
		"sent":       sentResult,
	}, nil
}

func (h *Handler) invokeMonitorSessionsAdd(ctx context.Context, payload map[string]any) (any, error) {
	var keysToAdd []string
	if rawKeys, ok := payload["keys"].([]any); ok {
		for _, k := range rawKeys {
			if str, ok := k.(string); ok && strings.TrimSpace(str) != "" {
				keysToAdd = append(keysToAdd, strings.TrimSpace(str))
			}
		}
	} else if rawKey, ok := payload["key"].(string); ok && strings.TrimSpace(rawKey) != "" {
		keysToAdd = append(keysToAdd, strings.TrimSpace(rawKey))
	} else if payload["agent_id"] != nil && payload["session_id"] != nil {
		keysToAdd = append(keysToAdd, fmt.Sprintf("%v::%v", payload["agent_id"], payload["session_id"]))
	} else {
		return nil, NewAgentApiError("invalid_input", "key or keys list is required")
	}

	if h.options.Publisher != nil {
		_ = h.options.Publisher.Publish(ctx, "monitor.control", map[string]any{
			"action": "add_sessions",
			"keys":   keysToAdd,
		})
	}

	return map[string]any{"ok": true, "added_keys": keysToAdd}, nil
}

func (h *Handler) invokeMonitorSessionsRemove(ctx context.Context, payload map[string]any) (any, error) {
	var key string
	aid, sid, err := parseSessionKey(payload)
	if err == nil {
		key = aid + "::" + sid
	} else if rawKey, ok := payload["key"].(string); ok && strings.TrimSpace(rawKey) != "" {
		key = strings.TrimSpace(rawKey)
	} else {
		return nil, NewAgentApiError("invalid_input", "key is required")
	}

	if h.options.Publisher != nil {
		_ = h.options.Publisher.Publish(ctx, "monitor.control", map[string]any{
			"action": "remove_session",
			"key":    key,
		})
	}

	return map[string]any{"ok": true, "removed_key": key}, nil
}

func (h *Handler) invokeMonitorLayoutSet(ctx context.Context, payload map[string]any) (any, error) {
	rawCols := payload["columns"]
	cols, err := toInt(rawCols)
	if err != nil {
		return nil, NewAgentApiError("invalid_input", "columns must be an integer (1-4)")
	}
	if cols < 1 {
		cols = 1
	} else if cols > 4 {
		cols = 4
	}

	if h.options.Publisher != nil {
		_ = h.options.Publisher.Publish(ctx, "monitor.control", map[string]any{
			"action":  "set_layout",
			"columns": cols,
		})
	}

	return map[string]any{"ok": true, "columns": cols}, nil
}

func (h *Handler) invokeBlackboardComponentSchema(ctx context.Context, payload map[string]any) (any, error) {
	rawCid, _ := payload["component_id"].(string)
	cid := strings.TrimSpace(rawCid)
	if cid == "" {
		return nil, NewAgentApiError("invalid_input", "component_id is required")
	}

	for k, v := range blackboard.Schemas {
		if strings.EqualFold(k, cid) {
			return map[string]any{
				"ok":           true,
				"component_id": v.ComponentID,
				"title":        v.Title,
				"schema":       v.Schema,
				"example":      v.Example,
			}, nil
		}
	}

	return nil, NewAgentApiError("not_found", fmt.Sprintf("Component '%s' not found. Call blackboard_components_list to see all valid IDs.", cid))
}

func (h *Handler) resolveBrowserSessionKey(ctx context.Context, payload map[string]any) (string, error) {
	rawKey, _ := payload["session_key"].(string)
	if strings.Contains(rawKey, "::") {
		parts := strings.SplitN(rawKey, "::", 2)
		sess, _ := h.options.Store.GetSession(ctx, parts[0], parts[1])
		if sess != nil {
			return rawKey, nil
		}
		return "", NewAgentApiError("not_found", fmt.Sprintf("Session '%s' was not found", rawKey))
	}

	aid, _ := payload["caller_agent_id"].(string)
	if aid == "" {
		aid, _ = payload["agent_id"].(string)
	}
	sid, _ := payload["caller_session_id"].(string)
	if sid == "" {
		sid, _ = payload["session_id"].(string)
	}

	if aid != "" && sid != "" {
		sess, _ := h.options.Store.GetSession(ctx, aid, sid)
		if sess != nil {
			return aid + "::" + sid, nil
		}
	}

	if aid != "" {
		sessions, _ := h.options.Store.ListSessions(ctx, aid)
		var active []map[string]any
		for _, s := range sessions {
			st, _ := s["status"].(string)
			if st == "running" || st == "waiting_approval" {
				active = append(active, s)
			}
		}
		if len(active) == 1 {
			idStr, _ := active[0]["id"].(string)
			return aid + "::" + idStr, nil
		}
	}

	return "", NewAgentApiError("invalid_input", "browser_run requires session_key because the calling session is ambiguous")
}

func (h *Handler) invokeBrowserRun(ctx context.Context, payload map[string]any) (any, error) {
	url, _ := payload["url"].(string)
	goal, _ := payload["goal"].(string)
	if strings.TrimSpace(url) == "" || strings.TrimSpace(goal) == "" {
		return nil, NewAgentApiError("invalid_input", "url and goal are required")
	}
	if h.options.BrowserInvoke != nil {
		return h.options.BrowserInvoke(ctx, "browser.run", payload)
	}

	if inputsRaw, ok := payload["inputs"]; ok && inputsRaw != nil {
		if _, ok := inputsRaw.(map[string]any); !ok {
			return nil, NewAgentApiError("invalid_input", "inputs must be an object mapping field labels to values")
		}
	}

	if incRaw, ok := payload["include_screenshot"]; ok && incRaw != nil {
		if _, ok := incRaw.(bool); !ok {
			return nil, NewAgentApiError("invalid_input", "include_screenshot must be a boolean")
		}
	}

	sessionKey, err := h.resolveBrowserSessionKey(ctx, payload)
	if err != nil {
		return nil, err
	}

	if h.options.BrowserManager == nil {
		return nil, NewAgentApiError("browser_task_failed", "Browser manager is not configured")
	}

	taskInput := make(map[string]any, len(payload)+1)
	for k, v := range payload {
		taskInput[k] = v
	}
	taskInput["session_key"] = sessionKey

	res, err := h.options.BrowserManager.RunTask(ctx, taskInput)
	if err != nil {
		return nil, NewAgentApiError("browser_task_failed", err.Error())
	}

	if rev, ok := res["screenshot_revision"]; ok && rev != nil && h.options.Publisher != nil {
		_ = h.options.Publisher.Publish(ctx, "browser.mirror.updated", map[string]any{
			"revision":    rev,
			"url":         res["url"],
			"title":       res["title"],
			"session_key": sessionKey,
		})
	}

	return res, nil
}

func (h *Handler) invokeBrowserScreenshot(ctx context.Context, payload map[string]any) (any, error) {
	if h.options.BrowserInvoke != nil {
		return h.options.BrowserInvoke(ctx, "browser.screenshot", payload)
	}
	sessionKey, err := h.resolveBrowserSessionKey(ctx, payload)
	if err != nil {
		return nil, err
	}

	if h.options.BrowserManager == nil {
		return nil, NewAgentApiError("browser_task_failed", "Browser manager is not configured")
	}

	snap, err := h.options.BrowserManager.CaptureScreenshot(ctx, sessionKey, true, "")
	if err != nil {
		return nil, NewAgentApiError("browser_task_failed", err.Error())
	}

	return snap, nil
}

func (h *Handler) invokeBrowserCDP(ctx context.Context, payload map[string]any) (any, error) {
	if h.options.BrowserInvoke != nil {
		return h.options.BrowserInvoke(ctx, "browser.cdp", payload)
	}
	method, _ := payload["method"].(string)
	if strings.TrimSpace(method) == "" {
		return nil, NewAgentApiError("invalid_input", "method is required")
	}

	params, _ := payload["params"].(map[string]any)
	if paramsRaw, ok := payload["params"]; ok && paramsRaw != nil {
		if params == nil {
			return nil, NewAgentApiError("invalid_input", "params must be an object")
		}
	}

	sessionKey, err := h.resolveBrowserSessionKey(ctx, payload)
	if err != nil {
		return nil, err
	}

	if h.options.BrowserManager == nil {
		return nil, NewAgentApiError("browser_cdp_failed", "Browser manager is not configured")
	}

	res, err := h.options.BrowserManager.CallCDP(ctx, sessionKey, method, params, "")
	if err != nil {
		return nil, NewAgentApiError("browser_cdp_failed", err.Error())
	}

	return res, nil
}

func toInt(v any) (int, error) {
	switch n := v.(type) {
	case int:
		return n, nil
	case int64:
		return int(n), nil
	case float64:
		return int(n), nil
	case json.Number:
		i, err := n.Int64()
		return int(i), err
	case string:
		var i int
		_, err := fmt.Sscanf(n, "%d", &i)
		return i, err
	}
	return 0, errors.New("not an integer")
}
