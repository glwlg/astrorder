package swarm

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

	"astrorder.dev/app-server/internal/store"
)

// Publisher interface for swarm events.
type Publisher interface {
	Publish(ctx context.Context, eventType string, data map[string]any) error
}

// SessionSender sends instructions to sessions (e.g. for SOS queen wake).
type SessionSender interface {
	Send(ctx context.Context, sessionKey, text string) error
}

// Service manages swarm milestones, telemetry, SOS escalations, and exclusive locks.
type Service struct {
	store     *store.Store
	publisher Publisher
	sender    SessionSender
}

// NewService constructs a swarm Service.
func NewService(st *store.Store, pub Publisher, sender SessionSender) *Service {
	return &Service{
		store:     st,
		publisher: pub,
		sender:    sender,
	}
}

// MilestoneDeclare declares an expected milestone.
func (s *Service) MilestoneDeclare(ctx context.Context, payload map[string]any) (map[string]any, error) {
	rawID, _ := payload["milestone_id"].(string)
	mid := strings.TrimSpace(rawID)
	if mid == "" {
		return nil, errors.New("milestone_id is required")
	}

	title, _ := payload["title"].(string)
	if strings.TrimSpace(title) == "" {
		title = mid
	} else {
		title = strings.TrimSpace(title)
	}

	wakeKey, _ := payload["wake_session_key"].(string)
	wakePrompt, _ := payload["wake_prompt"].(string)
	metadata, _ := payload["metadata"].(map[string]any)
	if metadata == nil {
		metadata = make(map[string]any)
	}

	record := map[string]any{
		"id":               mid,
		"title":            title,
		"status":           "pending",
		"wake_session_key": nullableTrimmedString(wakeKey),
		"wake_prompt":      nullableTrimmedString(wakePrompt),
		"metadata":         metadata,
		"created_at":       time.Now().UTC().Format(time.RFC3339Nano),
		"resolved_at":      nil,
		"result":           nil,
	}

	dbKey := "milestone:" + mid
	if err := s.store.SetPreference(ctx, dbKey, record, true); err != nil {
		return nil, err
	}

	if s.publisher != nil {
		_ = s.publisher.Publish(ctx, "swarm.milestone.event", map[string]any{
			"action":    "declare",
			"milestone": record,
		})
	}

	return map[string]any{"ok": true, "milestone": record}, nil
}

// MilestoneResolve marks a milestone as resolved.
func (s *Service) MilestoneResolve(ctx context.Context, payload map[string]any) (map[string]any, error) {
	rawID, _ := payload["milestone_id"].(string)
	mid := strings.TrimSpace(rawID)
	if mid == "" {
		return nil, errors.New("milestone_id is required")
	}

	resultData := payload["result"]
	dbKey := "milestone:" + mid

	var record map[string]any
	pref, err := s.store.GetPreference(ctx, dbKey)
	if err == nil && pref.Exists {
		if m, ok := pref.Value.(map[string]any); ok {
			record = m
		}
	}
	if record == nil {
		record = map[string]any{
			"id":               mid,
			"title":            mid,
			"status":           "pending",
			"wake_session_key": nil,
			"wake_prompt":      nil,
			"metadata":         map[string]any{},
			"created_at":       time.Now().UTC().Format(time.RFC3339Nano),
		}
	}

	record["status"] = "resolved"
	record["resolved_at"] = time.Now().UTC().Format(time.RFC3339Nano)
	record["result"] = resultData

	if err := s.store.SetPreference(ctx, dbKey, record, true); err != nil {
		return nil, err
	}

	if s.publisher != nil {
		_ = s.publisher.Publish(ctx, "swarm.milestone.event", map[string]any{
			"action":    "resolve",
			"milestone": record,
		})
	}

	// Wake blocked session if wake_session_key is set
	if wakeKey, ok := record["wake_session_key"].(string); ok && wakeKey != "" && s.sender != nil {
		prompt, _ := record["wake_prompt"].(string)
		if prompt == "" {
			prompt = fmt.Sprintf("Milestone '%s' has been resolved.", mid)
		}
		_ = s.sender.Send(ctx, wakeKey, prompt)
	}

	return map[string]any{"ok": true, "milestone": record}, nil
}

