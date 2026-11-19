package modelgateway

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	sessiondv1 "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
)

func strField(m map[string]any, key string) string {
	if m == nil {
		return ""
	}
	v, _ := m[key].(string)
	return v
}

func firstNonEmptyMapStr(key string, maps ...map[string]any) string {
	for _, m := range maps {
		if m == nil {
			continue
		}
		if v, ok := m[key].(string); ok {
			if trimmed := strings.TrimSpace(v); trimmed != "" {
				return trimmed
			}
		}
	}
	return ""
}

func remoteAgyDefault(user string) string {
	u := strings.TrimSpace(user)
	if u == "" {
		u = "luwei"
	}
	if u == "root" {
		return "/root/.local/bin/agy"
	}
	return "/home/" + u + "/.local/bin/agy"
}

func parseNativeGatewayJSON(nj *sessiondv1.NativeJson, dst any) error {
	if nj == nil || len(nj.Utf8Json) == 0 {
		return fmt.Errorf("native JSON is missing")
	}
	dec := json.NewDecoder(bytes.NewReader(nj.Utf8Json))
	dec.UseNumber()
	if err := dec.Decode(dst); err != nil {
		return err
	}
	if err := dec.Decode(new(any)); err != io.EOF {
		return fmt.Errorf("trailing native JSON")
	}
	return nil
}

func (h *Handler) agentConnectionEnabled(ctx context.Context, agent map[string]any) bool {
	if agent == nil {
		return false
	}
	connID := strField(agent, "connection_id")
	if connID == "" {
		connID = "local"
	}
	enabled, present, err := h.store.GetAgentConnectionChoice(ctx, connID+":antigravity")
	if err != nil {
		return false
	}
	if present && !enabled {
		return false
	}
	return true
}

func (h *Handler) resolveAntigravityTarget(ctx context.Context, agentID string) (*sessiondv1.RuntimeTarget, map[string]any, error) {
	var agent map[string]any
	if agentID != "" {
		row, err := h.store.GetAgent(ctx, agentID)
		if err != nil || row == nil {
			return nil, nil, fmt.Errorf("antigravity agent not found")
		}
		kind := strField(row, "kind")
		if kind != "antigravity" && kind != "antigravity-ssh" {
			return nil, nil, fmt.Errorf("agent %s is not antigravity", agentID)
		}
		if !h.agentConnectionEnabled(ctx, row) {
			return nil, nil, fmt.Errorf("antigravity agent connection is disabled")
		}
		agent = row
	} else {
		rows, err := h.store.ListAgents(ctx)
		if err != nil {
			return nil, nil, err
		}
		for _, row := range rows {
			kind := strField(row, "kind")
			if kind != "antigravity" && kind != "antigravity-ssh" {
				continue
			}
			if !h.agentConnectionEnabled(ctx, row) {
				continue
			}
			status := strField(row, "status")
			if status == "ready" || status == "connected" {
				agent = row
				break
			}
			if agent == nil {
				agent = row
			}
		}
		if agent == nil {
			return nil, nil, fmt.Errorf("no connected antigravity agent")
		}
	}

	aid := strField(agent, "id")
	kind := strField(agent, "kind")
	connID := strField(agent, "connection_id")
	target := &sessiondv1.RuntimeTarget{AgentId: &aid}
	if connID != "" && connID != "local" {
		target.ConnectionId = &connID
	}

	isSSH := (connID != "" && connID != "local") || kind == "antigravity-ssh"
	if !isSSH {
		target.Runtime = sessiondv1.RuntimeKind_RUNTIME_KIND_ANTIGRAVITY
		return target, agent, nil
	}

	target.Runtime = sessiondv1.RuntimeKind_RUNTIME_KIND_ANTIGRAVITY_SSH
	sshRow, err := h.store.GetSSHConnection(ctx, connID)
	if err != nil || sshRow == nil {
		return nil, nil, fmt.Errorf("ssh connection %s not found", connID)
	}
	settings, _ := sshRow["settings"].(map[string]any)
	host, _ := settings["host"].(string)
	alias := strField(settings, "ssh_config_alias")
	if host == "" && alias == "" {
		return nil, nil, fmt.Errorf("ssh host or alias is required")
	}

	portNum := 22
	if pVal, ok := settings["port"]; ok && pVal != nil {
		switch p := pVal.(type) {
		case int:
			portNum = p
		case float64:
			portNum = int(p)
		case int64:
			portNum = int(p)
		case json.Number:
			if n, err := p.Int64(); err == nil {
				portNum = int(n)
			}
		case string:
			if n, err := strconv.Atoi(p); err == nil {
				portNum = n
			}
		}
	}
	if portNum < 1 || portNum > 65535 {
		portNum = 22
	}

	sshConf := &sessiondv1.SshConnectConfig{
		Host: host,
		Port: uint32(portNum),
	}
	user := strField(settings, "user")
	if user != "" {
		sshConf.User = &user
	}
	if alias != "" {
		sshConf.SshConfigAlias = &alias
	}
	if idf := strField(settings, "identity_file"); idf != "" {
		sshConf.IdentityFile = &idf
	}
	ws := strField(settings, "workspace")
	if ws == "" {
		ws = "/home/luwei"
	}
	sshConf.Workspace = &ws

	if ae := firstNonEmptyMapStr("antigravity_executable", settings, sshRow); ae != "" {
		sshConf.AntigravityExecutable = &ae
	} else {
		defExe := remoteAgyDefault(user)
		sshConf.AntigravityExecutable = &defExe
	}
	if profile := strField(sshRow, "profile_name"); profile != "" {
		sshConf.ProfileName = &profile
	}
	target.Ssh = sshConf
	return target, agent, nil
}

