package modelgateway

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	sessiondv1 "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type directAgentDescriptor struct {
	AgentID        string
	Name           string
	Kind           string // normalized surface: antigravity, codex, grok, hermes, claude
	RawKind        string
	ConnectionID   string
	ConnectionName string
	TargetID       string
}

type directServerOption struct {
	ID   string
	Name string
}

type directSessionRow struct {
	SessionID      string `json:"sessionId"`
	AgentID        string `json:"agentId"`
	AgentName      string `json:"agentName"`
	AgentKind      string `json:"agentKind"`
	ConnectionID   string `json:"connectionId"`
	ConnectionName string `json:"connectionName"`
	Title          string `json:"title"`
	Workspace      string `json:"workspace"`
	Model          string `json:"model"`
	UpdatedAt      string `json:"updatedAt"`
	Requests       int64  `json:"requests"`
	InputTokens    int64  `json:"inputTokens"`
	OutputTokens   int64  `json:"outputTokens"`
	CacheRead      int64  `json:"cacheReadInputTokens"`
	CacheWrite     int64  `json:"cacheCreationInputTokens"`
	Reasoning      int64  `json:"reasoningOutputTokens"`
	TotalTokens    int64  `json:"totalTokens"`
}

type directAgentSummary struct {
	magpieTotals
	AgentID        string
	Name           string
	Kind           string
	ConnectionID   string
	ConnectionName string
	Sessions       int
}

type directUsageAggregate struct {
	Totals                magpieTotals
	Since                 time.Time
	DayTotals             map[string]magpieTotals
	DayModels             map[string]map[string]magpieGroup
	Models                map[string]magpieGroup
	Accounts              map[string]magpieGroup
	Agents                map[string]*directAgentSummary
	Sessions              []directSessionRow
	Servers               []directServerOption
	ConnectionID          string
	DirectOnly            bool
	ActiveSessions        int
	DaemonUpgradeRequired bool
}

func newDirectUsageAggregate() *directUsageAggregate {
	return &directUsageAggregate{
		DayTotals:    map[string]magpieTotals{},
		DayModels:    map[string]map[string]magpieGroup{},
		Models:       map[string]magpieGroup{},
		Accounts:     map[string]magpieGroup{},
		Agents:       map[string]*directAgentSummary{},
		ConnectionID: "all",
	}
}

func normalizeAgentSurface(kind string) string {
	k := strings.ToLower(strings.TrimSpace(kind))
	k = strings.TrimSuffix(k, "-ssh")
	return k
}

func normalizeGatewayURLHostPrefix(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}
	u, err := url.Parse(trimmed)
	if err != nil || u.Host == "" {
		return ""
	}
	return strings.ToLower(u.Scheme + "://" + u.Host)
}

func resolveAnalyticsTimeWindow(period, sinceStr, untilStr string, now time.Time) (sinceMs, untilMs int64, sinceTime time.Time) {
	if sinceStr != "" && untilStr != "" {
		s, _ := strconv.ParseInt(sinceStr, 10, 64)
		u, _ := strconv.ParseInt(untilStr, 10, 64)
		return s, u, time.UnixMilli(s)
	}
	localNow := now.In(time.Local)
	todayStart := time.Date(localNow.Year(), localNow.Month(), localNow.Day(), 0, 0, 0, 0, time.Local)
	switch period {
	case "today":
		return todayStart.UnixMilli(), 0, todayStart
	case "7d":
		start := todayStart.AddDate(0, 0, -6)
		return start.UnixMilli(), 0, start
	case "30d":
		start := todayStart.AddDate(0, 0, -29)
		return start.UnixMilli(), 0, start
	default:
		return 0, 0, time.Time{}
	}
}

