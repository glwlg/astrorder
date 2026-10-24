package journal

// RetiredSessions restores rejection fences before any live event is admitted.
func (s *Store) RetiredSessions(daemonID string) ([]string, error) {
	rows, err := s.db.Query(`SELECT session_id FROM catalog WHERE daemon_id=? AND retired=1`, daemonID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func (s *Store) TrackSession(daemonID, id, status string) error {
	_, err := s.db.Exec(`INSERT INTO catalog(daemon_id,session_id,status,retired) VALUES(?,?,?,0) ON CONFLICT(daemon_id,session_id) DO UPDATE SET status=excluded.status,retired=0`, daemonID, id, status)
	return err
}
func (s *Store) ForgetSession(daemonID, id string) error {
	_, err := s.db.Exec(`UPDATE catalog SET retired=1,status='idle' WHERE daemon_id=? AND session_id=?`, daemonID, id)
	return err
}
