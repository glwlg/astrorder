package journal

import (
	"database/sql"
	"time"

	_ "modernc.org/sqlite"
)

type Frame struct {
	Timestamp float64
	DaemonID  string
	SessionID string
	SeqID     int64
	Event     string
	Payload   map[string]any
	Status    string
}

type Store struct {
	db *sql.DB
}

func Open(path string) *Store {
	store, err := openChecked(path)
	if err != nil {
		panic(err)
	}
	return store
}

func openChecked(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err = db.Exec(`PRAGMA journal_mode=WAL; PRAGMA synchronous=FULL; PRAGMA busy_timeout=30000;
 CREATE TABLE IF NOT EXISTS frames (
 daemon_id TEXT NOT NULL, session_id TEXT NOT NULL, seq_id INTEGER NOT NULL,
 event TEXT NOT NULL, payload TEXT NOT NULL, status TEXT NOT NULL,
 PRIMARY KEY (daemon_id,session_id,seq_id));`); err != nil {
		db.Close()
		return nil, err
	}
	if err = migrateTimestamp(db); err != nil {
		db.Close()
		return nil, err
	}
	if _, err = db.Exec(`CREATE TABLE IF NOT EXISTS catalog (daemon_id TEXT NOT NULL,session_id TEXT NOT NULL,status TEXT NOT NULL,retired INTEGER NOT NULL DEFAULT 0,PRIMARY KEY(daemon_id,session_id));
 INSERT OR IGNORE INTO catalog(daemon_id,session_id,status) SELECT f.daemon_id,f.session_id,f.status FROM frames f WHERE f.seq_id=(SELECT MAX(seq_id) FROM frames WHERE daemon_id=f.daemon_id AND session_id=f.session_id);`); err != nil {
		db.Close()
		return nil, err
	}
	if _, err = db.Exec(`CREATE TABLE IF NOT EXISTS operations (scope TEXT NOT NULL,id TEXT NOT NULL,digest TEXT NOT NULL,response BLOB,PRIMARY KEY(scope,id));`); err != nil {
		db.Close()
		return nil, err
	}
	if err = migrateRetention(db); err != nil {
		db.Close()
		return nil, err
	}
	store := &Store{db: db}
	if err = store.Prune(time.Now()); err != nil {
		db.Close()
		return nil, err
	}
	return store, nil
}

func (store *Store) Append(daemonID, sessionID, event string, payload map[string]any, status string) Frame {
	frame, err := store.AppendChecked(daemonID, sessionID, event, payload, status)
	if err != nil {
		panic(err)
	}
	return frame
}

func (store *Store) After(daemonID, sessionID string, checkpoint int64) []Frame {
	frames, err := store.Replay(daemonID, sessionID, checkpoint)
	if err != nil {
		panic(err)
	}
	return frames
}

func (store *Store) Close() error {
	return store.db.Close()
}
