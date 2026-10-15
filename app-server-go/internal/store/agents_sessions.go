package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// GetAgent returns one agent by its exact ID, or nil when absent.
func (s *Store) GetAgent(ctx context.Context, agentID string) (map[string]any, error) {
	row, err := s.QueryRow(ctx, "SELECT * FROM agents WHERE id=?", agentID)
	if err != nil {
		return nil, err
	}
	return agentWire(row)
}

// ListAgents returns all agents ordered by exact ID.
func (s *Store) ListAgents(ctx context.Context) ([]map[string]any, error) {
	rows, err := s.QueryRows(ctx, "SELECT * FROM agents ORDER BY id")
	if err != nil {
		return nil, err
	}
	return wireRows(rows, agentWire)
}

// UpsertAgent inserts or replaces mutable agent projection fields from payload.
func (s *Store) UpsertAgent(ctx context.Context, payload map[string]any) (map[string]any, error) {
	id, err := stringValue(payload, "id", true)
	if err != nil {
		return nil, err
	}
	kind, err := stringValue(payload, "kind", true)
	if err != nil {
		return nil, err
	}
	name, err := stringValue(payload, "name", true)
	if err != nil {
		return nil, err
	}
	status, err := stringValue(payload, "status", true)
	if err != nil {
		return nil, err
	}
	capabilities := payload["capabilities"]
	if capabilities == nil {
		capabilities = []any{}
	}
	capabilitiesJSON, err := encodeJSON(capabilities)
	if err != nil {
		return nil, fmt.Errorf("capabilities: %w", err)
	}
	sourceID := optionalString(payload, "source_id")
	if sourceID == nil || *sourceID == "" {
		sourceID = &id
	}
	runtimeID := optionalString(payload, "runtime_id")
	if runtimeID == nil || *runtimeID == "" {
		runtimeID = &id
	}
	controlState := "unknown"
	if value := optionalString(payload, "control_state"); value != nil && *value != "" {
		controlState = *value
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO agents
(id,kind,name,status,capabilities,limitation,source_id,connection_id,profile_name,runtime_id,control_state,updated_at)
VALUES(?,?,?,?,?,?,?,?,?,?,?,?)
ON CONFLICT(id) DO UPDATE SET kind=excluded.kind,name=excluded.name,status=excluded.status,
capabilities=excluded.capabilities,limitation=excluded.limitation,source_id=excluded.source_id,
connection_id=excluded.connection_id,profile_name=excluded.profile_name,runtime_id=excluded.runtime_id,
control_state=excluded.control_state,updated_at=excluded.updated_at`,
		id, kind, name, status, capabilitiesJSON, nullablePayload(payload, "limitation"), *sourceID,
		nullablePayload(payload, "connection_id"), nullablePayload(payload, "profile_name"), *runtimeID,
		controlState, nowDatabaseTime())
	if err != nil {
		return nil, err
	}
	return s.GetAgent(ctx, id)
}

// SetAgentStatus updates one exact agent status and returns nil when absent.
func (s *Store) SetAgentStatus(ctx context.Context, agentID, status string) (map[string]any, error) {
	result, err := s.db.ExecContext(ctx, "UPDATE agents SET status=?,updated_at=? WHERE id=?", status, nowDatabaseTime(), agentID)
	if err != nil {
		return nil, err
	}
	if affected, _ := result.RowsAffected(); affected == 0 {
		return nil, nil
	}
	return s.GetAgent(ctx, agentID)
}

// SetAgentConnectionChoice records an explicit enabled/disabled environment choice.
func (s *Store) SetAgentConnectionChoice(ctx context.Context, id string, enabled bool) error {
	if id == "" {
		return errors.New("choice ID is required")
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO agent_connection_choices(id,enabled) VALUES(?,?)
ON CONFLICT(id) DO UPDATE SET enabled=excluded.enabled`, id, enabled)
	return err
}

// GetAgentConnectionChoice returns value, presence, and any query error.
func (s *Store) GetAgentConnectionChoice(ctx context.Context, id string) (bool, bool, error) {
	row, err := s.QueryRow(ctx, "SELECT enabled FROM agent_connection_choices WHERE id=?", id)
	if err != nil || row == nil {
		return false, false, err
	}
	enabled, err := boolValue(row["enabled"], false)
	return enabled, true, err
}

// GetSession resolves only the exact (agentID, sessionID) composite identity.
func (s *Store) GetSession(ctx context.Context, agentID, sessionID string) (map[string]any, error) {
	row, err := s.QueryRow(ctx, "SELECT * FROM sessions WHERE agent_id=? AND id=?", agentID, sessionID)
	if err != nil {
		return nil, err
	}
	return sessionWire(row)
}

