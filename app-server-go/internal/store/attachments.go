package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// GetAttachment returns full attachment metadata.
func (s *Store) GetAttachment(ctx context.Context, id string) (map[string]any, error) {
	row, err := s.QueryRow(ctx, "SELECT * FROM attachments WHERE id=?", id)
	if err != nil {
		return nil, err
	}
	return attachmentWire(row, false)
}

// AddAttachment inserts immutable attachment metadata.
func (s *Store) AddAttachment(ctx context.Context, payload map[string]any) (map[string]any, error) {
	id, err := stringValue(payload, "id", true)
	if err != nil {
		return nil, err
	}
	name, err := stringValue(payload, "name", true)
	if err != nil {
		return nil, err
	}
	mediaType, err := stringValue(payload, "media_type", true)
	if err != nil {
		return nil, err
	}
	storageName, err := stringValue(payload, "storage_name", true)
	if err != nil {
		return nil, err
	}
	size, err := integer(payload["size"], 0)
	if err != nil || size < 0 {
		return nil, errors.New("size must be a non-negative integer")
	}
	createdAt := nowDatabaseTime()
	if payload["created_at"] != nil {
		createdAt, err = databaseTime(payload["created_at"])
		if err != nil {
			return nil, err
		}
	}
	var result map[string]any
	err = s.WithTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO attachments(id,name,media_type,size,storage_name,source_path,created_at)
VALUES(?,?,?,?,?,?,?)`, id, name, mediaType, size, storageName, nullablePayload(payload, "source_path"), createdAt)
		if err != nil {
			return err
		}
		rows, err := queryRows(ctx, tx, "SELECT * FROM attachments WHERE id=?", id)
		if err != nil {
			return err
		}
		if len(rows) != 1 {
			return errors.New("attachment insert readback is missing")
		}
		result, err = attachmentWire(rows[0], false)
		return err
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

// AttachmentRows resolves IDs in caller order and errors if any are missing.
func (s *Store) AttachmentRows(ctx context.Context, ids []string) ([]map[string]any, error) {
	if len(ids) == 0 {
		return []map[string]any{}, nil
	}
	args := make([]any, len(ids))
	for i := range ids {
		args[i] = ids[i]
	}
	rows, err := s.QueryRows(ctx, "SELECT * FROM attachments WHERE id IN ("+placeholders(len(ids))+")", args...)
	if err != nil {
		return nil, err
	}
	byID := make(map[string]map[string]any, len(rows))
	for _, row := range rows {
		byID[row["id"].(string)] = row
	}
	result := make([]map[string]any, 0, len(ids))
	for _, id := range ids {
		row := byID[id]
		if row == nil {
			return nil, fmt.Errorf("attachment %s: %w", id, errNotFound)
		}
		summary, err := attachmentWire(row, true)
		if err != nil {
			return nil, err
		}
		result = append(result, summary)
	}
	return result, nil
}