func (h *Handler) detectGatewayRoutedTargets(ctx context.Context, cfg GatewayConfig, targetIDs []string) map[string]bool {
	if h.client == nil || h.client.Model == nil || len(targetIDs) == 0 {
		return map[string]bool{}
	}
	cfgKey := cfg.GatewayType + "|" + cfg.InferenceURL + "|" + fmt.Sprint(cfg.TargetOverrides)
	h.directRouteMu.Lock()
	if h.directRoutedToGw != nil && h.directRouteConfigKey == cfgKey && time.Since(h.directRouteCachedAt) < 90*time.Second {
		cp := make(map[string]bool, len(h.directRoutedToGw))
		for k, v := range h.directRoutedToGw {
			cp[k] = v
		}
		h.directRouteMu.Unlock()
		return cp
	}
	h.directRouteMu.Unlock()

	routed := map[string]bool{}
	var mu sync.Mutex
	var wg sync.WaitGroup

	for _, tid := range targetIDs {
		tid := tid
		wg.Add(1)
		go func() {
			defer wg.Done()
			tProto, err := h.resolveTargetProto(ctx, tid)
			if err != nil {
				return
			}
			infURL := cfg.InferenceURL
			if ov, ok := cfg.TargetOverrides[tid]; ok && ov != "" {
				infURL = ov
			}
			hostPrefix := normalizeGatewayURLHostPrefix(infURL)
			if hostPrefix == "" {
				return
			}
			pCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
			planResp, err := h.client.Model.Plan(pCtx, &sessiondv1.ModelConfigPlanRequest{Target: tProto})
			cancel()
			if err != nil || planResp == nil {
				// If an SSH host cannot be inspected right now, do not assume its
				// codex/grok/hermes traffic is direct if gateway is configured.
				mu.Lock()
				routed[tid+":unverified"] = true
				mu.Unlock()
				return
			}
			for _, f := range planResp.CurrentFiles {
				if len(f.Content) == 0 {
					continue
				}
				lowerContent := strings.ToLower(string(f.Content))
				if !strings.Contains(lowerContent, hostPrefix) {
					continue
				}
				mu.Lock()
				switch f.File {
				case sessiondv1.ManagedFile_MANAGED_FILE_CODEX_CONFIG:
					routed[tid+":codex"] = true
				case sessiondv1.ManagedFile_MANAGED_FILE_GROK_CONFIG:
					routed[tid+":grok"] = true
				case sessiondv1.ManagedFile_MANAGED_FILE_HERMES_CONFIG:
					routed[tid+":hermes"] = true
				case sessiondv1.ManagedFile_MANAGED_FILE_ANTIGRAVITY_CONFIG:
					routed[tid+":antigravity"] = true
				}
				mu.Unlock()
			}
		}()
	}
	wg.Wait()

	h.directRouteMu.Lock()
	h.directRoutedToGw = make(map[string]bool, len(routed))
	for k, v := range routed {
		h.directRoutedToGw[k] = v
	}
	h.directRouteConfigKey = cfgKey
	h.directRouteCachedAt = time.Now()
	h.directRouteMu.Unlock()
	return routed
}

func (h *Handler) listSessionUsageAgents(ctx context.Context, cfg GatewayConfig, surface, connectionID string, directOnly bool) ([]directAgentDescriptor, []directServerOption, error) {
	rows, err := h.store.ListAgents(ctx)
	if err != nil {
		return nil, nil, err
	}
	if len(rows) == 0 {
		return nil, nil, nil
	}

	sshConns, err := h.store.ListSSHConnections(ctx)
	if err != nil {
		return nil, nil, err
	}
	connNames := map[string]string{"local": "本机"}
	for _, c := range sshConns {
		cid := strField(c, "id")
		if cid == "" {
			continue
		}
		disp := strField(c, "display_name")
		if disp == "" {
			disp = strField(c, "profile_name")
		}
		if disp == "" {
			disp = cid
		}
		connNames[cid] = disp
	}

	filterConn := strings.TrimSpace(connectionID)
	if filterConn == "all" {
		filterConn = ""
	}

	serversMap := map[string]directServerOption{}
	targetSet := map[string]bool{}
	var candidates []directAgentDescriptor
	for _, row := range rows {
		aid := strField(row, "id")
		rawKind := strField(row, "kind")
		baseKind := normalizeAgentSurface(rawKind)
		if aid == "" || baseKind == "" {
			continue
		}
		connID := strField(row, "connection_id")
		if connID == "" {
			connID = "local"
		}
		if enabled, present, cErr := h.store.GetAgentConnectionChoice(ctx, connID+":"+baseKind); cErr == nil && present && !enabled {
			continue
		}
		cName, configured := connNames[connID]
		if configured {
			serversMap[connID] = directServerOption{ID: connID, Name: cName}
		} else {
			// Keep stored usage attributable to removed connections without listing them as selectable servers.
			cName = connID
		}

		if surface != "all" && surface != "" && baseKind != surface {
			continue
		}
		if filterConn != "" && connID != filterConn {
			continue
		}
		tid := "local"
		if connID != "local" {
			tid = "ssh:" + connID
		}
		if directOnly && baseKind != "antigravity" {
			targetSet[tid] = true
		}
		name := strField(row, "name")
		if name == "" {
			name = aid
		}
		candidates = append(candidates, directAgentDescriptor{
			AgentID:        aid,
			Name:           name,
			Kind:           baseKind,
			RawKind:        rawKind,
			ConnectionID:   connID,
			ConnectionName: cName,
			TargetID:       tid,
		})
	}

	servers := make([]directServerOption, 0, len(serversMap))
	for _, s := range serversMap {
		servers = append(servers, s)
	}
	sort.Slice(servers, func(i, j int) bool {
		if servers[i].ID == "local" {
			return true
		}
		if servers[j].ID == "local" {
			return false
		}
		li, lj := strings.ToLower(servers[i].Name), strings.ToLower(servers[j].Name)
		if li != lj {
			return li < lj
		}
		return servers[i].ID < servers[j].ID
	})

	if !directOnly {
		return candidates, servers, nil
	}

	var targetList []string
	for tid := range targetSet {
		targetList = append(targetList, tid)
	}
	routed := h.detectGatewayRoutedTargets(ctx, cfg, targetList)

	hasConfiguredGateway := normalizeGatewayURLHostPrefix(cfg.InferenceURL) != ""
	var direct []directAgentDescriptor
	for _, cand := range candidates {
		if cand.Kind == "antigravity" {
			if !routed[cand.TargetID+":antigravity"] {
				direct = append(direct, cand)
			}
			continue
		}
		if routed[cand.TargetID+":"+cand.Kind] || routed[cand.TargetID+":unverified"] {
			continue
		}
		// In unit-test / daemonless mode without Model.Plan, only treat non-Antigravity
		// agents as direct if no gateway is configured or their metrics explicitly mark direct.
		if h.client == nil && hasConfiguredGateway {
			continue
		}
		direct = append(direct, cand)
	}
	return direct, servers, nil
}

