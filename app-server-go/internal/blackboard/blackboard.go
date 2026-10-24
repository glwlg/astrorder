package blackboard

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"astrorder.dev/app-server/internal/store"
)

// Publisher interface for publishing blackboard change events.
type Publisher interface {
	Publish(ctx context.Context, eventType string, data map[string]any) error
}

// Service handles blackboard read/write/delete and auto-render.
type Service struct {
	store     *store.Store
	publisher Publisher
}

// NewService creates a blackboard Service.
func NewService(st *store.Store, pub Publisher) *Service {
	return &Service{
		store:     st,
		publisher: pub,
	}
}

// ResolveNamespace determines the blackboard namespace based on priority rules:
// 1. Explicit non-empty namespace other than "session:default"
// 2. session_key: group::id -> group:id, swarm::root -> swarm:root, agent::session -> stored scope or session:agent::session
// 3. group_id -> group:id
// 4. parent_key ancestry walk -> swarm:rootAgent::rootSession
// 5. Explicit caller agent/session fields resolved through delegation edges
func (s *Service) ResolveNamespace(ctx context.Context, payload map[string]any) (string, error) {
	// 1. Caller validation first: caller errors/conflicts MUST NOT be bypassed by explicit namespace/session_key
	callerAid, callerSid, hasCaller, cErr := extractCaller(payload)
	if cErr != nil {
		return "", cErr
	}

	// 2. Explicit namespace (e.g. global, custom, or explicit session namespace)
	if rawNS, exists := payload["namespace"]; exists && rawNS != nil {
		val, ok := rawNS.(string)
		if !ok {
			return "", errors.New("namespace must be a string")
		}
		if val == "" {
			return "", errors.New("namespace cannot be empty")
		}
		if strings.ContainsAny(val, " 	\r\n") {
			return "", errors.New("namespace contains illegal whitespace")
		}
		if val != "session:default" {
			if !strings.HasPrefix(val, "session:") {
				return val, nil
			}
			sessionRef := strings.TrimPrefix(val, "session:")
			if sessionRef == "" {
				return "", errors.New("session namespace reference cannot be empty")
			}
			if strings.Contains(sessionRef, "::") {
				parts := strings.SplitN(sessionRef, "::", 2)
				aid, sid := parts[0], parts[1]
				if err := validateIdentifier("agent_id", aid); err != nil {
					return "", err
				}
				if err := validateIdentifier("session_id", sid); err != nil {
					return "", err
				}
				scope, err := s.GetSessionScope(ctx, aid, sid)
				if err != nil {
					return "", err
				}
				return scope, nil
			}
			// Bare sessionRef: requires explicit agent_id, cannot guess
			if callerAid == "" {
				return "", errors.New("bare session_id requires explicit agent_id or composite key 'agent_id::session_id'")
			}
			if err := validateIdentifier("session_id", sessionRef); err != nil {
				return "", err
			}
			scope, err := s.GetSessionScope(ctx, callerAid, sessionRef)
			if err != nil {
				return "", err
			}
			return scope, nil
		}
	}

	// 3. Explicit session_key (e.g. group::id, swarm::root, agent::session)
	if rawSK, exists := payload["session_key"]; exists && rawSK != nil {
		sk, ok := rawSK.(string)
		if !ok {
			return "", errors.New("session_key must be a string")
		}
		if sk == "" {
			return "", errors.New("session_key cannot be empty")
		}
		if strings.ContainsAny(sk, " 	\r\n") {
			return "", errors.New("session_key contains illegal whitespace")
		}
		if strings.HasPrefix(sk, "group::") {
			groupID := strings.TrimPrefix(sk, "group::")
			if err := validateIdentifier("group_id", groupID); err != nil {
				return "", err
			}
			return "group:" + groupID, nil
		}
		if strings.HasPrefix(sk, "swarm::") {
			rootID := strings.TrimPrefix(sk, "swarm::")
			if err := validateIdentifier("swarm_root", rootID); err != nil {
				return "", err
			}
			return "swarm:" + rootID, nil
		}
		if strings.Contains(sk, "::") {
			parts := strings.SplitN(sk, "::", 2)
			aid, sid := parts[0], parts[1]
			if err := validateIdentifier("agent_id", aid); err != nil {
				return "", err
			}
			if err := validateIdentifier("session_id", sid); err != nil {
				return "", err
			}
			scope, err := s.GetSessionScope(ctx, aid, sid)
			if err != nil {
				return "", err
			}
			return scope, nil
		}
		// Bare session_key: requires explicit agent_id, cannot guess
		if callerAid == "" {
			return "", errors.New("bare session_key requires explicit agent_id or composite key 'agent_id::session_id'")
		}
		if err := validateIdentifier("session_id", sk); err != nil {
			return "", err
		}
		scope, err := s.GetSessionScope(ctx, callerAid, sk)
		if err != nil {
			return "", err
		}
		return scope, nil
	}

	// 4. Explicit group_id
	if rawGID, exists := payload["group_id"]; exists && rawGID != nil {
		groupID, ok := rawGID.(string)
		if !ok {
			return "", errors.New("group_id must be a string")
		}
		if err := validateIdentifier("group_id", groupID); err != nil {
			return "", err
		}
		return "group:" + groupID, nil
	}

	// 5. Explicit parent_key
	if rawPK, exists := payload["parent_key"]; exists && rawPK != nil {
		parentKey, ok := rawPK.(string)
		if !ok {
			return "", errors.New("parent_key must be a string")
		}
		if !strings.Contains(parentKey, "::") {
			return "", errors.New("parent_key must be 'agent_id::session_id'")
		}
		if strings.ContainsAny(parentKey, " 	\r\n") {
			return "", errors.New("parent_key contains illegal whitespace")
		}
		parts := strings.SplitN(parentKey, "::", 2)
		aid, sid := parts[0], parts[1]
		if err := validateIdentifier("parent_agent_id", aid); err != nil {
			return "", err
		}
		if err := validateIdentifier("parent_session_id", sid); err != nil {
			return "", err
		}
		if s.store != nil {
			_, hasParent, err := s.store.GetDelegationParent(ctx, aid, sid)
			if err != nil {
				return "", fmt.Errorf("check delegation parent: %w", err)
			}
			children, err := s.store.ListDelegationChildren(ctx, aid, sid)
			if err != nil {
				return "", fmt.Errorf("check delegation children: %w", err)
			}
			if hasParent || len(children) > 0 {
				root, rErr := s.store.ResolveDelegationRoot(ctx, aid, sid)
				if rErr != nil {
					return "", fmt.Errorf("resolve delegation root: %w", rErr)
				}
				if root.AgentID != "" && root.SessionID != "" {
					return fmt.Sprintf("swarm:%s::%s", root.AgentID, root.SessionID), nil
				}
			}
		}
		// Not part of a delegation edge: do not guess along parent_session_id / handoff
		return fmt.Sprintf("session:%s::%s", aid, sid), nil
	}

	// 6. Valid explicit caller identity
	if hasCaller && callerAid != "" && callerSid != "" {
		scope, err := s.GetSessionScope(ctx, callerAid, callerSid)
		if err != nil {
			return "", err
		}
		return scope, nil
	}

	return "", errors.New("Blackboard namespace could not be resolved: missing exact caller identity, session_key, or explicit namespace")
}

