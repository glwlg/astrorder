package store

import (
	"context"
	"errors"
	"fmt"
)

// GetSSHConnection returns an exact connection, or the first ID when empty.
func (s *Store) GetSSHConnection(ctx context.Context, connectionID string) (map[string]any, error) {
	query := "SELECT * FROM ssh_connections"
	args := []any{}
	if connectionID == "" {
		query += " ORDER BY id LIMIT 1"
	} else {
		query += " WHERE id=?"
		args = append(args, connectionID)
	}
	row, err := s.QueryRow(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	return sshWire(row)
}

// ListSSHConnections returns all saved connections ordered by ID.
func (s *Store) ListSSHConnections(ctx context.Context) ([]map[string]any, error) {
	rows, err := s.QueryRows(ctx, "SELECT * FROM ssh_connections ORDER BY id")
	if err != nil {
		return nil, err
	}
	return wireRows(rows, sshWire)
}

// SaveSSHConnection inserts or updates one connection. Empty ID allocates a stable random ID.
func (s *Store) SaveSSHConnection(ctx context.Context, payload map[string]any) (map[string]any, error) {
	id, _ := stringValue(payload, "id", false)
	if id == "" {
		var err error
		id, err = randomID("ssh-", 24)
		if err != nil {
			return nil, err
		}
	}
	port, err := integer(payload["port"], 22)
	if err != nil || port < 1 || port > 65535 {
		return nil, errors.New("port must be between 1 and 65535")
	}
	state := payloadStringDefault(payload, "state", "disconnected")
	detail, _ := stringValue(payload, "detail", false)
	profile := payloadStringDefault(payload, "profile_name", "default")
	_, err = s.db.ExecContext(ctx, `INSERT INTO ssh_connections
(id,display_name,profile_name,host,port,user,ssh_config_alias,identity_file,hermes_path,workspace,state,detail,remote_os,agent_id,runtime_id,updated_at)
VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET display_name=excluded.display_name,
profile_name=excluded.profile_name,host=excluded.host,port=excluded.port,user=excluded.user,ssh_config_alias=excluded.ssh_config_alias,
identity_file=excluded.identity_file,hermes_path=excluded.hermes_path,workspace=excluded.workspace,state=excluded.state,
detail=excluded.detail,remote_os=excluded.remote_os,agent_id=excluded.agent_id,runtime_id=excluded.runtime_id,updated_at=excluded.updated_at`,
		id, nullablePayload(payload, "display_name"), profile, nullablePayload(payload, "host"), port,
		nullablePayload(payload, "user"), nullablePayload(payload, "ssh_config_alias"), nullablePayload(payload, "identity_file"),
		nullablePayload(payload, "hermes_path"), nullablePayload(payload, "workspace"), state, detail,
		nullablePayload(payload, "remote_os"), nullablePayload(payload, "agent_id"), nullablePayload(payload, "runtime_id"), nowDatabaseTime())
	if err != nil {
		return nil, err
	}
	return s.GetSSHConnection(ctx, id)
}

// UpdateSSHConnectionState updates status and optional runtime ownership fields.
func (s *Store) UpdateSSHConnectionState(ctx context.Context, connectionID, state, detail string, payload map[string]any) (map[string]any, error) {
	if connectionID == "" {
		first, err := s.GetSSHConnection(ctx, "")
		if err != nil || first == nil {
			return nil, err
		}
		connectionID = first["id"].(string)
	}
	sets := "state=?,detail=?,updated_at=?"
	args := []any{state, detail, nowDatabaseTime()}
	for _, key := range []string{"remote_os", "agent_id", "runtime_id"} {
		if value, ok := payload[key]; ok {
			sets += "," + key + "=?"
			args = append(args, value)
		}
	}
	args = append(args, connectionID)
	result, err := s.db.ExecContext(ctx, "UPDATE ssh_connections SET "+sets+" WHERE id=?", args...)
	if err != nil {
		return nil, err
	}
	if affected, _ := result.RowsAffected(); affected == 0 {
		return nil, nil
	}
	return s.GetSSHConnection(ctx, connectionID)
}

// DeleteSSHConnection deletes one exact connection.
func (s *Store) DeleteSSHConnection(ctx context.Context, connectionID string) (bool, error) {
	result, err := s.db.ExecContext(ctx, "DELETE FROM ssh_connections WHERE id=?", connectionID)
	if err != nil {
		return false, err
	}
	affected, _ := result.RowsAffected()
	return affected > 0, nil
}

// MarkSSHConnectionsDisconnected marks App-owned transient connection states after restart.
func (s *Store) MarkSSHConnectionsDisconnected(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `UPDATE ssh_connections SET state='disconnected',
detail='Astrorder restarted; reconnect explicitly',agent_id=NULL,runtime_id=NULL,updated_at=?
WHERE state IN ('connecting','connected','ready')`, nowDatabaseTime())
	return err
}