func (a *directUsageAggregate) record(provider, model, accountLabel, day string, slotTime time.Time, desc directAgentDescriptor, t magpieTotals) {
	if t.totalTokens() <= 0 && t.Calls <= 0 {
		return
	}
	a.Totals.add(t)
	if !slotTime.IsZero() && (a.Since.IsZero() || slotTime.Before(a.Since)) {
		a.Since = slotTime
	}

	dt := a.DayTotals[day]
	dt.add(t)
	a.DayTotals[day] = dt

	dm := a.DayModels[day]
	if dm == nil {
		dm = map[string]magpieGroup{}
		a.DayModels[day] = dm
	}
	mKey := provider + "\x00" + model
	dmg := dm[mKey]
	dmg.Provider = provider
	dmg.Model = model
	dmg.magpieTotals.add(t)
	dm[mKey] = dmg

	mg := a.Models[mKey]
	mg.Provider = provider
	mg.Model = model
	mg.magpieTotals.add(t)
	a.Models[mKey] = mg

	if accountLabel != "" {
		aKey := provider + "\x00" + accountLabel
		ag := a.Accounts[aKey]
		ag.Provider = provider
		ag.Account = accountLabel
		ag.magpieTotals.add(t)
		a.Accounts[aKey] = ag
	}

	agSum := a.Agents[desc.AgentID]
	if agSum == nil {
		agSum = &directAgentSummary{
			AgentID:        desc.AgentID,
			Name:           desc.Name,
			Kind:           desc.Kind,
			ConnectionID:   desc.ConnectionID,
			ConnectionName: desc.ConnectionName,
		}
		a.Agents[desc.AgentID] = agSum
	}
	agSum.magpieTotals.add(t)
}

func int64MapField(m map[string]any, key string) int64 {
	v, ok := m[key]
	if !ok || v == nil {
		return 0
	}
	switch n := v.(type) {
	case int64:
		return n
	case int:
		return int64(n)
	case float64:
		return int64(n)
	case json.Number:
		i, _ := n.Int64()
		return i
	default:
		i, _ := strconv.ParseInt(fmt.Sprint(v), 10, 64)
		return i
	}
}

func isDaemonUpgradeRequiredError(err error) bool {
	if err == nil {
		return false
	}
	code := status.Code(err)
	msg := strings.ToLower(err.Error())
	if code == codes.InvalidArgument && (strings.Contains(msg, "unknown fields") || strings.Contains(msg, "native query request is invalid")) {
		return true
	}
	if code == codes.Unimplemented && !strings.Contains(msg, "ssh transport") {
		return true
	}
	return strings.Contains(msg, "unsupported antigravity")
}

