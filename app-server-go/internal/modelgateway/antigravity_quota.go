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

	"astrorder.dev/app-server/internal/connections"
	sessiondv1 "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
)

func strField(m map[string]any, key string) string {
	if m == nil {
		return ""
	}
	v, _ := m[key].(string)
	return v
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
	if ws != "" {
		sshConf.Workspace = &ws
	}

	sshConf.AntigravityExecutable = connections.RuntimeExecutable(ctx, h.remoteExecutable, "antigravity", sshRow)
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

type antigravityUsageBucket struct {
	ID                string  `json:"id"`
	Name              string  `json:"name"`
	Description       string  `json:"description"`
	Window            string  `json:"window"`
	RemainingFraction float64 `json:"remaining_fraction"`
	UsedPercent       float64 `json:"used_percent"`
	ResetAt           int64   `json:"reset_at"`
	ResetTime         string  `json:"reset_time"`
}

type antigravityUsageGroup struct {
	Name        string                   `json:"name"`
	Description string                   `json:"description"`
	Buckets     []antigravityUsageBucket `json:"buckets"`
}

func antigravityGroupDisplayName(name string) string {
	trimmed := strings.TrimSpace(name)
	switch strings.ToLower(trimmed) {
	case "gemini models":
		return "Gemini 模型组"
	case "claude and gpt models":
		return "Claude / GPT 模型组"
	default:
		return trimmed
	}
}

func matchAntigravityUsageGroup(groups []antigravityUsageGroup, candidate string) *antigravityUsageGroup {
	cand := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(candidate, "antigravity/")))
	if cand == "" || cand == "default" {
		return nil
	}

	if strings.Contains(cand, "argon") {
		for i := range groups {
			if strings.Contains(strings.ToLower(groups[i].Name), "argon") {
				return &groups[i]
			}
			for _, b := range groups[i].Buckets {
				if strings.Contains(strings.ToLower(b.ID), "argon") {
					return &groups[i]
				}
			}
		}
		for i := range groups {
			if strings.Contains(strings.ToLower(groups[i].Name), "gemini") {
				return &groups[i]
			}
		}
		return nil
	}

	if strings.Contains(cand, "claude") || strings.Contains(cand, "gpt") || strings.Contains(cand, "openai") || strings.Contains(cand, "oss") {
		for i := range groups {
			lowerName := strings.ToLower(groups[i].Name)
			lowerDesc := strings.ToLower(groups[i].Description)
			if strings.Contains(lowerName, "claude") || strings.Contains(lowerName, "gpt") ||
				strings.Contains(lowerDesc, "claude") || strings.Contains(lowerDesc, "gpt") {
				return &groups[i]
			}
			for _, b := range groups[i].Buckets {
				if strings.HasPrefix(strings.ToLower(b.ID), "3p-") {
					return &groups[i]
				}
			}
		}
		return nil
	}

	if strings.Contains(cand, "gemini") || strings.Contains(cand, "orchestrator") {
		for _, kw := range []string{"neon", "hydrogen", "flash", "carbon"} {
			if strings.Contains(cand, kw) {
				for i := range groups {
					if strings.Contains(strings.ToLower(groups[i].Name), "argon") {
						continue
					}
					if strings.Contains(strings.ToLower(groups[i].Description), kw) || strings.Contains(strings.ToLower(groups[i].Name), kw) {
						return &groups[i]
					}
				}
			}
		}
		for i := range groups {
			lowerName := strings.ToLower(groups[i].Name)
			if strings.Contains(lowerName, "gemini") && !strings.Contains(lowerName, "argon") {
				return &groups[i]
			}
		}
	}

	normCand := cand
	for _, suffix := range []string{"-xhigh", "-high", "-medium", "-low", "-max", "-worker"} {
		normCand = strings.TrimSuffix(normCand, suffix)
	}
	normCand = strings.NewReplacer("-", " ", "_", " ", ".", " ").Replace(normCand)
	normCand = strings.Join(strings.Fields(normCand), " ")
	if normCand != "" {
		for i := range groups {
			lowerDesc := strings.ToLower(strings.NewReplacer("-", " ", "_", " ", ".", " ").Replace(groups[i].Description))
			lowerName := strings.ToLower(strings.NewReplacer("-", " ", "_", " ", ".", " ").Replace(groups[i].Name))
			if strings.Contains(lowerDesc, normCand) || strings.Contains(lowerName, normCand) {
				return &groups[i]
			}
		}
	}
	return nil
}

