package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// PendingApprovals returns durable native approvals independently of event retention.
func (s *Store) PendingApprovals(ctx context.Context) ([]map[string]any, error) {
	rows, err := s.QueryRows(ctx, `SELECT value FROM workspace_preferences WHERE instr("key", 'approval:') = 1 ORDER BY "key"`)
	if err != nil {
		return nil, err
	}
	result := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		value, ok := row["value"].(map[string]any)
		if !ok {
			return nil, errors.New("persisted approval must be an object")
		}
		if value["state"] == "pending" {
			result = append(result, value)
		}
	}
	return result, nil
}

func persistApprovalTx(ctx context.Context, tx *sql.Tx, event Event) error {
	if event.Type != "approval.upsert" {
		return nil
	}
	value := event.Data
	id, _ := value["id"].(string)
	aid, _ := value["agent_id"].(string)
	sid, _ := value["session_id"].(string)
	if aid == "" {
		aid = event.AgentID
	}
	if sid == "" {
		sid = event.SessionID
	}
	if id == "" || aid == "" || sid == "" {
		return errors.New("approval requires exact agent, session and approval identity")
	}
	if (event.AgentID != "" && event.AgentID != aid) || (event.SessionID != "" && event.SessionID != sid) {
		return errors.New("approval identity conflicts with event scope")
	}
	scope, err := encodeJSON([]string{aid, sid, id})
	if err != nil {
		return err
	}
	key := "approval:" + scope
	merged := map[string]any{}
	var previous string
	err = tx.QueryRowContext(ctx, `SELECT value FROM workspace_preferences WHERE "key"=?`, key).Scan(&previous)
	if err == nil {
		decoded, decodeErr := decodeJSON(previous)
		if decodeErr != nil {
			return decodeErr
		}
		object, ok := decoded.(map[string]any)
		if !ok {
			return errors.New("persisted approval must be an object")
		}
		for k, v := range object {
			merged[k] = v
		}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	for k, v := range value {
		merged[k] = v
	}
	merged["id"] = id
	merged["agent_id"] = aid
	merged["session_id"] = sid
	encoded, err := encodeJSON(merged)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO workspace_preferences("key",value) VALUES(?,?) ON CONFLICT("key") DO UPDATE SET value=excluded.value`, key, encoded)
	if err != nil {
		return fmt.Errorf("persist native approval: %w", err)
	}
	return nil
}