func validateIdentifier(name, val string) error {
	if val == "" {
		return fmt.Errorf("%s cannot be empty", name)
	}
	if strings.ContainsAny(val, " 	\r\n") {
		return fmt.Errorf("%s contains illegal whitespace", name)
	}
	return nil
}

func extractCaller(payload map[string]any) (string, string, bool, error) {
	var callerAid, callerSid string
	hasCallerMap := false

	if rawCaller, exists := payload["caller"]; exists {
		if rawCaller == nil {
			return "", "", false, errors.New("caller cannot be null")
		}
		callerMap, isMap := rawCaller.(map[string]any)
		if !isMap {
			return "", "", false, errors.New("caller must be a JSON object")
		}
		if len(callerMap) == 0 {
			return "", "", false, errors.New("caller object cannot be empty")
		}

		aidVal, hasAid := callerMap["aid"]
		agentIdVal, hasAgentId := callerMap["agent_id"]
		if hasAid && hasAgentId && aidVal != agentIdVal {
			return "", "", false, errors.New("conflicting aid and agent_id in caller object")
		}
		if hasAid {
			s, ok := aidVal.(string)
			if !ok {
				return "", "", false, errors.New("caller aid must be a string")
			}
			callerAid = s
		} else if hasAgentId {
			s, ok := agentIdVal.(string)
			if !ok {
				return "", "", false, errors.New("caller agent_id must be a string")
			}
			callerAid = s
		}

		sidVal, hasSid := callerMap["sid"]
		sessionIdVal, hasSessionId := callerMap["session_id"]
		if hasSid && hasSessionId && sidVal != sessionIdVal {
			return "", "", false, errors.New("conflicting sid and session_id in caller object")
		}
		if hasSid {
			s, ok := sidVal.(string)
			if !ok {
				return "", "", false, errors.New("caller sid must be a string")
			}
			callerSid = s
		} else if hasSessionId {
			s, ok := sessionIdVal.(string)
			if !ok {
				return "", "", false, errors.New("caller session_id must be a string")
			}
			callerSid = s
		}

		if (callerAid != "" && callerSid == "") || (callerAid == "" && callerSid != "") {
			return "", "", false, errors.New("caller object must specify both agent_id and session_id")
		}
		hasCallerMap = true
	}

	var topAid string
	topAid1, hasTopAid1 := payload["caller_agent_id"]
	topAid2, hasTopAid2 := payload["agent_id"]
	if hasTopAid1 && topAid1 == nil {
		return "", "", false, errors.New("caller_agent_id cannot be null")
	}
	if hasTopAid2 && topAid2 == nil {
		return "", "", false, errors.New("agent_id cannot be null")
	}
	var sTopAid1, sTopAid2 string
	if hasTopAid1 {
		s, ok := topAid1.(string)
		if !ok {
			return "", "", false, errors.New("caller_agent_id must be a string")
		}
		sTopAid1 = s
	}
	if hasTopAid2 {
		s, ok := topAid2.(string)
		if !ok {
			return "", "", false, errors.New("agent_id must be a string")
		}
		sTopAid2 = s
	}
	if hasTopAid1 && hasTopAid2 && sTopAid1 != "" && sTopAid2 != "" && sTopAid1 != sTopAid2 {
		return "", "", false, errors.New("conflicting caller_agent_id and agent_id in payload")
	}
	if sTopAid1 != "" {
		topAid = sTopAid1
	} else {
		topAid = sTopAid2
	}

	var topSid string
	topSid1, hasTopSid1 := payload["caller_session_id"]
	topSid2, hasTopSid2 := payload["session_id"]
	if hasTopSid1 && topSid1 == nil {
		return "", "", false, errors.New("caller_session_id cannot be null")
	}
	if hasTopSid2 && topSid2 == nil {
		return "", "", false, errors.New("session_id cannot be null")
	}
	var sTopSid1, sTopSid2 string
	if hasTopSid1 {
		s, ok := topSid1.(string)
		if !ok {
			return "", "", false, errors.New("caller_session_id must be a string")
		}
		sTopSid1 = s
	}
	if hasTopSid2 {
		s, ok := topSid2.(string)
		if !ok {
			return "", "", false, errors.New("session_id must be a string")
		}
		sTopSid2 = s
	}
	if hasTopSid1 && hasTopSid2 && sTopSid1 != "" && sTopSid2 != "" && sTopSid1 != sTopSid2 {
		return "", "", false, errors.New("conflicting caller_session_id and session_id in payload")
	}
	if sTopSid1 != "" {
		topSid = sTopSid1
	} else {
		topSid = sTopSid2
	}

	if callerAid != "" && topAid != "" && callerAid != topAid {
		return "", "", false, errors.New("conflicting caller agent_id in payload")
	}
	if callerSid != "" && topSid != "" && callerSid != topSid {
		return "", "", false, errors.New("conflicting caller session_id in payload")
	}

	aid := callerAid
	if aid == "" {
		aid = topAid
	}
	sid := callerSid
	if sid == "" {
		sid = topSid
	}

	hasAny := hasCallerMap || hasTopAid1 || hasTopAid2 || hasTopSid1 || hasTopSid2
	if !hasAny {
		return "", "", false, nil
	}

	if aid != "" {
		if err := validateIdentifier("agent_id", aid); err != nil {
			return "", "", false, err
		}
	}
	if sid != "" {
		if err := validateIdentifier("session_id", sid); err != nil {
			return "", "", false, err
		}
	}

	if (aid != "" && sid == "") || (aid == "" && sid != "") {
		return "", "", false, errors.New("caller must specify both agent_id and session_id")
	}

	return aid, sid, true, nil
}