func (h *Handler) collectDirectUsage(ctx context.Context, cfg GatewayConfig, period, surface, sinceStr, untilStr string) (*directUsageAggregate, error) {
	return h.collectSessionUsage(ctx, cfg, period, surface, "", sinceStr, untilStr, true)
}

func (h *Handler) collectSessionUsage(ctx context.Context, cfg GatewayConfig, period, surface, connectionID, sinceStr, untilStr string, directOnly bool) (*directUsageAggregate, error) {
	agg := newDirectUsageAggregate()
	agg.DirectOnly = directOnly
	if norm := strings.TrimSpace(connectionID); norm != "" && norm != "all" {
		agg.ConnectionID = norm
	}
	now := time.Now()
	sinceMs, untilMs, winStart := resolveAnalyticsTimeWindow(period, sinceStr, untilStr, now)
	if !winStart.IsZero() {
		agg.Since = winStart
	}

	agents, servers, err := h.listSessionUsageAgents(ctx, cfg, surface, connectionID, directOnly)
	agg.Servers = servers
	if err != nil || len(agents) == 0 {
		return agg, err
	}

	var agyAgents []directAgentDescriptor
	seenAgyTarget := map[string]bool{}
	var otherAgents []directAgentDescriptor
	for _, d := range agents {
		if d.Kind == "antigravity" {
			if !seenAgyTarget[d.TargetID] {
				seenAgyTarget[d.TargetID] = true
				agyAgents = append(agyAgents, d)
			}
		} else {
			otherAgents = append(otherAgents, d)
		}
	}

	var mu sync.Mutex
	var wg sync.WaitGroup

	// 1. Collect Antigravity native step-level usage across local & SSH targets.
	if h.client != nil && h.client.Runtime != nil {
		for _, desc := range agyAgents {
			desc := desc
			wg.Add(1)
			go func() {
				defer wg.Done()
				target, _, tErr := h.resolveAntigravityTarget(ctx, desc.AgentID)
				if tErr != nil {
					return
				}
				q := &sessiondv1.AntigravityUsageSummaryQuery{}
				if sinceMs > 0 {
					q.SinceMs = &sinceMs
				}
				if untilMs > 0 {
					q.UntilMs = &untilMs
				}
				qCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
				resp, qErr := h.client.Runtime.QueryNative(qCtx, &sessiondv1.NativeQueryRequest{
					Target: target,
					Query: &sessiondv1.NativeQueryRequest_AntigravityUsageSummary{
						AntigravityUsageSummary: q,
					},
				})
				cancel()
				if qErr != nil {
					if isDaemonUpgradeRequiredError(qErr) {
						mu.Lock()
						agg.DaemonUpgradeRequired = true
						mu.Unlock()
					}
					return
				}
				if resp == nil || resp.Result == nil {
					return
				}
				var payload struct {
					Slots          []map[string]any `json:"slots"`
					Sessions       []map[string]any `json:"sessions"`
					ActiveSessions int              `json:"active_sessions"`
				}
				if parseNativeGatewayJSON(resp.Result, &payload) != nil {
					return
				}

				acctLabel := desc.ConnectionName
				if directOnly {
					acctLabel = "直连 · " + desc.ConnectionName
				}
				mu.Lock()
				defer mu.Unlock()
				for _, sl := range payload.Slots {
					slot15m := int64MapField(sl, "slot_15m")
					model := strings.TrimSpace(strField(sl, "model"))
					if model == "" {
						model = "antigravity-default"
					}
					slotTime := time.Unix(slot15m*900, 0).In(time.Local)
					day := slotTime.Format("2006-01-02")
					tot := magpieTotals{
						Calls:      int64MapField(sl, "calls"),
						Input:      int64MapField(sl, "input"),
						Output:     int64MapField(sl, "output"),
						CacheRead:  int64MapField(sl, "cache_read"),
						CacheWrite: int64MapField(sl, "cache_write"),
						Reasoning:  int64MapField(sl, "reasoning"),
					}
					agg.record("antigravity", model, acctLabel, day, slotTime, desc, tot)
				}

				if payload.ActiveSessions > 0 {
					agg.ActiveSessions += payload.ActiveSessions
					if agSum := agg.Agents[desc.AgentID]; agSum != nil {
						agSum.Sessions += payload.ActiveSessions
					}
				}

				for _, sr := range payload.Sessions {
					sid := strField(sr, "session_id")
					title := strings.TrimSpace(strField(sr, "title"))
					ws := strField(sr, "workspace")
					if title == "" {
						if storedSess, sErr := h.store.GetSession(ctx, desc.AgentID, sid); sErr == nil && storedSess != nil {
							title = strings.TrimSpace(strField(storedSess, "title"))
							if ws == "" {
								ws = strField(storedSess, "workspace")
							}
						}
					}
					if title == "" {
						title = sid
					}
					agg.Sessions = append(agg.Sessions, directSessionRow{
						SessionID:      sid,
						AgentID:        desc.AgentID,
						AgentName:      desc.Name,
						AgentKind:      desc.Kind,
						ConnectionID:   desc.ConnectionID,
						ConnectionName: desc.ConnectionName,
						Title:          title,
						Workspace:      ws,
						Model:          strField(sr, "model"),
						UpdatedAt:      strField(sr, "updated_at"),
						Requests:       int64MapField(sr, "calls"),
						InputTokens:    int64MapField(sr, "input"),
						OutputTokens:   int64MapField(sr, "output"),
						CacheRead:      int64MapField(sr, "cache_read"),
						CacheWrite:     int64MapField(sr, "cache_write"),
						Reasoning:      int64MapField(sr, "reasoning"),
						TotalTokens:    int64MapField(sr, "total"),
					})
				}
			}()
		}
	}

	// 2. Collect token_metrics for non-Antigravity agents (direct-only filters out gateway providers).
	if len(otherAgents) > 0 {
		byAgent := map[string]directAgentDescriptor{}
		placeholders := make([]string, 0, len(otherAgents))
		args := make([]any, 0, len(otherAgents))
		for _, d := range otherAgents {
			byAgent[d.AgentID] = d
			placeholders = append(placeholders, "?")
			args = append(args, d.AgentID)
		}
		filterClause := ""
		if directOnly {
			filterClause = " AND COALESCE(provider, '') NOT IN ('ocx', 'opencodex', 'magpie', 'custom')"
		}
		query := fmt.Sprintf(
			`SELECT agent_id, session_id, model, provider, date, input_tokens, output_tokens, cached_tokens, reasoning_tokens, total_tokens, updated_at
			 FROM token_metrics
			 WHERE agent_id IN (%s)%s AND total_tokens > 0
			 ORDER BY agent_id, session_id, date ASC, total_tokens DESC`,
			strings.Join(placeholders, ","),
			filterClause,
		)
		if rows, rErr := h.store.QueryRows(ctx, query, args...); rErr == nil {
			type sessDayKey struct {
				aid, sid, date string
			}
			seenSessDay := map[sessDayKey]bool{}
			type prevSnap struct {
				in, out, cache, reason, tot int64
			}
			prevBySess := map[string]prevSnap{}
			sessAgg := map[string]*directSessionRow{}
			agentSessions := map[string]map[string]bool{}

			minDay := ""
			if sinceMs > 0 {
				minDay = time.UnixMilli(sinceMs).In(time.Local).Format("2006-01-02")
			}
			maxDay := ""
			if untilMs > 0 {
				maxDay = time.UnixMilli(untilMs).In(time.Local).Format("2006-01-02")
			}

			mu.Lock()
			for _, r := range rows {
				aid := strField(r, "agent_id")
				sid := strField(r, "session_id")
				day := strField(r, "date")
				sdk := sessDayKey{aid: aid, sid: sid, date: day}
				if seenSessDay[sdk] {
					continue
				}
				seenSessDay[sdk] = true

				rawIn := int64MapField(r, "input_tokens")
				rawOut := int64MapField(r, "output_tokens")
				rawCache := int64MapField(r, "cached_tokens")
				rawReason := int64MapField(r, "reasoning_tokens")
				rawTot := int64MapField(r, "total_tokens")

				sessKey := aid + "\x00" + sid
				prev := prevBySess[sessKey]
				dIn := rawIn - prev.in
				dOut := rawOut - prev.out
				dCache := rawCache - prev.cache
				dReason := rawReason - prev.reason
				dTot := rawTot - prev.tot
				if dTot < 0 || dIn < 0 || dOut < 0 {
					dIn, dOut, dCache, dReason, dTot = rawIn, rawOut, rawCache, rawReason, rawTot
				}
				if rawTot >= prev.tot {
					prevBySess[sessKey] = prevSnap{in: rawIn, out: rawOut, cache: rawCache, reason: rawReason, tot: rawTot}
				}
				if dTot <= 0 {
					continue
				}
				if (minDay != "" && day < minDay) || (maxDay != "" && day > maxDay) {
					continue
				}

				desc := byAgent[aid]
				uncachedIn := dIn - dCache
				if uncachedIn < 0 {
					uncachedIn = 0
				}
				model := strings.TrimSpace(strField(r, "model"))
				if model == "" || model == "native" {
					if directOnly {
						model = desc.Kind + "-direct"
					} else {
						model = desc.Kind
					}
				}
				provider := strings.TrimSpace(strField(r, "provider"))
				if provider == "" {
					provider = desc.Kind
				}
				acctLabel := desc.ConnectionName
				if directOnly {
					acctLabel = "直连 · " + desc.ConnectionName
				}
				dayTime, _ := time.ParseInLocation("2006-01-02", day, time.Local)
				tot := magpieTotals{
					Calls:     1,
					Input:     uncachedIn,
					Output:    dOut,
					CacheRead: dCache,
					Reasoning: dReason,
				}
				agg.record(provider, model, acctLabel, day, dayTime, desc, tot)

				if agentSessions[aid] == nil {
					agentSessions[aid] = map[string]bool{}
				}
				if !agentSessions[aid][sid] {
					agentSessions[aid][sid] = true
					agg.ActiveSessions++
					if agSum := agg.Agents[aid]; agSum != nil {
						agSum.Sessions++
					}
				}

				sr := sessAgg[sessKey]
				if sr == nil {
					title := sid
					ws := ""
					if sRow, sErr := h.store.GetSession(ctx, aid, sid); sErr == nil && sRow != nil {
						if tStr := strings.TrimSpace(strField(sRow, "title")); tStr != "" {
							title = tStr
						}
						ws = strField(sRow, "workspace")
					}
					sr = &directSessionRow{
						SessionID:      sid,
						AgentID:        aid,
						AgentName:      desc.Name,
						AgentKind:      desc.Kind,
						ConnectionID:   desc.ConnectionID,
						ConnectionName: desc.ConnectionName,
						Title:          title,
						Workspace:      ws,
						Model:          model,
						UpdatedAt:      fmt.Sprint(r["updated_at"]),
					}
					sessAgg[sessKey] = sr
				}
				sr.Requests++
				sr.InputTokens += uncachedIn
				sr.OutputTokens += dOut
				sr.CacheRead += dCache
				sr.Reasoning += dReason
				sr.TotalTokens += uncachedIn + dCache + dOut
				sr.Model = model
			}
			for _, sr := range sessAgg {
				agg.Sessions = append(agg.Sessions, *sr)
			}
			mu.Unlock()
		}
	}

	wg.Wait()

	sort.Slice(agg.Sessions, func(i, j int) bool {
		if agg.Sessions[i].TotalTokens != agg.Sessions[j].TotalTokens {
			return agg.Sessions[i].TotalTokens > agg.Sessions[j].TotalTokens
		}
		return agg.Sessions[i].UpdatedAt > agg.Sessions[j].UpdatedAt
	})
	if len(agg.Sessions) > 50 {
		agg.Sessions = agg.Sessions[:50]
	}
	return agg, nil
}