// AppendConnectionHistory stores redacted connection diagnostics.
func (s *Store) AppendConnectionHistory(ctx context.Context, connectionID, stage, state, detail string, details map[string]any) (map[string]any, error) {
	id, err := randomID("history-", 32)
	if err != nil {
		return nil, err
	}
	detailsJSON, err := encodeJSON(redactStructured(details, 0))
	if err != nil {
		return nil, err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO connection_history(id,connection_id,stage,state,detail,details,created_at)
VALUES(?,?,?,?,?,?,?)`, id, connectionID, truncate(stage, 64), truncate(state, 32), redactText(detail), detailsJSON, nowDatabaseTime())
	if err != nil {
		return nil, err
	}
	row, err := s.QueryRow(ctx, "SELECT * FROM connection_history WHERE id=?", id)
	if err != nil {
		return nil, err
	}
	return historyWire(row)
}

// ListConnectionHistory returns an ascending page and cursor for older rows.
func (s *Store) ListConnectionHistory(ctx context.Context, connectionID, before string, limit int) ([]map[string]any, string, error) {
	if limit < 1 {
		return nil, "", errors.New("limit must be positive")
	}
	query := "SELECT * FROM connection_history WHERE connection_id=?"
	args := []any{connectionID}
	if before != "" {
		rowID, err := decodeCursor(before)
		if err != nil {
			return nil, "", err
		}
		query += " AND row_id<?"
		args = append(args, rowID)
	}
	query += " ORDER BY row_id DESC LIMIT ?"
	args = append(args, limit+1)
	rows, err := s.QueryRows(ctx, query, args...)
	if err != nil {
		return nil, "", err
	}
	hasMore := len(rows) > limit
	if hasMore {
		rows = rows[:limit]
	}
	for left, right := 0, len(rows)-1; left < right; left, right = left+1, right-1 {
		rows[left], rows[right] = rows[right], rows[left]
	}
	next := ""
	if hasMore && len(rows) > 0 {
		rowID, _ := integer(rows[0]["row_id"], 0)
		next = encodeCursor(rowID)
	}
	converted, err := wireRows(rows, historyWire)
	return converted, next, err
}

func truncate(value string, max int) string {
	return truncateCodepoints(value, max)
}

// GetReviewBinding returns an exact source composite binding.
func (s *Store) GetReviewBinding(ctx context.Context, sourceAgentID, sourceSessionID string) (map[string]any, error) {
	row, err := s.QueryRow(ctx, "SELECT * FROM review_relay_bindings WHERE source_agent_id=? AND source_session_id=?", sourceAgentID, sourceSessionID)
	if err != nil {
		return nil, err
	}
	return reviewBindingWire(row)
}

// UpsertReviewBinding inserts or replaces one source composite binding.
func (s *Store) UpsertReviewBinding(ctx context.Context, payload map[string]any) (map[string]any, error) {
	sourceAgentID, err := stringValue(payload, "source_agent_id", true)
	if err != nil {
		return nil, err
	}
	sourceSessionID, err := stringValue(payload, "source_session_id", true)
	if err != nil {
		return nil, err
	}
	enabled, err := boolValue(payload["enabled"], true)
	if err != nil {
		return nil, err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO review_relay_bindings
(source_agent_id,source_session_id,review_agent_id,review_session_id,workspace,enabled,updated_at)
VALUES(?,?,?,?,?,?,?) ON CONFLICT(source_agent_id,source_session_id) DO UPDATE SET review_agent_id=excluded.review_agent_id,
review_session_id=excluded.review_session_id,workspace=excluded.workspace,enabled=excluded.enabled,updated_at=excluded.updated_at`,
		sourceAgentID, sourceSessionID, payload["review_agent_id"], payload["review_session_id"], payload["workspace"], enabled, nowDatabaseTime())
	if err != nil {
		return nil, err
	}
	return s.GetReviewBinding(ctx, sourceAgentID, sourceSessionID)
}

// DeleteReviewBinding removes one source composite binding.
func (s *Store) DeleteReviewBinding(ctx context.Context, sourceAgentID, sourceSessionID string) (bool, error) {
	result, err := s.db.ExecContext(ctx, "DELETE FROM review_relay_bindings WHERE source_agent_id=? AND source_session_id=?", sourceAgentID, sourceSessionID)
	if err != nil {
		return false, err
	}
	affected, _ := result.RowsAffected()
	return affected > 0, nil
}