// GetSessionScope checks blackboard_scope:{aid}::{sid}, group_session: mapping,
// and real delegation_edges root resolution, strictly propagating DB errors.
func (s *Service) GetSessionScope(ctx context.Context, agentID, sessionID string) (string, error) {
	if s.store == nil {
		return "", errors.New("store is required to resolve session scope")
	}
	if err := validateIdentifier("agent_id", agentID); err != nil {
		return "", err
	}
	if err := validateIdentifier("session_id", sessionID); err != nil {
		return "", err
	}

	// 1. Explicit blackboard_scope preference
	key := fmt.Sprintf("blackboard_scope:%s::%s", agentID, sessionID)
	pref, err := s.store.GetPreference(ctx, key)
	if err != nil {
		return "", fmt.Errorf("read blackboard_scope preference: %w", err)
	}
	if pref.Exists {
		if scopeStr, ok := pref.Value.(string); ok && scopeStr != "" {
			return scopeStr, nil
		}
	}

	// 2. Check group_session:{group_id}:{agent_id} == session_id
	suffix := ":" + agentID
	rows, err := s.store.QueryRows(ctx, `SELECT "key", value FROM workspace_preferences WHERE "key" LIKE 'group_session:%'`)
	if err != nil {
		return "", fmt.Errorf("query group_session preferences: %w", err)
	}
	for _, row := range rows {
		k, _ := row["key"].(string)
		v, _ := row["value"].(string)
		vClean := strings.Trim(v, `"`)
		if strings.HasSuffix(k, suffix) && vClean == sessionID {
			groupID := strings.TrimSuffix(strings.TrimPrefix(k, "group_session:"), suffix)
			return "group:" + groupID, nil
		}
	}

	// 3. Check real delegation edges
	_, hasParent, err := s.store.GetDelegationParent(ctx, agentID, sessionID)
	if err != nil {
		return "", fmt.Errorf("get delegation parent: %w", err)
	}
	children, err := s.store.ListDelegationChildren(ctx, agentID, sessionID)
	if err != nil {
		return "", fmt.Errorf("list delegation children: %w", err)
	}
	if hasParent || len(children) > 0 {
		root, rErr := s.store.ResolveDelegationRoot(ctx, agentID, sessionID)
		if rErr != nil {
			return "", fmt.Errorf("resolve delegation root: %w", rErr)
		}
		if root.AgentID != "" && root.SessionID != "" {
			return fmt.Sprintf("swarm:%s::%s", root.AgentID, root.SessionID), nil
		}
	}

	return fmt.Sprintf("session:%s::%s", agentID, sessionID), nil
}

