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
	items, err := wireRows(rows, agentWire)
	if err != nil {
		return nil, err
	}
	for _, item := range items {
		connection, _ := item["connection_id"].(string)
		if connection == "" {
			connection = "local"
		}
		kind, _ := item["kind"].(string)
		enabled, present, err := s.GetAgentConnectionChoice(ctx, connection+":"+kind)
		if err != nil {
			return nil, err
		}
		if present && !enabled {
			item["status"] = "disconnected"
			item["limitation"] = "已主动断联，请在连接管理中重新接入"
		}
	}
	return items, nil
}
