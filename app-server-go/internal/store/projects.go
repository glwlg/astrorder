package store

import (
	"context"
	"fmt"
)

// ListProjects returns project projections newest-first.
func (s *Store) ListProjects(ctx context.Context) ([]map[string]any, error) {
	rows, err := s.QueryRows(ctx, "SELECT * FROM projects ORDER BY updated_at DESC,id")
	if err != nil {
		return nil, err
	}
	return wireRows(rows, projectWire)
}

// GetProject returns a project by its exact synthetic primary key.
func (s *Store) GetProject(ctx context.Context, id string) (map[string]any, error) {
	row, err := s.QueryRow(ctx, "SELECT * FROM projects WHERE id=?", id)
	if err != nil {
		return nil, err
	}
	return projectWire(row)
}

// UpsertProject builds the exact project:<sourceID>\x00<projectID> identity.
func (s *Store) UpsertProject(ctx context.Context, payload map[string]any) (map[string]any, error) {
	sourceID, err := stringValue(payload, "source_id", true)
	if err != nil {
		return nil, err
	}
	projectID, err := stringValue(payload, "project_id", true)
	if err != nil {
		return nil, err
	}
	id := "project:" + sourceID + "\x00" + projectID
	updatedAt, err := databaseTime(payload["updated_at"])
	if err != nil {
		return nil, err
	}
	count, err := integer(payload["session_count"], 0)
	if err != nil {
		return nil, err
	}
	if count < 0 {
		count = 0
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO projects
(id,source_id,connection_id,agent_id,profile_name,project_id,project_name,workspace,session_count,updated_at)
VALUES(?,?,?,?,?,?,?,?,?,?)
ON CONFLICT(id) DO UPDATE SET source_id=excluded.source_id,connection_id=excluded.connection_id,
agent_id=excluded.agent_id,profile_name=excluded.profile_name,project_id=excluded.project_id,
project_name=excluded.project_name,workspace=excluded.workspace,session_count=excluded.session_count,
updated_at=excluded.updated_at`, id, sourceID, nullablePayload(payload, "connection_id"),
		nullablePayload(payload, "agent_id"), nullablePayload(payload, "profile_name"), projectID,
		nullablePayload(payload, "project_name"), nullablePayload(payload, "workspace"), count, updatedAt)
	if err != nil {
		return nil, err
	}
	return s.GetProject(ctx, id)
}

// UpsertProjects writes each valid payload and returns canonical rows.
func (s *Store) UpsertProjects(ctx context.Context, payloads []map[string]any) ([]map[string]any, error) {
	result := make([]map[string]any, 0, len(payloads))
	for _, payload := range payloads {
		row, err := s.UpsertProject(ctx, payload)
		if err != nil {
			return nil, err
		}
		result = append(result, row)
	}
	return result, nil
}

// DeleteProject deletes one exact project row; session deletion remains explicit.
func (s *Store) DeleteProject(ctx context.Context, id string) (bool, error) {
	result, err := s.db.ExecContext(ctx, "DELETE FROM projects WHERE id=?", id)
	if err != nil {
		return false, err
	}
	affected, _ := result.RowsAffected()
	return affected > 0, nil
}

// ProjectID returns the stable composite project primary key.
func ProjectID(sourceID, projectID string) (string, error) {
	if sourceID == "" || projectID == "" {
		return "", fmt.Errorf("sourceID and projectID are required")
	}
	return "project:" + sourceID + "\x00" + projectID, nil
}