// Get retrieves a key or all keys in a namespace.
func (s *Service) Get(ctx context.Context, payload map[string]any) (map[string]any, error) {
	ns, err := s.ResolveNamespace(ctx, payload)
	if err != nil {
		return nil, err
	}

	keyRaw := payload["key"]
	var key string
	if kStr, ok := keyRaw.(string); ok && strings.TrimSpace(kStr) != "" {
		key = strings.TrimSpace(kStr)
	}

	if key != "" {
		dbKey := fmt.Sprintf("blackboard:%s:%s", ns, key)
		pref, err := s.store.GetPreference(ctx, dbKey)
		if err != nil {
			return nil, err
		}
		return map[string]any{
			"namespace": ns,
			"key":       key,
			"value":     pref.Value,
			"exists":    pref.Exists,
		}, nil
	}

	prefix := fmt.Sprintf("blackboard:%s:", ns)
	query := `SELECT "key", value FROM workspace_preferences WHERE instr("key", ?) = 1 ORDER BY "key"`
	rows, err := s.store.QueryRows(ctx, query, prefix)
	if err != nil {
		return nil, err
	}

	items := make(map[string]any, len(rows))
	for _, row := range rows {
		k, _ := row["key"].(string)
		shortKey := strings.TrimPrefix(k, prefix)
		items[shortKey] = row["value"]
	}

	return map[string]any{
		"namespace": ns,
		"items":     items,
		"count":     len(items),
	}, nil
}

