package journal

import (
	"crypto/rand"
	"encoding/hex"
	"time"
)

// OpenChecked reports storage errors without terminating the daemon.
func OpenChecked(path string) (store *Store, err error) {
	// Open remains the legacy convenience API; share its initialization below.
	return openChecked(path)
}

func (s *Store) Identity() (string, error) {
	if _, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS metadata (key TEXT PRIMARY KEY, value TEXT NOT NULL)`); err != nil {
		return "", err
	}
	bytes := make([]byte, 16)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	if _, err := s.db.Exec(`INSERT OR IGNORE INTO metadata(key,value) VALUES ('daemon_id',?)`, hex.EncodeToString(bytes)); err != nil {
		return "", err
	}
	var id string
	err := s.db.QueryRow(`SELECT value FROM metadata WHERE key='daemon_id'`).Scan(&id)
	return id, err
}

func (s *Store) AppendChecked(daemonID, sessionID, event string, payload map[string]any, status string) (Frame, error) {
	return s.AppendAt(daemonID, sessionID, event, payload, status, float64(time.Now().UnixMicro())/1e6)
}

func (s *Store) AppendAt(daemonID, sessionID, event string, payload map[string]any, status string, timestamp float64) (Frame, error) {
	frames, err := s.AppendBatch([]Frame{{Timestamp: timestamp, DaemonID: daemonID, SessionID: sessionID, Event: event, Payload: payload, Status: status}})
	if err != nil {
		return Frame{}, err
	}
	return frames[0], nil
}

func (s *Store) Replay(daemonID, sessionID string, checkpoint int64) ([]Frame, error) {
	rows, err := s.db.Query(`SELECT seq_id,event,payload,status,timestamp FROM frames WHERE daemon_id=? AND session_id=? AND seq_id>? ORDER BY seq_id`, daemonID, sessionID, checkpoint)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var frames []Frame
	for rows.Next() {
		f := Frame{DaemonID: daemonID, SessionID: sessionID}
		var raw []byte
		if err = rows.Scan(&f.SeqID, &f.Event, &raw, &f.Status, &f.Timestamp); err != nil {
			return nil, err
		}
		if err = DecodePayload(raw, &f.Payload); err != nil {
			return nil, err
		}
		frames = append(frames, f)
	}
	return frames, rows.Err()
}

func (s *Store) Latest(daemonID string) ([]Frame, error) {
	rows, err := s.db.Query(`SELECT c.session_id,COALESCE(w.max_seq,0),c.status,COALESCE((SELECT MIN(seq_id) FROM frames WHERE daemon_id=c.daemon_id AND session_id=c.session_id),w.max_seq+1,0) FROM catalog c LEFT JOIN stream_watermarks w ON w.daemon_id=c.daemon_id AND w.session_id=c.session_id WHERE c.daemon_id=? AND c.retired=0`, daemonID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var frames []Frame
	for rows.Next() {
		f := Frame{DaemonID: daemonID}
		var min int64
		if err = rows.Scan(&f.SessionID, &f.SeqID, &f.Status, &min); err != nil {
			return nil, err
		}
		f.Payload = map[string]any{"min_seq_id": min}
		frames = append(frames, f)
	}
	return frames, rows.Err()
}