// MilestoneList lists all active, pending, and resolved milestones.
func (s *Service) MilestoneList(ctx context.Context) (map[string]any, error) {
	query := `SELECT "key", value FROM workspace_preferences WHERE "key" LIKE 'milestone:%' ORDER BY "key"`
	rows, err := s.store.QueryRows(ctx, query)
	if err != nil {
		return nil, err
	}
	var items []any
	for _, row := range rows {
		if m, ok := row["value"].(map[string]any); ok {
			items = append(items, m)
		}
	}
	if items == nil {
		items = []any{}
	}
	return map[string]any{"items": items, "count": len(items)}, nil
}

// TelemetryReport records structured progress telemetry.
func (s *Service) TelemetryReport(ctx context.Context, payload map[string]any) (map[string]any, error) {
	key, _ := payload["key"].(string)
	var agentID, sessionID string
	if key != "" && strings.Contains(key, "::") {
		parts := strings.SplitN(key, "::", 2)
		agentID, sessionID = parts[0], parts[1]
	} else if payload["session_id"] != nil && payload["agent_id"] != nil {
		agentID = fmt.Sprintf("%v", payload["agent_id"])
		sessionID = fmt.Sprintf("%v", payload["session_id"])
	} else {
		if key == "" {
			key = "anonymous_worker"
		}
	}

	canonicalKey := key
	if agentID != "" && sessionID != "" {
		canonicalKey = agentID + "::" + sessionID
	}

	progress := coerceInt(payload["progress"], 0)
	if progress < 0 {
		progress = 0
	} else if progress > 100 {
		progress = 100
	}

	phase := strings.TrimSpace(fmt.Sprintf("%v", payload["phase"]))
	if phase == "" || phase == "<nil>" {
		phase = "working"
	}

	status := strings.TrimSpace(fmt.Sprintf("%v", payload["status"]))
	if status == "" || status == "<nil>" {
		status = "ok"
	}

	summary := strings.TrimSpace(fmt.Sprintf("%v", payload["summary"]))
	if summary == "<nil>" {
		summary = ""
	}

	artifacts := payload["artifacts"]
	var artifactList []any
	if l, ok := artifacts.([]any); ok {
		artifactList = l
	} else if artifacts != nil {
		artifactList = []any{fmt.Sprintf("%v", artifacts)}
	} else {
		artifactList = []any{}
	}

	report := map[string]any{
		"key":        canonicalKey,
		"agent_id":   nullableString(agentID),
		"session_id": nullableString(sessionID),
		"progress":   progress,
		"phase":      phase,
		"status":     status,
		"summary":    summary,
		"artifacts":  artifactList,
		"updated_at": time.Now().UTC().Format(time.RFC3339Nano),
	}

	dbKey := "telemetry:" + canonicalKey
	if err := s.store.SetPreference(ctx, dbKey, report, true); err != nil {
		return nil, err
	}

	if s.publisher != nil {
		_ = s.publisher.Publish(ctx, "swarm.telemetry.event", report)
	}

	return map[string]any{"ok": true, "report": report}, nil
}

// TelemetryGet retrieves telemetry reports.
func (s *Service) TelemetryGet(ctx context.Context, payload map[string]any) (map[string]any, error) {
	rawKey, _ := payload["key"].(string)
	key := strings.TrimSpace(rawKey)
	if key != "" {
		dbKey := "telemetry:" + key
		pref, err := s.store.GetPreference(ctx, dbKey)
		if err != nil {
			return nil, err
		}
		return map[string]any{
			"key":    key,
			"report": pref.Value,
			"exists": pref.Exists,
		}, nil
	}

	prefix := "telemetry:"
	query := `SELECT "key", value FROM workspace_preferences WHERE "key" LIKE 'telemetry:%' ORDER BY "key"`
	rows, err := s.store.QueryRows(ctx, query)
	if err != nil {
		return nil, err
	}
	items := make(map[string]any, len(rows))
	for _, row := range rows {
		k, _ := row["key"].(string)
		items[strings.TrimPrefix(k, prefix)] = row["value"]
	}
	return map[string]any{"items": items, "count": len(items)}, nil
}

