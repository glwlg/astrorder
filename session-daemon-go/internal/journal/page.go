package journal

import (
	"database/sql"
	"fmt"
)

type ReplayPage struct {
	Frames  []Frame
	Next    int64
	Through int64
	MinSeq  int64
	HasMore bool
	Status  string
}

// Page reads a bounded page from a fixed committed high watermark.
// through=0 captures the current boundary; subsequent pages reuse Through.
func (s *Store) Page(daemonID, sessionID string, after, through int64, limit int) (ReplayPage, error) {
	p := ReplayPage{Frames: []Frame{}, Next: after}
	if after < 0 || through < 0 || limit < 1 || limit > 1000 {
		return p, fmt.Errorf("invalid replay bounds")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return p, err
	}
	defer tx.Rollback()
	var max int64
	if err = tx.QueryRow(`SELECT COALESCE((SELECT MIN(seq_id) FROM frames WHERE daemon_id=? AND session_id=?),max_seq+1),max_seq FROM stream_watermarks WHERE daemon_id=? AND session_id=?`, daemonID, sessionID, daemonID, sessionID).Scan(&p.MinSeq, &max); err != nil && err != sql.ErrNoRows {
		return p, err
	}
	if through == 0 {
		through = max
	}
	if through > max || after > through {
		return p, fmt.Errorf("replay cursor exceeds committed boundary")
	}
	p.Through = through
	if p.MinSeq > 0 && after < p.MinSeq-1 {
		// The fixed replay boundary may itself have expired between pages.
		// Return metadata so the protocol can request native-history rebuild.
		return p, tx.Commit()
	}
	if through == max {
		if err = tx.QueryRow(`SELECT COALESCE((SELECT status FROM catalog WHERE daemon_id=? AND session_id=?),'')`, daemonID, sessionID).Scan(&p.Status); err != nil {
			return p, err
		}
	}
	if through > 0 && p.Status == "" {
		if err = tx.QueryRow(`SELECT status FROM frames WHERE daemon_id=? AND session_id=? AND seq_id=?`, daemonID, sessionID, through).Scan(&p.Status); err != nil {
			return p, err
		}
	}
	rows, err := tx.Query(`SELECT seq_id,event,payload,status,timestamp FROM frames WHERE daemon_id=? AND session_id=? AND seq_id>? AND seq_id<=? ORDER BY seq_id LIMIT ?`, daemonID, sessionID, after, through, limit+1)
	if err != nil {
		return p, err
	}
	for rows.Next() {
		f := Frame{DaemonID: daemonID, SessionID: sessionID}
		var raw []byte
		if err = rows.Scan(&f.SeqID, &f.Event, &raw, &f.Status, &f.Timestamp); err != nil {
			rows.Close()
			return p, err
		}
		if len(p.Frames) == limit {
			p.HasMore = true
			break
		}
		if err = DecodePayload(raw, &f.Payload); err != nil {
			rows.Close()
			return p, err
		}
		p.Frames = append(p.Frames, f)
		p.Next = f.SeqID
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return p, err
	}
	if err = tx.Commit(); err != nil {
		return p, err
	}
	return p, nil
}
