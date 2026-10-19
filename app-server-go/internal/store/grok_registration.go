package store

import (
	"context"
	"database/sql"
	"errors"
)

// NormalizeGrokRegistrationTx repairs metadata from older daemons only when
// the exact canonical remote identity belongs to a saved SSH connection.
func NormalizeGrokRegistrationTx(ctx context.Context, tx *sql.Tx, payload map[string]any) error {
	if payload["kind"] != "grok" {
		return nil
	}
	if connection, _ := payload["connection_id"].(string); connection != "" {
		return nil
	}
	id, _ := payload["id"].(string)
	var connection, name string
	err := tx.QueryRowContext(ctx, `SELECT id, COALESCE(display_name,'') FROM ssh_connections WHERE 'ssh-grok-' || id = ?`, id).Scan(&connection, &name)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	payload["connection_id"] = connection
	if payload["name"] == "Grok Build" || payload["name"] == "" {
		if name == "" {
			name = connection
		}
		payload["name"] = name + " · Grok"
	}
	return nil
}

// RepairGrokRegistrations corrects already projected legacy hellos before the
// App exposes its initial catalog. No Agent or native session is replaced.
func (s *Store) RepairGrokRegistrations(ctx context.Context) error {
	return s.WithTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE agents SET connection_id=(SELECT c.id FROM ssh_connections c WHERE agents.id='ssh-grok-' || c.id), name=CASE WHEN name='Grok Build' OR name='' OR name IS NULL THEN (SELECT COALESCE(NULLIF(c.display_name,''),c.id) || ' · Grok' FROM ssh_connections c WHERE agents.id='ssh-grok-' || c.id) ELSE name END WHERE kind='grok' AND COALESCE(connection_id,'')='' AND EXISTS(SELECT 1 FROM ssh_connections c WHERE agents.id='ssh-grok-' || c.id)`)
		return err
	})
}