// SOSEscalate escalates an emergency blocker.
func (s *Service) SOSEscalate(ctx context.Context, payload map[string]any) (map[string]any, error) {
	rawReason, _ := payload["reason"].(string)
	reason := strings.TrimSpace(rawReason)
	if reason == "" {
		return nil, errors.New("reason is required")
	}

	key, _ := payload["key"].(string)
	contextDetail, _ := payload["context"].(string)
	targetKey, _ := payload["target_session_key"].(string)

	if targetKey == "" && key != "" && strings.Contains(key, "::") && s.store != nil {
		parts := strings.SplitN(key, "::", 2)
		sess, _ := s.store.GetSession(ctx, parts[0], parts[1])
		if sess != nil {
			pAid, _ := sess["parent_agent_id"].(string)
			if pAid == "" {
				pAid, _ = sess["handoff_from_agent_id"].(string)
			}
			pSid, _ := sess["parent_session_id"].(string)
			if pSid == "" {
				pSid, _ = sess["handoff_from_session_id"].(string)
			}
			if pAid != "" && pSid != "" {
				targetKey = pAid + "::" + pSid
			}
		}
	}

	randomHex := randomID(6)
	sosID := "sos_" + randomHex
	workerKey := key
	if workerKey == "" {
		workerKey = "anonymous_worker"
	}

	record := map[string]any{
		"id":          sosID,
		"worker_key":  workerKey,
		"target_key":  nullableTrimmedString(targetKey),
		"reason":      reason,
		"context":     contextDetail,
		"status":      "active",
		"created_at":  time.Now().UTC().Format(time.RFC3339Nano),
		"resolved_at": nil,
		"resolution":  nil,
	}

	dbKey := "sos:" + sosID
	if err := s.store.SetPreference(ctx, dbKey, record, true); err != nil {
		return nil, err
	}

	dispatched := false
	if targetKey != "" && s.sender != nil {
		sosMsg := fmt.Sprintf("🚨 【工蜂紧急求援 SOS】 节点: %s\n遇险原因: %s\n上下文详情: %s\nSOS 编号: %s。请评估是否派发辅助任务或下发纠偏指令。",
			workerKey, reason, contextDetail, sosID)
		if err := s.sender.Send(ctx, targetKey, sosMsg); err == nil {
			dispatched = true
		}
	}

	if s.publisher != nil {
		_ = s.publisher.Publish(ctx, "swarm.sos.event", map[string]any{
			"action":         "escalate",
			"sos":            record,
			"notified_queen": dispatched,
		})
	}

	return map[string]any{"ok": true, "sos": record, "notified_queen": dispatched}, nil
}

// SOSList lists active SOS escalations.
func (s *Service) SOSList(ctx context.Context) (map[string]any, error) {
	query := `SELECT "key", value FROM workspace_preferences WHERE "key" LIKE 'sos:%' ORDER BY "key"`
	rows, err := s.store.QueryRows(ctx, query)
	if err != nil {
		return nil, err
	}
	var items []any
	for _, row := range rows {
		if m, ok := row["value"].(map[string]any); ok {
			if m["status"] == "active" {
				items = append(items, m)
			}
		}
	}
	if items == nil {
		items = []any{}
	}
	return map[string]any{"items": items, "count": len(items)}, nil
}