func (agg *directUsageAggregate) toResponse(period, surface string) map[string]any {
	totalTokens := agg.Totals.totalTokens()

	models := make([]map[string]any, 0, len(agg.Models))
	providers := map[string]magpieTotals{}
	for _, g := range agg.Models {
		row := g.metrics()
		row["provider"] = g.Provider
		row["model"] = g.Model
		row["shareRatio"] = ratio(g.totalTokens(), totalTokens)
		models = append(models, row)
		pt := providers[g.Provider]
		pt.add(g.magpieTotals)
		providers[g.Provider] = pt
	}
	sort.Slice(models, func(i, j int) bool {
		ti, _ := models[i]["totalTokens"].(int64)
		tj, _ := models[j]["totalTokens"].(int64)
		if ti != tj {
			return ti > tj
		}
		return fmt.Sprint(models[i]["model"]) < fmt.Sprint(models[j]["model"])
	})

	providerRows := make([]map[string]any, 0, len(providers))
	for p, t := range providers {
		row := t.metrics()
		row["provider"] = p
		row["shareRatio"] = ratio(t.totalTokens(), totalTokens)
		providerRows = append(providerRows, row)
	}
	sort.Slice(providerRows, func(i, j int) bool {
		return providerRows[i]["provider"].(string) < providerRows[j]["provider"].(string)
	})

	accounts := make([]map[string]any, 0, len(agg.Accounts))
	for _, g := range agg.Accounts {
		row := g.metrics()
		row["accountLogLabel"] = g.Provider + " · " + g.Account
		row["ambiguous"] = false
		accounts = append(accounts, row)
	}
	sort.Slice(accounts, func(i, j int) bool {
		return accounts[i]["accountLogLabel"].(string) < accounts[j]["accountLogLabel"].(string)
	})

	days := make([]map[string]any, 0, len(agg.DayTotals))
	for day, t := range agg.DayTotals {
		row := t.metrics()
		row["date"] = day
		if dm := agg.DayModels[day]; len(dm) > 0 {
			dayModelList := make([]map[string]any, 0, len(dm))
			for _, g := range dm {
				mRow := g.metrics()
				mRow["provider"] = g.Provider
				mRow["model"] = g.Model
				dayModelList = append(dayModelList, mRow)
			}
			sort.Slice(dayModelList, func(i, j int) bool {
				ti, _ := dayModelList[i]["totalTokens"].(int64)
				tj, _ := dayModelList[j]["totalTokens"].(int64)
				return ti > tj
			})
			row["models"] = dayModelList
		}
		days = append(days, row)
	}
	sort.Slice(days, func(i, j int) bool {
		return days[i]["date"].(string) < days[j]["date"].(string)
	})

	activeServerSet := map[string]bool{}
	agentRows := make([]map[string]any, 0, len(agg.Agents))
	for _, a := range agg.Agents {
		if a.ConnectionID != "" {
			activeServerSet[a.ConnectionID] = true
		}
		row := a.magpieTotals.metrics()
		row["agentId"] = a.AgentID
		row["name"] = a.Name
		row["kind"] = a.Kind
		row["connectionId"] = a.ConnectionID
		row["connectionName"] = a.ConnectionName
		row["sessions"] = a.Sessions
		row["shareRatio"] = ratio(a.magpieTotals.totalTokens(), totalTokens)
		agentRows = append(agentRows, row)
	}
	sort.Slice(agentRows, func(i, j int) bool {
		ti, _ := agentRows[i]["totalTokens"].(int64)
		tj, _ := agentRows[j]["totalTokens"].(int64)
		if ti != tj {
			return ti > tj
		}
		return fmt.Sprint(agentRows[i]["name"]) < fmt.Sprint(agentRows[j]["name"])
	})

	serverRows := make([]map[string]any, 0, len(agg.Servers))
	for _, s := range agg.Servers {
		serverRows = append(serverRows, map[string]any{
			"id":   s.ID,
			"name": s.Name,
		})
	}

	summary := agg.Totals.metrics()
	summary["activeAgents"] = len(agentRows)
	summary["activeSessions"] = agg.ActiveSessions
	summary["activeServers"] = len(activeServerSet)

	sessionsOut := agg.Sessions
	if sessionsOut == nil {
		sessionsOut = []directSessionRow{}
	}

	sinceUnixMs := int64(0)
	if !agg.Since.IsZero() {
		sinceUnixMs = agg.Since.UnixMilli()
	}

	gatewayType := "sessions"
	if agg.DirectOnly {
		gatewayType = "direct"
	}
	connID := agg.ConnectionID
	if connID == "" {
		connID = "all"
	}

	return map[string]any{
		"gatewayType":           gatewayType,
		"bucket":                "day",
		"range":                 period,
		"surface":               surface,
		"connectionId":          connID,
		"since":                 sinceUnixMs,
		"generatedAt":           time.Now().UTC().Format(time.RFC3339),
		"summary":               summary,
		"days":                  days,
		"models":                models,
		"providers":             providerRows,
		"accounts":              accounts,
		"servers":               serverRows,
		"directAgents":          agentRows,
		"directSessions":        sessionsOut,
		"daemonUpgradeRequired": agg.DaemonUpgradeRequired,
	}
}