// Set writes a value into the blackboard in the resolved namespace.
func (s *Service) Set(ctx context.Context, payload map[string]any) (map[string]any, error) {
	rawKey, _ := payload["key"].(string)
	key := strings.TrimSpace(rawKey)
	if key == "" {
		return nil, errors.New("key is required")
	}

	ns, err := s.ResolveNamespace(ctx, payload)
	if err != nil {
		return nil, err
	}

	value := payload["value"]
	value = enrichComponentPayload(key, value)

	dbKey := fmt.Sprintf("blackboard:%s:%s", ns, key)
	if err := s.store.SetPreference(ctx, dbKey, value, true); err != nil {
		return nil, fmt.Errorf("failed to persist blackboard key: %w", err)
	}

	if s.publisher != nil {
		_ = s.publisher.Publish(ctx, "blackboard.change", map[string]any{
			"action":    "set",
			"namespace": ns,
			"key":       key,
			"value":     value,
		})
	}

	// If group namespace, broadcast group notice to bot_group_msgs
	if strings.HasPrefix(ns, "group:") && s.store != nil {
		gid := strings.TrimPrefix(ns, "group:")
		s.appendGroupNotice(ctx, gid, key, payload)
	}

	return map[string]any{
		"ok":        true,
		"namespace": ns,
		"key":       key,
		"value":     value,
	}, nil
}

// Delete removes a key or wipes the namespace if key is "*" or clean_namespace is true.
func (s *Service) Delete(ctx context.Context, payload map[string]any) (map[string]any, error) {
	ns, err := s.ResolveNamespace(ctx, payload)
	if err != nil {
		return nil, err
	}

	cleanNS := payload["clean_namespace"] == true
	rawKey, _ := payload["key"].(string)
	key := strings.TrimSpace(rawKey)
	if key == "*" {
		cleanNS = true
	}

	if cleanNS {
		prefix := fmt.Sprintf("blackboard:%s:", ns)
		err := s.store.WithTx(ctx, func(tx *sql.Tx) error {
			_, e := tx.ExecContext(ctx, `DELETE FROM workspace_preferences WHERE instr("key", ?) = 1`, prefix)
			return e
		})
		if err != nil {
			return nil, err
		}
		key = "*"
	} else {
		if key == "" {
			return nil, errors.New("key is required")
		}
		dbKey := fmt.Sprintf("blackboard:%s:%s", ns, key)
		_, err := s.store.DeletePreference(ctx, dbKey)
		if err != nil {
			return nil, err
		}
	}

	if s.publisher != nil {
		_ = s.publisher.Publish(ctx, "blackboard.change", map[string]any{
			"action":    "delete",
			"namespace": ns,
			"key":       key,
		})
	}

	return map[string]any{
		"ok":        true,
		"namespace": ns,
		"key":       key,
	}, nil
}