// SOSResolve resolves an active SOS escalation.
func (s *Service) SOSResolve(ctx context.Context, payload map[string]any) (map[string]any, error) {
	rawID, _ := payload["sos_id"].(string)
	sosID := strings.TrimSpace(rawID)
	if sosID == "" {
		return nil, errors.New("sos_id is required")
	}

	resolution, _ := payload["resolution"].(string)
	if strings.TrimSpace(resolution) == "" {
		resolution = "Resolved by commander or orchestrator"
	} else {
		resolution = strings.TrimSpace(resolution)
	}

	dbKey := "sos:" + sosID
	pref, err := s.store.GetPreference(ctx, dbKey)
	if err != nil || !pref.Exists {
		return nil, fmt.Errorf("SOS alert '%s' was not found", sosID)
	}
	record, ok := pref.Value.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("SOS alert '%s' was not found", sosID)
	}

	record["status"] = "resolved"
	record["resolved_at"] = time.Now().UTC().Format(time.RFC3339Nano)
	record["resolution"] = resolution

	if err := s.store.SetPreference(ctx, dbKey, record, true); err != nil {
		return nil, err
	}

	if s.publisher != nil {
		_ = s.publisher.Publish(ctx, "swarm.sos.event", map[string]any{
			"action": "resolve",
			"sos":    record,
		})
	}

	return map[string]any{"ok": true, "sos": record}, nil
}

// LockAcquire attempts to acquire an exclusive lock in an atomic transaction.
func (s *Service) LockAcquire(ctx context.Context, payload map[string]any) (map[string]any, error) {
	rawRes, _ := payload["resource"].(string)
	resource := strings.TrimSpace(rawRes)
	if resource == "" {
		return nil, errors.New("resource identifier is required")
	}

	owner := "anonymous_worker"
	if o, ok := payload["owner_key"].(string); ok && strings.TrimSpace(o) != "" {
		owner = strings.TrimSpace(o)
	}

	ttl := coerceInt(payload["ttl_seconds"], 300)
	if ttl < 10 {
		ttl = 10
	} else if ttl > 3600 {
		ttl = 3600
	}

	now := time.Now().UTC()
	dbKey := "lock:" + resource
	var result map[string]any

	err := s.store.WithTx(ctx, func(tx *sql.Tx) error {
		row := tx.QueryRowContext(ctx, `SELECT value FROM workspace_preferences WHERE "key"=?`, dbKey)
		var storedVal string
		err := row.Scan(&storedVal)
		if err == nil {
			var m map[string]any
			if jsonErr := json.Unmarshal([]byte(storedVal), &m); jsonErr == nil {
				if expStr, ok := m["expires_at"].(string); ok {
					expTime, tErr := time.Parse(time.RFC3339Nano, expStr)
					if tErr != nil {
						expTime, tErr = time.Parse(time.RFC3339, expStr)
					}
					if tErr == nil && expTime.After(now) && m["owner_key"] != owner {
						result = map[string]any{
							"acquired":      false,
							"resource":      resource,
							"current_owner": m["owner_key"],
							"expires_at":    expStr,
							"message":       fmt.Sprintf("Resource '%s' is locked by %v until %s", resource, m["owner_key"], expStr),
						}
						return nil
					}
				}
			}
		} else if !errors.Is(err, sql.ErrNoRows) {
			return err
		}

		expiresAt := now.Add(time.Duration(ttl) * time.Second)
		lockRecord := map[string]any{
			"resource":    resource,
			"owner_key":   owner,
			"acquired_at": now.Format(time.RFC3339Nano),
			"expires_at":  expiresAt.Format(time.RFC3339Nano),
			"ttl_seconds": ttl,
		}
		raw, mErr := json.Marshal(lockRecord)
		if mErr != nil {
			return mErr
		}
		_, execErr := tx.ExecContext(ctx, `INSERT INTO workspace_preferences("key", value) VALUES(?,?) ON CONFLICT("key") DO UPDATE SET value=excluded.value`, dbKey, string(raw))
		if execErr != nil {
			return execErr
		}
		result = map[string]any{
			"acquired": true,
			"lock":     lockRecord,
		}
		return nil
	})

	if err != nil {
		return nil, err
	}

	if result["acquired"] == true && s.publisher != nil {
		_ = s.publisher.Publish(ctx, "swarm.lock.event", map[string]any{
			"action": "acquired",
			"lock":   result["lock"],
		})
	}

	return result, nil
}

