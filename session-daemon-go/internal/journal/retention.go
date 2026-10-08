package journal

import (
	"database/sql"
	"fmt"
	"time"
)

// Retention bounds encoded transport data, including a per-frame metadata
// allowance. SQLite file pages are reused; this is not a physical file-size cap.
type Retention struct {
	TotalBytes   int64         `json:"total_bytes"`
	SessionBytes int64         `json:"session_bytes"`
	MaxAge       time.Duration `json:"-"`
}

func migrateRetention(db *sql.DB) error {
	_, err := db.Exec(`
 CREATE TABLE IF NOT EXISTS stream_watermarks(daemon_id TEXT NOT NULL,session_id TEXT NOT NULL,max_seq INTEGER NOT NULL,bytes INTEGER NOT NULL DEFAULT 0,PRIMARY KEY(daemon_id,session_id));
 CREATE TABLE IF NOT EXISTS frame_retention(frame_rowid INTEGER PRIMARY KEY,bytes INTEGER NOT NULL,received REAL NOT NULL);
 CREATE INDEX IF NOT EXISTS frame_retention_age ON frame_retention(received);
 CREATE TABLE IF NOT EXISTS retention_settings(id INTEGER PRIMARY KEY CHECK(id=1),total_bytes INTEGER NOT NULL,session_bytes INTEGER NOT NULL,max_age REAL NOT NULL,last_prune REAL NOT NULL DEFAULT 0);
 INSERT OR IGNORE INTO retention_settings VALUES(1,268435456,16777216,604800,0);
 CREATE TABLE IF NOT EXISTS retention_usage(id INTEGER PRIMARY KEY CHECK(id=1),bytes INTEGER NOT NULL);
 INSERT OR IGNORE INTO retention_usage VALUES(1,0);
 INSERT OR IGNORE INTO frame_retention SELECT rowid,length(CAST(payload AS BLOB))+length(event)+length(session_id)+length(daemon_id)+128,unixepoch('now') FROM frames;
 INSERT INTO stream_watermarks SELECT daemon_id,session_id,MAX(seq_id),0 FROM frames GROUP BY daemon_id,session_id ON CONFLICT(daemon_id,session_id) DO UPDATE SET max_seq=MAX(max_seq,excluded.max_seq);
 UPDATE stream_watermarks SET bytes=COALESCE((SELECT SUM(r.bytes) FROM frames f JOIN frame_retention r ON r.frame_rowid=f.rowid WHERE f.daemon_id=stream_watermarks.daemon_id AND f.session_id=stream_watermarks.session_id),0);
 UPDATE retention_usage SET bytes=(SELECT COALESCE(SUM(bytes),0) FROM frame_retention);
 CREATE TRIGGER IF NOT EXISTS frames_retention_insert AFTER INSERT ON frames BEGIN
 INSERT INTO frame_retention VALUES(new.rowid,length(CAST(new.payload AS BLOB))+length(new.event)+length(new.session_id)+length(new.daemon_id)+128,unixepoch('now'));
 INSERT INTO stream_watermarks VALUES(new.daemon_id,new.session_id,new.seq_id,(SELECT bytes FROM frame_retention WHERE frame_rowid=new.rowid)) ON CONFLICT(daemon_id,session_id) DO UPDATE SET max_seq=MAX(max_seq,new.seq_id),bytes=bytes+excluded.bytes;
 UPDATE retention_usage SET bytes=bytes+(SELECT bytes FROM frame_retention WHERE frame_rowid=new.rowid);
 END;
 CREATE TRIGGER IF NOT EXISTS frames_retention_delete AFTER DELETE ON frames BEGIN
 UPDATE stream_watermarks SET bytes=bytes-(SELECT bytes FROM frame_retention WHERE frame_rowid=old.rowid) WHERE daemon_id=old.daemon_id AND session_id=old.session_id;
 UPDATE retention_usage SET bytes=bytes-(SELECT bytes FROM frame_retention WHERE frame_rowid=old.rowid);
 DELETE FROM frame_retention WHERE frame_rowid=old.rowid;
 END;`)
	return err
}

func (s *Store) SetRetention(r Retention) error {
	if r.TotalBytes <= 0 || r.SessionBytes <= 0 || r.SessionBytes > r.TotalBytes || r.MaxAge <= 0 {
		return fmt.Errorf("invalid journal retention limits")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`UPDATE retention_settings SET total_bytes=?,session_bytes=?,max_age=?,last_prune=0 WHERE id=1`, r.TotalBytes, r.SessionBytes, r.MaxAge.Seconds()); err != nil {
		return err
	}
	if err = pruneFrames(tx, time.Now(), true); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) Prune(now time.Time) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = pruneFrames(tx, now, true); err != nil {
		return err
	}
	return tx.Commit()
}

func pruneFrames(tx *sql.Tx, now time.Time, force bool) error {
	var total, perSession, used int64
	var age, last float64
	if err := tx.QueryRow(`SELECT total_bytes,session_bytes,max_age,last_prune,(SELECT bytes FROM retention_usage WHERE id=1) FROM retention_settings WHERE id=1`).Scan(&total, &perSession, &age, &last, &used); err != nil {
		return err
	}
	current := float64(now.UnixMicro()) / 1e6
	if force || current-last >= 60 {
		if _, err := tx.Exec(`DELETE FROM frames WHERE rowid IN (SELECT frame_rowid FROM frame_retention WHERE received<?)`, current-age); err != nil {
			return err
		}
		if _, err := tx.Exec(`UPDATE retention_settings SET last_prune=? WHERE id=1`, current); err != nil {
			return err
		}
	}
	rows, err := tx.Query(`SELECT daemon_id,session_id FROM stream_watermarks WHERE bytes>?`, perSession)
	if err != nil {
		return err
	}
	var streams [][2]string
	for rows.Next() {
		var stream [2]string
		if err = rows.Scan(&stream[0], &stream[1]); err != nil {
			rows.Close()
			return err
		}
		streams = append(streams, stream)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, stream := range streams {
		if _, err = tx.Exec(`DELETE FROM frames WHERE rowid IN (SELECT rid FROM (SELECT f.rowid AS rid,SUM(r.bytes) OVER(ORDER BY f.seq_id DESC) AS retained FROM frames f JOIN frame_retention r ON r.frame_rowid=f.rowid WHERE f.daemon_id=? AND f.session_id=?) WHERE retained>?)`, stream[0], stream[1], perSession); err != nil {
			return err
		}
	}
	if used > total {
		if _, err = tx.Exec(`DELETE FROM frames WHERE rowid IN (SELECT rid FROM (SELECT frame_rowid AS rid,SUM(bytes) OVER(ORDER BY received DESC,frame_rowid DESC) AS retained FROM frame_retention) WHERE retained>?)`, total); err != nil {
			return err
		}
	}
	return nil
}