// AutoRender analyzes content and formats it into a component-shaped JSON card on blackboard.
func (s *Service) AutoRender(ctx context.Context, payload map[string]any) (map[string]any, error) {
	rawKey, _ := payload["key"].(string)
	key := strings.TrimSpace(rawKey)
	if key == "" {
		return nil, errors.New("key is required")
	}

	rawContent, _ := payload["content"].(string)
	content := strings.TrimSpace(rawContent)
	if content == "" {
		return nil, errors.New("content is required")
	}

	title, _ := payload["title"].(string)
	if title == "" {
		title = strings.Title(strings.ReplaceAll(key, "_", " "))
	}

	lines := splitCleanLines(content)
	comp := inferComponentFromContent(content, lines)

	rendered := map[string]any{
		"component": comp,
		"title":     title,
	}

	switch comp {
	case "StepTimeline":
		var steps []map[string]any
		var cleanLines []string
		for _, l := range lines {
			if !strings.HasSuffix(l, "：") && !strings.HasSuffix(l, ":") && !strings.Contains(l, "路线图") {
				cleanLines = append(cleanLines, l)
			}
		}
		for idx, line := range cleanLines {
			status := "pending"
			low := strings.ToLower(line)
			if containsAny(low, "completed", "complete", "ok", "pass", "done", "成功", "完成", "已完成", "已落地", "已建立", "已声明") {
				status = "completed"
			} else if containsAny(low, "failed", "fail", "error", "err", "失败", "异常") {
				status = "failed"
			} else if idx == len(cleanLines)-1 {
				status = "running"
			}
			steps = append(steps, map[string]any{"title": line, "status": status})
		}
		if len(steps) == 0 {
			steps = []map[string]any{{"title": content, "status": "completed"}}
		}
		rendered["steps"] = steps

	case "MetricGrid":
		var metrics []map[string]any
		limit := len(lines)
		if limit > 8 {
			limit = 8
		}
		for idx, line := range lines[:limit] {
			if strings.Contains(line, ":") || strings.Contains(line, "=") || strings.Contains(line, "：") {
				rep := strings.ReplaceAll(strings.ReplaceAll(line, "：", ":"), "=", ":")
				parts := strings.SplitN(rep, ":", 2)
				metrics = append(metrics, map[string]any{"label": strings.TrimSpace(parts[0]), "value": strings.TrimSpace(parts[1])})
			} else {
				metrics = append(metrics, map[string]any{"label": fmt.Sprintf("Metric %d", idx+1), "value": line})
			}
		}
		rendered["metrics"] = metrics

	case "Checklist":
		var items []map[string]any
		for _, line := range lines {
			low := strings.ToLower(line)
			checked := containsAny(low, "[x]", "done", "pass", "ok", "完成", "通过")
			label := strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(line, "[x]", ""), "[ ]", ""))
			items = append(items, map[string]any{"label": label, "checked": checked})
		}
		rendered["items"] = items

	case "CveSecurityReport":
		summary := content
		if len(summary) > 300 {
			summary = summary[:300]
		}
		rendered["summary"] = summary
		sev := "medium"
		if containsAny(strings.ToLower(content), "high", "critical", "严重") {
			sev = "high"
		}
		rendered["severity"] = sev
		var findings []map[string]any
		limit := len(lines)
		if limit > 5 {
			limit = 5
		}
		for _, l := range lines[:limit] {
			findings = append(findings, map[string]any{"title": l, "severity": "medium"})
		}
		rendered["findings"] = findings

	case "TestReport":
		rendered["total"] = len(lines)
		passed := 0
		for _, l := range lines {
			if containsAny(strings.ToLower(l), "pass", "ok", "通过", "成功") {
				passed++
			}
		}
		rendered["passed"] = passed
		rendered["failed"] = len(lines) - passed
		var tests []map[string]any
		limit := len(lines)
		if limit > 10 {
			limit = 10
		}
		for _, l := range lines[:limit] {
			st := "failed"
			if containsAny(strings.ToLower(l), "pass", "ok") {
				st = "passed"
			}
			tests = append(tests, map[string]any{"name": l, "status": st})
		}
		rendered["tests"] = tests

	default:
		rendered["status"] = "info"
		rendered["description"] = content
	}

	setPayload := make(map[string]any, len(payload)+2)
	for k, v := range payload {
		setPayload[k] = v
	}
	setPayload["key"] = key
	setPayload["value"] = rendered

	setRes, err := s.Set(ctx, setPayload)
	if err != nil {
		return nil, err
	}

	return map[string]any{
		"ok":           true,
		"namespace":    setRes["namespace"],
		"key":          key,
		"component":    comp,
		"jev_decision": map[string]any{"fallback": true},
		"rendered":     rendered,
	}, nil
}