func mergeDirectIntoMagpieSummary(s *magpieSummary, direct *directUsageAggregate) {
	if s == nil || direct == nil || (direct.Totals.totalTokens() <= 0 && direct.Totals.Calls <= 0) {
		return
	}
	s.magpieTotals.add(direct.Totals)
	if !direct.Since.IsZero() && (s.Since.IsZero() || direct.Since.Before(s.Since)) {
		s.Since = direct.Since
	}

	modelIdx := map[string]int{}
	for i, m := range s.Models {
		modelIdx[m.Provider+"\x00"+m.Model] = i
	}
	for k, dm := range direct.Models {
		if idx, ok := modelIdx[k]; ok {
			s.Models[idx].magpieTotals.add(dm.magpieTotals)
		} else {
			modelIdx[k] = len(s.Models)
			s.Models = append(s.Models, dm)
		}
	}

	acctIdx := map[string]int{}
	for i, a := range s.Accounts {
		acctIdx[a.Provider+"\x00"+a.Account] = i
	}
	for k, da := range direct.Accounts {
		if idx, ok := acctIdx[k]; ok {
			s.Accounts[idx].magpieTotals.add(da.magpieTotals)
		} else {
			acctIdx[k] = len(s.Accounts)
			s.Accounts = append(s.Accounts, da)
		}
	}

	dayIdx := map[string]int{}
	for i, p := range s.Series {
		day := p.Time.In(time.Local).Format("2006-01-02")
		dayIdx[day] = i
	}
	for day, dt := range direct.DayTotals {
		if idx, ok := dayIdx[day]; ok {
			s.Series[idx].magpieTotals.add(dt)
		} else {
			parsed, err := time.ParseInLocation("2006-01-02", day, time.Local)
			if err != nil {
				parsed = time.Now().In(time.Local)
			}
			dayIdx[day] = len(s.Series)
			s.Series = append(s.Series, magpiePoint{
				Time:         parsed,
				magpieTotals: dt,
			})
		}
	}
}

