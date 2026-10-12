package store

import (
	"context"
	"fmt"
)

// PresenceActivityRows returns the last user-message timestamp per exact
// (agent_id, session_id) composite. It joins the projected sessions table so
// orphaned, deleted, or different-agent rows are excluded, and it never reads
// message text or bodies.
func (s *Store) PresenceActivityRows(ctx context.Context) ([]map[string]any, error) {
	rows, err := s.QueryRows(ctx, `SELECT m.agent_id AS agent_id, m.session_id AS session_id, MAX(m.created_at) AS last_user_at
FROM messages m JOIN sessions s ON s.agent_id = m.agent_id AND s.id = m.session_id
WHERE m.role = 'user'
GROUP BY m.agent_id, m.session_id
ORDER BY m.agent_id, m.session_id`)
	if err != nil {
		return nil, err
	}
	result := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		stamp, err := wireTime(row["last_user_at"])
		if err != nil {
			return nil, fmt.Errorf("last_user_at: %w", err)
		}
		result = append(result, map[string]any{
			"agent_id":     row["agent_id"],
			"id":           row["session_id"],
			"last_user_at": stamp,
		})
	}
	return result, nil
}