func enrichComponentPayload(key string, value any) any {
	if m, ok := value.(map[string]any); ok {
		if _, hasComp := m["component"]; !hasComp {
			if _, hasType := m["type"]; !hasType {
				if hasKeyAny(m, "steps", "pipeline", "stages", "milestones") {
					m["component"] = "StepTimeline"
				} else if hasKeyAny(m, "items", "checklist", "todos", "tasks") {
					m["component"] = "Checklist"
				} else if hasKeyAny(m, "metrics", "counters", "stats", "kpi") {
					m["component"] = "MetricGrid"
				} else if hasKeyAny(m, "rows", "records", "data") {
					m["component"] = "DataTable"
				} else if hasKeyAny(m, "endpoints", "apis", "routes") {
					m["component"] = "ApiEndpointsCard"
				}
				return m
			}
		}
	} else if str, ok := value.(string); ok && strings.TrimSpace(str) != "" {
		return map[string]any{
			"component": "StatusCard",
			"title":     strings.Title(strings.ReplaceAll(key, "_", " ")),
			"content":   strings.TrimSpace(str),
		}
	}
	return value
}

func hasKeyAny(m map[string]any, keys ...string) bool {
	for _, k := range keys {
		if _, ok := m[k]; ok {
			return true
		}
	}
	return false
}

func splitCleanLines(s string) []string {
	raw := strings.Split(s, "\n")
	var res []string
	for _, line := range raw {
		trimmed := strings.TrimSpace(line)
		if trimmed != "" {
			res = append(res, trimmed)
		}
	}
	return res
}

func inferComponentFromContent(content string, lines []string) string {
	low := strings.ToLower(content)
	if containsAny(low, "pipeline", "stage", "step", "阶段", "步骤", "流水线") {
		return "StepTimeline"
	}
	if containsAny(low, "metric", "qps", "kpi", "指标", "延时", "吞吐") {
		return "MetricGrid"
	}
	if containsAny(low, "checklist", "todo", "验收", "待办", "[ ]", "[x]") {
		return "Checklist"
	}
	if containsAny(low, "test", "passed", "failed", "测试用例") {
		return "TestReport"
	}
	if containsAny(low, "cve", "vulnerability", "漏洞", "合规") {
		return "CveSecurityReport"
	}
	return "StatusCard"
}

func containsAny(s string, substrs ...string) bool {
	for _, sub := range substrs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

func (s *Service) appendGroupNotice(ctx context.Context, gid, key string, payload map[string]any) {
	sender, _ := payload["caller_agent_id"].(string)
	if sender == "" {
		sender, _ = payload["agent_id"].(string)
	}
	if sender == "" {
		sender = "Agent"
	}
	noticeID := fmt.Sprintf("gmsg-bb-%d", time.Now().UnixNano())
	notice := map[string]any{
		"id":          noticeID,
		"group_id":    gid,
		"sender_type": "system",
		"sender_id":   "astrorder:blackboard",
		"sender_name": "作战黑板",
		"text":        fmt.Sprintf("📋 【黑板更新】%s 更新了黑板条目「%s」", sender, key),
		"mentions":    []any{},
		"hop_count":   0,
		"created_at":  time.Now().UTC().Format(time.RFC3339Nano),
	}

	prefKey := "bot_group_msgs:" + gid
	_ = s.store.WithTx(ctx, func(tx *sql.Tx) error {
		row, err := s.store.GetPreference(ctx, prefKey)
		var list []any
		if err == nil && row.Exists {
			if l, ok := row.Value.([]any); ok {
				list = l
			}
		}
		list = append(list, notice)
		raw, mErr := json.Marshal(list)
		if mErr != nil {
			return mErr
		}
		_, execErr := tx.ExecContext(ctx, `INSERT INTO workspace_preferences("key", value) VALUES(?,?) ON CONFLICT("key") DO UPDATE SET value=excluded.value`, prefKey, string(raw))
		return execErr
	})

	if s.publisher != nil {
		_ = s.publisher.Publish(ctx, "bot_group.message", notice)
	}
}