// CreateReviewRun inserts one review relay execution row.
func (s *Store) CreateReviewRun(ctx context.Context, payload map[string]any) (map[string]any, error) {
	id, _ := stringValue(payload, "id", false)
	if id == "" {
		var err error
		id, err = randomID("review-", 32)
		if err != nil {
			return nil, err
		}
	}
	baselineJSON, err := encodeJSON(payload["baseline_ids"])
	if err != nil {
		return nil, err
	}
	now := nowDatabaseTime()
	_, err = s.db.ExecContext(ctx, `INSERT INTO review_relay_runs
(id,source_agent_id,source_session_id,review_agent_id,review_session_id,command_id,baseline_ids,status,comment_text,error,created_at,updated_at)
VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`, id, payload["source_agent_id"], payload["source_session_id"], payload["review_agent_id"],
		payload["review_session_id"], payload["command_id"], baselineJSON, payload["status"], nullablePayload(payload, "comment_text"),
		nullablePayload(payload, "error"), now, now)
	if err != nil {
		return nil, err
	}
	return s.GetReviewRun(ctx, id)
}

// GetReviewRun returns one run by ID.
func (s *Store) GetReviewRun(ctx context.Context, id string) (map[string]any, error) {
	row, err := s.QueryRow(ctx, "SELECT * FROM review_relay_runs WHERE id=?", id)
	if err != nil {
		return nil, err
	}
	return reviewRunWire(row)
}

// ListReviewRuns lists source-scoped runs newest-first; activeOnly filters terminal states.
func (s *Store) ListReviewRuns(ctx context.Context, sourceAgentID, sourceSessionID string, activeOnly bool) ([]map[string]any, error) {
	query := "SELECT * FROM review_relay_runs WHERE source_agent_id=? AND source_session_id=?"
	if activeOnly {
		query += " AND status IN ('validating','reviewing','forwarding')"
	}
	query += " ORDER BY created_at DESC"
	rows, err := s.QueryRows(ctx, query, sourceAgentID, sourceSessionID)
	if err != nil {
		return nil, err
	}
	return wireRows(rows, reviewRunWire)
}

// UpdateReviewRun updates status/comment/error fields and returns nil when absent.
func (s *Store) UpdateReviewRun(ctx context.Context, id string, payload map[string]any) (map[string]any, error) {
	sets := "updated_at=?"
	args := []any{nowDatabaseTime()}
	for _, key := range []string{"status", "comment_text", "error"} {
		if value, ok := payload[key]; ok {
			sets += "," + key + "=?"
			args = append(args, value)
		}
	}
	args = append(args, id)
	result, err := s.db.ExecContext(ctx, "UPDATE review_relay_runs SET "+sets+" WHERE id=?", args...)
	if err != nil {
		return nil, err
	}
	if affected, _ := result.RowsAffected(); affected == 0 {
		return nil, nil
	}
	return s.GetReviewRun(ctx, id)
}

// UpsertTokenMetric writes one exact daily metric composite identity.
func (s *Store) UpsertTokenMetric(ctx context.Context, payload map[string]any) (map[string]any, error) {
	fields := []string{"input_tokens", "output_tokens", "cached_tokens", "reasoning_tokens", "total_tokens", "context_window", "last_input_tokens"}
	values := make([]any, 0, len(fields))
	for _, field := range fields {
		value, err := integer(payload[field], 0)
		if err != nil || value < 0 {
			return nil, fmt.Errorf("%s must be a non-negative integer", field)
		}
		values = append(values, value)
	}
	args := []any{payload["agent_id"], payload["session_id"], payload["model"], nullablePayload(payload, "provider"), payload["date"]}
	args = append(args, values...)
	args = append(args, nowDatabaseTime())
	_, err := s.db.ExecContext(ctx, `INSERT INTO token_metrics
(agent_id,session_id,model,provider,date,input_tokens,output_tokens,cached_tokens,reasoning_tokens,total_tokens,context_window,last_input_tokens,updated_at)
VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(agent_id,session_id,model,date) DO UPDATE SET provider=excluded.provider,
input_tokens=excluded.input_tokens,output_tokens=excluded.output_tokens,cached_tokens=excluded.cached_tokens,
reasoning_tokens=excluded.reasoning_tokens,total_tokens=excluded.total_tokens,context_window=excluded.context_window,
last_input_tokens=excluded.last_input_tokens,updated_at=excluded.updated_at`, args...)
	if err != nil {
		return nil, err
	}
	row, err := s.QueryRow(ctx, "SELECT * FROM token_metrics WHERE agent_id=? AND session_id=? AND model=? AND date=?", payload["agent_id"], payload["session_id"], payload["model"], payload["date"])
	if err != nil {
		return nil, err
	}
	return metricWire(row)
}

// ListTokenMetrics returns metrics newest-first, optionally scoped to an agent.
func (s *Store) ListTokenMetrics(ctx context.Context, agentID, sessionID string) ([]map[string]any, error) {
	query := "SELECT * FROM token_metrics WHERE session_id=?"
	args := []any{sessionID}
	if agentID != "" {
		query += " AND agent_id=?"
		args = append(args, agentID)
	}
	query += " ORDER BY updated_at DESC"
	rows, err := s.QueryRows(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	return wireRows(rows, metricWire)
}
