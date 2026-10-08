package journal

import (
	"encoding/json"
	"fmt"
	"math"
	"time"
)

// AppendBatch commits a bounded group atomically, amortizing the durable fsync.
// Callers must not publish any member before this returns successfully.
func (s *Store) AppendBatch(frames []Frame) ([]Frame, error) {
	encoded := make([][]byte, len(frames))
	snapshots := make([]map[string]any, len(frames))
	for i, f := range frames {
		if math.IsNaN(f.Timestamp) || math.IsInf(f.Timestamp, 0) {
			return nil, fmt.Errorf("timestamp must be finite")
		}
		var err error
		encoded[i], err = json.Marshal(f.Payload)
		if err != nil {
			return nil, err
		}
		if err = DecodePayload(encoded[i], &snapshots[i]); err != nil {
			return nil, err
		}
	}
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	result := make([]Frame, len(frames))
	for i, f := range frames {
		var retired int
		if err = tx.QueryRow(`SELECT COALESCE((SELECT retired FROM catalog WHERE daemon_id=? AND session_id=?),0)`, f.DaemonID, f.SessionID).Scan(&retired); err != nil {
			return nil, err
		}
		if retired != 0 {
			return nil, fmt.Errorf("session has been retired")
		}
		if err = tx.QueryRow(`SELECT COALESCE((SELECT max_seq FROM stream_watermarks WHERE daemon_id=? AND session_id=?),0)+1`, f.DaemonID, f.SessionID).Scan(&f.SeqID); err != nil {
			return nil, err
		}
		if _, err = tx.Exec(`INSERT INTO frames(daemon_id,session_id,seq_id,event,payload,status,timestamp) VALUES (?,?,?,?,?,?,?)`, f.DaemonID, f.SessionID, f.SeqID, f.Event, encoded[i], f.Status, f.Timestamp); err != nil {
			return nil, err
		}
		if f.Event != "runtime.owner" && f.Status != "" {
			if _, err = tx.Exec(`INSERT INTO catalog(daemon_id,session_id,status) VALUES (?,?,?) ON CONFLICT(daemon_id,session_id) DO UPDATE SET status=excluded.status`, f.DaemonID, f.SessionID, f.Status); err != nil {
				return nil, err
			}
		}
		f.Payload = snapshots[i]
		result[i] = f
	}
	if err = pruneFrames(tx, time.Now(), false); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return result, nil
}