func buildAntigravityGroupAccount(agentID, baseLabel, plan, asOf string, g antigravityUsageGroup, suffix string) map[string]any {
	quota := map[string]any{}
	customWindows := make([]map[string]any, 0)
	hasShort := false
	hasWeekly := false

	for _, b := range g.Buckets {
		winKind := strings.ToLower(strings.TrimSpace(b.Window))
		if winKind == "5h" && !hasShort {
			hasShort = true
			quota["shortPercent"] = b.UsedPercent
			if b.ResetAt > 0 {
				quota["shortResetAt"] = b.ResetAt
			}
			continue
		}
		if winKind == "weekly" && !hasWeekly {
			hasWeekly = true
			quota["weeklyPercent"] = b.UsedPercent
			if b.ResetAt > 0 {
				quota["weeklyResetAt"] = b.ResetAt
			}
			continue
		}
		label := strings.TrimSpace(b.Name)
		if label == "" {
			label = strings.TrimSpace(b.ID)
		}
		if label == "" {
			label = "订阅配额"
		}
		row := map[string]any{
			"label":   label,
			"percent": b.UsedPercent,
		}
		if b.ResetAt > 0 {
			row["resetAt"] = b.ResetAt
		}
		customWindows = append(customWindows, row)
	}
	quota["customWindows"] = customWindows

	logLabel := baseLabel
	if groupLabel := antigravityGroupDisplayName(g.Name); groupLabel != "" {
		logLabel = baseLabel + " · " + groupLabel
	}

	account := map[string]any{
		"id":           "antigravity/" + agentID + "/" + suffix,
		"logLabel":     logLabel,
		"plan":         plan,
		"error":        "",
		"last":         false,
		"readDeferred": false,
		"quota":        quota,
	}
	if asOf != "" {
		account["asOf"] = asOf
	}
	return account
}

// antigravityNativeQuotaPaused disables outbound Antigravity native quota
// queries (`AntigravityQuotaRead` / `agy -p '/usage'`) to comply with Google
// usage policy. Local SQLite session token/context metrics remain enabled.
var antigravityNativeQuotaPaused = true

func (h *Handler) serveAntigravityNativeModelQuota(ctx context.Context, model, agentID string) (map[string]any, bool) {
	if antigravityNativeQuotaPaused {
		return nil, false
	}
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
		Provider string                  `json:"provider"`
		Plan     string                  `json:"plan"`
		PlanName string                  `json:"plan_name"`
		AsOf     string                  `json:"as_of"`
		Groups   []antigravityUsageGroup `json:"groups"`
		Models   []map[string]any        `json:"models"`
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

	if len(raw.Groups) > 0 {
		agentKey := strField(agent, "id")
		var accounts []map[string]any
		if matchedGroup := matchAntigravityUsageGroup(raw.Groups, candidate); matchedGroup != nil {
			accounts = []map[string]any{buildAntigravityGroupAccount(agentKey, logLabel, plan, raw.AsOf, *matchedGroup, "matched")}
		} else {
			accounts = make([]map[string]any, 0, len(raw.Groups))
			for idx, g := range raw.Groups {
				accounts = append(accounts, buildAntigravityGroupAccount(agentKey, logLabel, plan, raw.AsOf, g, strconv.Itoa(idx)))
			}
		}
		return map[string]any{
			"model":     candidate,
			"providers": []string{"antigravity"},
			"reports":   []any{},
			"accounts":  accounts,
		}, true
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