// LockRelease releases an exclusive lock.
func (s *Service) LockRelease(ctx context.Context, payload map[string]any) (map[string]any, error) {
	rawRes, _ := payload["resource"].(string)
	resource := strings.TrimSpace(rawRes)
	if resource == "" {
		return nil, errors.New("resource identifier is required")
	}

	owner := ""
	if o, ok := payload["owner_key"].(string); ok {
		owner = strings.TrimSpace(o)
	}

	dbKey := "lock:" + resource
	var result map[string]any
	var released bool

	err := s.store.WithTx(ctx, func(tx *sql.Tx) error {
		row := tx.QueryRowContext(ctx, `SELECT value FROM workspace_preferences WHERE "key"=?`, dbKey)
		var storedVal string
		err := row.Scan(&storedVal)
		if errors.Is(err, sql.ErrNoRows) {
			result = map[string]any{"released": true, "resource": resource}
			released = true
			return nil
		} else if err != nil {
			return err
		}

		var m map[string]any
		if jsonErr := json.Unmarshal([]byte(storedVal), &m); jsonErr == nil {
			lockOwner, _ := m["owner_key"].(string)
			if owner != "" && lockOwner != owner {
				result = map[string]any{
					"released": false,
					"message":  fmt.Sprintf("Cannot release lock owned by %s", lockOwner),
				}
				return nil
			}
		}

		_, delErr := tx.ExecContext(ctx, `DELETE FROM workspace_preferences WHERE "key"=?`, dbKey)
		if delErr != nil {
			return delErr
		}
		result = map[string]any{"released": true, "resource": resource}
		released = true
		return nil
	})

	if err != nil {
		return nil, err
	}

	if released && s.publisher != nil {
		_ = s.publisher.Publish(ctx, "swarm.lock.event", map[string]any{
			"action":   "released",
			"resource": resource,
		})
	}

	return result, nil
}

// LockList lists active locks and deletes expired ones inside a transaction.
func (s *Service) LockList(ctx context.Context) (map[string]any, error) {
	now := time.Now().UTC()
	var active []any

	err := s.store.WithTx(ctx, func(tx *sql.Tx) error {
		rows, qErr := tx.QueryContext(ctx, `SELECT "key", value FROM workspace_preferences WHERE "key" LIKE 'lock:%'`)
		if qErr != nil {
			return qErr
		}
		defer rows.Close()

		var expiredKeys []string
		for rows.Next() {
			var k, val string
			if sErr := rows.Scan(&k, &val); sErr != nil {
				continue
			}
			var m map[string]any
			if jErr := json.Unmarshal([]byte(val), &m); jErr == nil {
				if expStr, ok := m["expires_at"].(string); ok {
					expTime, tErr := time.Parse(time.RFC3339Nano, expStr)
					if tErr != nil {
						expTime, tErr = time.Parse(time.RFC3339, expStr)
					}
					if tErr == nil && expTime.After(now) {
						active = append(active, m)
						continue
					}
				}
			}
			expiredKeys = append(expiredKeys, k)
		}

		for _, k := range expiredKeys {
			_, _ = tx.ExecContext(ctx, `DELETE FROM workspace_preferences WHERE "key"=?`, k)
		}
		return nil
	})

	if err != nil {
		return nil, err
	}

	if active == nil {
		active = []any{}
	}
	return map[string]any{"items": active, "count": len(active)}, nil
}

func coerceInt(val any, defaultVal int) int {
	if val == nil {
		return defaultVal
	}
	switch v := val.(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		return int(v)
	case string:
		if i, err := strconv.Atoi(v); err == nil {
			return i
		}
	}
	return defaultVal
}

func nullableString(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func nullableTrimmedString(s string) any {
	trimmed := strings.TrimSpace(s)
	if trimmed == "" {
		return nil
	}
	return trimmed
}

func randomID(length int) string {
	b := make([]byte, length)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