func (h *Handler) shouldTryAntigravityNative(ctx context.Context, model, agentID string) bool {
	if agentID != "" {
		if agent, err := h.store.GetAgent(ctx, agentID); err == nil && agent != nil {
			kind := strField(agent, "kind")
			return kind == "antigravity" || kind == "antigravity-ssh"
		}
	}
	return strings.HasPrefix(model, "antigravity/")
}

func matchAntigravityQuotaModel(models []map[string]any, candidate string) map[string]any {
	cand := strings.TrimSpace(strings.TrimPrefix(candidate, "antigravity/"))
	lowerCand := strings.ToLower(cand)
	if strings.Contains(lowerCand, "-worker") {
		cand = "gemini-pro-orchestrator"
		lowerCand = "gemini-pro-orchestrator"
	}

	var withQuota []map[string]any
	for _, m := range models {
		if _, ok := m["used_percent"]; ok {
			withQuota = append(withQuota, m)
		}
	}
	if len(withQuota) == 0 {
		return nil
	}

	if lowerCand == "" || lowerCand == "default" {
		return withQuota[0]
	}

	for _, m := range withQuota {
		if strings.EqualFold(strField(m, "id"), cand) || strings.EqualFold(strField(m, "label"), cand) {
			return m
		}
	}
	for _, m := range withQuota {
		id := strings.ToLower(strField(m, "id"))
		label := strings.ToLower(strField(m, "label"))
		if id != "" && (strings.HasPrefix(id, lowerCand+"-") || strings.HasPrefix(lowerCand, id)) {
			return m
		}
		baseLabel := strings.TrimSpace(strings.SplitN(label, "(", 2)[0])
		if baseLabel != "" && strings.EqualFold(baseLabel, cand) {
			return m
		}
	}
	return nil
}

func (h *Handler) serveAntigravityNativeModelQuota(ctx context.Context, model, agentID string) (map[string]any, bool) {
	if h.client == nil || h.client.Runtime == nil {
		return nil, false
	}
	target, agent, err := h.resolveAntigravityTarget(ctx, agentID)
	if err != nil {
		return nil, false
	}

	qCtx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()

	resp, err := h.client.Runtime.QueryNative(qCtx, &sessiondv1.NativeQueryRequest{
		Target: target,
		Query: &sessiondv1.NativeQueryRequest_AntigravityQuotaRead{
			AntigravityQuotaRead: &sessiondv1.AntigravityQuotaReadQuery{},
		},
	})
	if err != nil || resp == nil {
		return nil, false
	}

	var raw struct {
		Provider string           `json:"provider"`
		Plan     string           `json:"plan"`
		PlanName string           `json:"plan_name"`
		AsOf     string           `json:"as_of"`
		Models   []map[string]any `json:"models"`
	}
	if err := parseNativeGatewayJSON(resp.Result, &raw); err != nil {
		return nil, false
	}

	_, candidate, qualified := strings.Cut(model, "/")
	if !qualified {
		candidate = model
	}

	plan := raw.Plan
	if plan == "" {
		plan = raw.PlanName
	}
	logLabel := strField(agent, "name")
	if logLabel == "" {
		logLabel = "Antigravity 原生订阅"
	}

	matched := matchAntigravityQuotaModel(raw.Models, candidate)
	if matched == nil {
		if plan == "" {
			return nil, false
		}
		account := map[string]any{
			"id":           "antigravity/" + strField(agent, "id"),
			"logLabel":     logLabel,
			"plan":         plan,
			"error":        "",
			"last":         false,
			"readDeferred": false,
			"quota": map[string]any{
				"customWindows": []map[string]any{},
			},
		}
		if raw.AsOf != "" {
			account["asOf"] = raw.AsOf
		}
		return map[string]any{
			"model":     candidate,
			"providers": []string{"antigravity"},
			"reports":   []any{},
			"accounts":  []map[string]any{account},
		}, true
	}

	usedVal, err := strconv.ParseFloat(fmt.Sprint(matched["used_percent"]), 64)
	if err != nil {
		return nil, false
	}
	windowLabel := strField(matched, "label")
	if windowLabel == "" {
		windowLabel = strField(matched, "id")
	}
	if windowLabel == "" {
		windowLabel = "订阅配额"
	}

	window := map[string]any{
		"label":   windowLabel,
		"percent": usedVal,
	}
	if rawReset, ok := matched["reset_at"]; ok && rawReset != nil {
		if resetAt, rErr := strconv.ParseInt(fmt.Sprint(rawReset), 10, 64); rErr == nil && resetAt > 0 {
			window["resetAt"] = resetAt
		}
	}

	account := map[string]any{
		"id":           "antigravity/" + strField(agent, "id"),
		"logLabel":     logLabel,
		"plan":         plan,
		"error":        "",
		"last":         false,
		"readDeferred": false,
		"quota": map[string]any{
			"customWindows": []map[string]any{window},
		},
	}
	if raw.AsOf != "" {
		account["asOf"] = raw.AsOf
	}

	return map[string]any{
		"model":     candidate,
		"providers": []string{"antigravity"},
		"reports":   []any{},
		"accounts":  []map[string]any{account},
	}, true
}

