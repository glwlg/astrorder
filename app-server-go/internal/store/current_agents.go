package store

import "context"

// ListCurrentAgents excludes disconnected orphan registrations without sessions.
// Keep their rows intact for diagnostics; configured or historically used agents remain visible.
func (s *Store) ListCurrentAgents(ctx context.Context) ([]map[string]any, error) {
	rows, err := s.QueryRows(ctx, `SELECT a.* FROM agents a WHERE NOT (
 COALESCE(a.connection_id,'') != '' AND a.status='disconnected'
 AND NOT EXISTS (SELECT 1 FROM ssh_connections c WHERE c.id=a.connection_id)
 AND NOT EXISTS (SELECT 1 FROM sessions s WHERE s.agent_id=a.id)
 ) ORDER BY a.id`)
	if err != nil {
		return nil, err
	}
	return wireRows(rows, agentWire)
}
