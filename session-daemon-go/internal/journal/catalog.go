package journal

func (s *Store) TrackSession(daemonID, id, status string) error {
	_, err := s.db.Exec(`INSERT INTO catalog(daemon_id,session_id,status,retired) VALUES(?,?,?,0) ON CONFLICT(daemon_id,session_id) DO UPDATE SET status=excluded.status,retired=0`, daemonID, id, status)
	return err
}
func (s *Store) ForgetSession(daemonID, id string) error {
	_, err := s.db.Exec(`UPDATE catalog SET retired=1,status='idle' WHERE daemon_id=? AND session_id=?`, daemonID, id)
	return err
}