// FindSessionByID provides the legacy ambiguous lookup, choosing newest row_id.
// Callers that know an agent must use GetSession instead.
func (s *Store) FindSessionByID(ctx context.Context, sessionID string) (map[string]any, error) {
	row, err := s.QueryRow(ctx, "SELECT * FROM sessions WHERE id=? ORDER BY row_id DESC LIMIT 1", sessionID)
	if err != nil {
		return nil, err
	}
	return sessionWire(row)
}

// ListSessions returns sessions newest-first; an empty agentID lists all agents.
func (s *Store) ListSessions(ctx context.Context, agentID string) ([]map[string]any, error) {
	query := "SELECT * FROM sessions"
	args := []any{}
	if agentID != "" {
		query += " WHERE agent_id=?"
		args = append(args, agentID)
	}
	query += " ORDER BY updated_at DESC,row_id DESC"
	rows, err := s.QueryRows(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	return wireRows(rows, sessionWire)
}

// UpsertSession inserts or updates an exact composite session projection.
func (s *Store) UpsertSession(ctx context.Context, payload map[string]any) (map[string]any, error) {
	id, err := stringValue(payload, "id", true)
	if err != nil {
		return nil, err
	}
	agentID, err := stringValue(payload, "agent_id", true)
	if err != nil {
		return nil, err
	}
	status, err := stringValue(payload, "status", true)
	if err != nil {
		return nil, err
	}
	title, _ := stringValue(payload, "title", false)
	title = truncateCodepoints(title, 512)
	updatedAt, err := databaseTime(payload["updated_at"])
	if err != nil {
		return nil, fmt.Errorf("updated_at: %w", err)
	}
	ephemeral, err := boolValue(payload["ephemeral"], false)
	if err != nil {
		return nil, err
	}
	sourceID := payloadStringDefault(payload, "source_id", agentID)
	sourceSessionID := payloadStringDefault(payload, "source_session_id", id)
	historyState := payloadStringDefault(payload, "history_state", "local")
	controlState := payloadStringDefault(payload, "control_state", "unknown")
	var result map[string]any
	err = s.WithTx(ctx, func(tx *sql.Tx) error {
		existingRows, queryErr := queryRows(ctx, tx, "SELECT * FROM sessions WHERE agent_id=? AND id=?", agentID, id)
		if queryErr != nil {
			return queryErr
		}
		if len(existingRows) == 0 {
			runtimeOwner, marshalErr := nullableJSON(payload, "runtime_owner")
			if marshalErr != nil {
				return marshalErr
			}
			_, execErr := tx.ExecContext(ctx, `INSERT INTO sessions
(id,agent_id,title,workspace,status,source_id,connection_id,source_session_id,project_id,project_name,
history_state,native_kind,ephemeral,control_state,runtime_owner,selected_model_provider,selected_model,
selected_reasoning_effort,selected_approval_mode,handoff_from_agent_id,handoff_from_session_id,handoff_context,
handoff_context_consumed,updated_at)
VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
				id, agentID, title, nullablePayload(payload, "workspace"), status, sourceID,
				nullablePayload(payload, "connection_id"), sourceSessionID, nullablePayload(payload, "project_id"),
				nullablePayload(payload, "project_name"), historyState, nullablePayload(payload, "native_kind"),
				ephemeral, controlState, runtimeOwner, nullablePayload(payload, "selected_model_provider"),
				nullablePayload(payload, "selected_model"), nullablePayload(payload, "selected_reasoning_effort"),
				nullablePayload(payload, "selected_approval_mode"), nullablePayload(payload, "handoff_from_agent_id"),
				nullablePayload(payload, "handoff_from_session_id"), nullablePayload(payload, "handoff_context"),
				false, updatedAt)
			if execErr != nil {
				return execErr
			}
		} else {
			existing := existingRows[0]
			if isPlaceholderTitle(title, id) && !isPlaceholderTitle(fmt.Sprint(existing["title"]), id) {
				title = fmt.Sprint(existing["title"])
			}
			workspace := preserveNilPayload(payload, "workspace", existing["workspace"])
			projectID := preserveNilPayload(payload, "project_id", existing["project_id"])
			projectName := preserveNilPayload(payload, "project_name", existing["project_name"])
			handoffAgent := preserveEmptyPayload(payload, "handoff_from_agent_id", existing["handoff_from_agent_id"])
			handoffSession := preserveEmptyPayload(payload, "handoff_from_session_id", existing["handoff_from_session_id"])
			existingEphemeral, _ := boolValue(existing["ephemeral"], false)
			nativeKind := existing["native_kind"]
			if _, present := payload["native_kind"]; present {
				nativeKind = payload["native_kind"]
			}
			_, execErr := tx.ExecContext(ctx, `UPDATE sessions SET title=?,workspace=?,status=?,source_id=?,connection_id=?,source_session_id=?,
project_id=?,project_name=?,history_state=?,native_kind=?,ephemeral=?,control_state=?,handoff_from_agent_id=?,handoff_from_session_id=?,updated_at=?
WHERE agent_id=? AND id=?`, title, workspace, status, sourceID, nullablePayload(payload, "connection_id"), sourceSessionID,
				projectID, projectName, historyState, nativeKind, existingEphemeral || ephemeral, controlState,
				handoffAgent, handoffSession, updatedAt, agentID, id)
			if execErr != nil {
				return execErr
			}
		}
		rows, queryErr := queryRows(ctx, tx, "SELECT * FROM sessions WHERE agent_id=? AND id=?", agentID, id)
		if queryErr != nil {
			return queryErr
		}
		result, queryErr = sessionWire(rows[0])
		return queryErr
	})
	return result, err
}

// UpdateSession applies supported partial updates to an exact session.
func (s *Store) UpdateSession(ctx context.Context, agentID, sessionID string, updates map[string]any) (map[string]any, error) {
	sets := make([]string, 0, 5)
	args := make([]any, 0, 7)
	for _, key := range []string{"title", "status", "workspace"} {
		if value, ok := updates[key]; ok {
			if key == "title" {
				text, ok := value.(string)
				if !ok {
					return nil, errors.New("title must be a string")
				}
				value = truncateCodepoints(strings.TrimSpace(text), 512)
			}
			sets = append(sets, key+"=?")
			args = append(args, value)
		}
	}
	if value, ok := updates["runtime_owner"]; ok {
		encoded, err := encodeJSON(value)
		if err != nil {
			return nil, err
		}
		sets = append(sets, "runtime_owner=?")
		args = append(args, encoded)
	}
	if len(sets) == 0 {
		return s.GetSession(ctx, agentID, sessionID)
	}
	sets = append(sets, "updated_at=?")
	args = append(args, nowDatabaseTime(), agentID, sessionID)
	var result sql.Result
	err := s.WithTx(ctx, func(tx *sql.Tx) error {
		if _, renamed := updates["title"]; renamed {
			if _, err := tx.ExecContext(ctx, `DELETE FROM workspace_preferences WHERE "key"=?`, SessionTitleKey(agentID, sessionID)); err != nil {
				return err
			}
		}
		var err error
		result, err = tx.ExecContext(ctx, "UPDATE sessions SET "+strings.Join(sets, ",")+" WHERE agent_id=? AND id=?", args...)
		return err
	})
	if err != nil {
		return nil, err
	}
	if affected, _ := result.RowsAffected(); affected == 0 {
		return nil, nil
	}
	return s.GetSession(ctx, agentID, sessionID)
}

// DeleteSession removes a composite session and its application-owned children.
func (s *Store) DeleteSession(ctx context.Context, agentID, sessionID string) (bool, error) {
	deleted := false
	err := s.WithTx(ctx, func(tx *sql.Tx) error {
		var err error
		deleted, err = DeleteSessionTx(ctx, tx, agentID, sessionID)
		return err
	})
	return deleted, err
}

// DeleteSessionTx shares exact session cleanup with checkpointed event projection.
func DeleteSessionTx(ctx context.Context, tx *sql.Tx, agentID, sessionID string) (bool, error) {
	for _, table := range []string{"messages", "commands", "tasks"} {
		if _, err := tx.ExecContext(ctx, "DELETE FROM "+table+" WHERE agent_id=? AND session_id=?", agentID, sessionID); err != nil {
			return false, err
		}
	}
	prefix := agentID + "::" + sessionID
	if _, err := tx.ExecContext(ctx, `DELETE FROM workspace_preferences
WHERE instr(key, ?)=1 OR instr(key, ?)=1 OR key=? OR key=? OR key=?
OR (instr(key, 'approval:')=1 AND json_extract(value, '$.agent_id')=? AND json_extract(value, '$.session_id')=?)`,
		"blackboard:session:"+prefix+":", "blackboard:swarm:"+prefix+":", "blackboard_scope:"+prefix,
		nativeTurnKey(agentID, sessionID), SessionTitleKey(agentID, sessionID), agentID, sessionID); err != nil {
		return false, err
	}
	result, err := tx.ExecContext(ctx, "DELETE FROM sessions WHERE agent_id=? AND id=?", agentID, sessionID)
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	if err != nil || affected == 0 {
		return false, err
	}
	// A session removed here may still exist in its native store. Remember that
	// it was removed on purpose so native discovery does not list it again.
	if _, err := tx.ExecContext(ctx, `INSERT INTO workspace_preferences("key",value) VALUES(?,?) ON CONFLICT("key") DO NOTHING`,
		nativeRemovedKey(agentID, sessionID), "true"); err != nil {
		return false, err
	}
	return true, nil
}

// SetSessionModelBinding stores the provider/model selection for an exact session.
func (s *Store) SetSessionModelBinding(ctx context.Context, agentID, sessionID, provider, model string) (map[string]any, error) {
	return s.updateBinding(ctx, agentID, sessionID, "selected_model_provider=?,selected_model=?", provider, model)
}

// GetSessionModelBinding returns nil unless both provider and model are present.
func (s *Store) GetSessionModelBinding(ctx context.Context, agentID, sessionID string) (map[string]any, error) {
	row, err := s.QueryRow(ctx, "SELECT selected_model_provider,selected_model,selected_reasoning_effort FROM sessions WHERE agent_id=? AND id=?", agentID, sessionID)
	if err != nil || row == nil || row["selected_model_provider"] == nil || row["selected_model"] == nil {
		return nil, err
	}
	result := map[string]any{"provider": row["selected_model_provider"], "model": row["selected_model"]}
	if row["selected_reasoning_effort"] != nil && row["selected_reasoning_effort"] != "" {
		result["effort"] = row["selected_reasoning_effort"]
	}
	return result, nil
}

// SetSessionReasoningBinding stores reasoning effort for an exact session.
func (s *Store) SetSessionReasoningBinding(ctx context.Context, agentID, sessionID, effort string) (map[string]any, error) {
	return s.updateBinding(ctx, agentID, sessionID, "selected_reasoning_effort=?", effort)
}

// SetSessionApprovalModeBinding stores approval mode for an exact session.
func (s *Store) SetSessionApprovalModeBinding(ctx context.Context, agentID, sessionID, mode string) (map[string]any, error) {
	return s.updateBinding(ctx, agentID, sessionID, "selected_approval_mode=?", mode)
}

func (s *Store) updateBinding(ctx context.Context, agentID, sessionID, set string, values ...any) (map[string]any, error) {
	args := append(values, agentID, sessionID)
	result, err := s.db.ExecContext(ctx, "UPDATE sessions SET "+set+" WHERE agent_id=? AND id=?", args...)
	if err != nil {
		return nil, err
	}
	if affected, _ := result.RowsAffected(); affected == 0 {
		return nil, errNotFound
	}
	return s.GetSession(ctx, agentID, sessionID)
}

// SetSessionHandoffContext makes context available for one atomic claim.
func (s *Store) SetSessionHandoffContext(ctx context.Context, agentID, sessionID, value string) error {
	result, err := s.db.ExecContext(ctx, "UPDATE sessions SET handoff_context=?,handoff_context_consumed=0 WHERE agent_id=? AND id=?", value, agentID, sessionID)
	if err != nil {
		return err
	}
	if affected, _ := result.RowsAffected(); affected == 0 {
		return errNotFound
	}
	return nil
}

// ClaimSessionHandoffContext consumes and returns pending context atomically.
func (s *Store) ClaimSessionHandoffContext(ctx context.Context, agentID, sessionID string) (string, error) {
	var value string
	err := s.db.QueryRowContext(ctx, `UPDATE sessions SET handoff_context_consumed=1
WHERE agent_id=? AND id=? AND handoff_context IS NOT NULL AND handoff_context_consumed=0
RETURNING handoff_context`, agentID, sessionID).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return value, err
}

// RestoreSessionHandoffContext makes a previously claimed matching value pending.
func (s *Store) RestoreSessionHandoffContext(ctx context.Context, agentID, sessionID, value string) error {
	_, err := s.db.ExecContext(ctx, "UPDATE sessions SET handoff_context_consumed=0 WHERE agent_id=? AND id=? AND handoff_context=?", agentID, sessionID, value)
	return err
}

func optionalString(payload map[string]any, key string) *string {
	value, ok := payload[key]
	if !ok || value == nil {
		return nil
	}
	text, ok := value.(string)
	if !ok {
		return nil
	}
	return &text
}

func payloadStringDefault(payload map[string]any, key, fallback string) string {
	if value := optionalString(payload, key); value != nil && *value != "" {
		return *value
	}
	return fallback
}

func nullablePayload(payload map[string]any, key string) any {
	value, ok := payload[key]
	if !ok || value == "" {
		return nil
	}
	return value
}

func nullableJSON(payload map[string]any, key string) (any, error) {
	value, ok := payload[key]
	if !ok || value == nil {
		return nil, nil
	}
	return encodeJSON(value)
}

func preserveNilPayload(payload map[string]any, key string, existing any) any {
	if value, ok := payload[key]; ok && value != nil {
		return value
	}
	return existing
}

func preserveEmptyPayload(payload map[string]any, key string, existing any) any {
	if value, ok := payload[key]; ok && value != nil && value != "" {
		return value
	}
	return existing
}

func isPlaceholderTitle(title, id string) bool {
	return IsPlaceholderSessionTitle(title, id)
}