func mergeDirectIntoGenericResponse(data map[string]any, direct *directUsageAggregate) {
	if data == nil || direct == nil || direct.Totals.totalTokens() <= 0 {
		return
	}
	dirResp := direct.toResponse(strField(data, "range"), strField(data, "surface"))

	if sumMap, ok := data["summary"].(map[string]any); ok {
		sumMap["requests"] = int64MapField(sumMap, "requests") + direct.Totals.Calls
		sumMap["totalTokens"] = int64MapField(sumMap, "totalTokens") + direct.Totals.totalTokens()
		sumMap["inputTokens"] = int64MapField(sumMap, "inputTokens") + direct.Totals.Input
		sumMap["outputTokens"] = int64MapField(sumMap, "outputTokens") + direct.Totals.Output
		sumMap["cachedInputTokens"] = int64MapField(sumMap, "cachedInputTokens") + direct.Totals.CacheRead
		sumMap["cacheReadInputTokens"] = int64MapField(sumMap, "cacheReadInputTokens") + direct.Totals.CacheRead
		sumMap["cacheCreationInputTokens"] = int64MapField(sumMap, "cacheCreationInputTokens") + direct.Totals.CacheWrite
		sumMap["reasoningOutputTokens"] = int64MapField(sumMap, "reasoningOutputTokens") + direct.Totals.Reasoning
	}

	if dirModels, ok := dirResp["models"].([]map[string]any); ok && len(dirModels) > 0 {
		existing, _ := data["models"].([]any)
		for _, dm := range dirModels {
			existing = append(existing, dm)
		}
		data["models"] = existing
	}

	if dirDays, ok := dirResp["days"].([]map[string]any); ok && len(dirDays) > 0 {
		existingDays, _ := data["days"].([]any)
		byDate := map[string]map[string]any{}
		for _, rawD := range existingDays {
			if dm, ok := rawD.(map[string]any); ok {
				if dStr := strField(dm, "date"); dStr != "" {
					byDate[dStr] = dm
				}
			}
		}
		for _, dd := range dirDays {
			dStr := strField(dd, "date")
			if cur, exists := byDate[dStr]; exists {
				cur["requests"] = int64MapField(cur, "requests") + int64MapField(dd, "requests")
				cur["totalTokens"] = int64MapField(cur, "totalTokens") + int64MapField(dd, "totalTokens")
				if ddModels, ok := dd["models"].([]map[string]any); ok && len(ddModels) > 0 {
					curModels, _ := cur["models"].([]any)
					for _, m := range ddModels {
						curModels = append(curModels, m)
					}
					cur["models"] = curModels
				}
			} else {
				existingDays = append(existingDays, dd)
			}
		}
		data["days"] = existingDays
	}
}