func (h *Handler) fetchAntigravityNativeSessionUsage(ctx context.Context, agentID, sessionID string) bool {
	if h.client == nil || h.client.Runtime == nil || agentID == "" || sessionID == "" {
		return false
	}
	target, _, err := h.resolveAntigravityTarget(ctx, agentID)
	if err != nil {
		return false
	}

	nativeID := sessionID
	readonly := false
	if sessRow, sErr := h.store.GetSession(ctx, agentID, sessionID); sErr == nil && sessRow != nil {
		if src := strField(sessRow, "source_session_id"); src != "" {
			nativeID = src
		}
		readonly = strField(sessRow, "control_state") == "readonly"
	}

	uCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	resp, err := h.client.Runtime.QueryNative(uCtx, &sessiondv1.NativeQueryRequest{
		Target: target,
		Query: &sessiondv1.NativeQueryRequest_AntigravitySessionUsage{
			AntigravitySessionUsage: &sessiondv1.AntigravitySessionUsageQuery{
				ConversationId: nativeID,
				Readonly:       &readonly,
			},
		},
	})
	if err != nil || resp == nil {
		return false
	}

	var data map[string]any
	if err := parseNativeGatewayJSON(resp.Result, &data); err != nil || data == nil {
		return false
	}

	numVal := func(k string) int64 {
		v, ok := data[k]
		if !ok || v == nil {
			return 0
		}
		n, err := strconv.ParseInt(fmt.Sprint(v), 10, 64)
		if err != nil || n < 0 {
			return 0
		}
		return n
	}

	totalTokens := numVal("total_tokens")
	contextWindow := numVal("context_window")
	lastInputTokens := numVal("last_input_tokens")
	ctxAvailable, _ := data["context_available"].(bool)
	if !ctxAvailable && contextWindow == 0 && lastInputTokens == 0 && totalTokens == 0 {
		return false
	}

	model := strings.TrimSpace(strField(data, "model"))
	if model == "" {
		model = strings.TrimSpace(strField(data, "default_model"))
	}
	if model == "" {
		model = "default"
	}

	if existing, exErr := h.store.ListTokenMetrics(ctx, agentID, sessionID); exErr == nil && len(existing) > 0 && existing[0]["provider"] == "antigravity" {
		if exModel := strings.TrimSpace(strField(existing[0], "model")); exModel != "" && exModel != "default" {
			model = exModel
		}
		if exCw, cwErr := strconv.ParseInt(fmt.Sprint(existing[0]["context_window"]), 10, 64); cwErr == nil && exCw > contextWindow {
			contextWindow = exCw
			if exLi, liErr := strconv.ParseInt(fmt.Sprint(existing[0]["last_input_tokens"]), 10, 64); liErr == nil && exLi > 0 {
				lastInputTokens = exLi
			}
		} else if lastInputTokens == 0 {
			if exLi, liErr := strconv.ParseInt(fmt.Sprint(existing[0]["last_input_tokens"]), 10, 64); liErr == nil && exLi > 0 {
				lastInputTokens = exLi
			}
		}
	}

	_, upErr := h.store.UpsertTokenMetric(ctx, map[string]any{
		"agent_id":          agentID,
		"session_id":        sessionID,
		"model":             model,
		"provider":          "antigravity",
		"date":              time.Now().UTC().Format("2006-01-02"),
		"input_tokens":      numVal("input_tokens"),
		"output_tokens":     numVal("output_tokens"),
		"cached_tokens":     numVal("cached_tokens"),
		"reasoning_tokens":  numVal("reasoning_tokens"),
		"total_tokens":      totalTokens,
		"context_window":    contextWindow,
		"last_input_tokens": lastInputTokens,
	})
	return upErr == nil
}
