package journal

import (
	"database/sql"
	"path/filepath"
	"testing"
)

func TestTimestampSurvivesMigrationAndReplay(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`CREATE TABLE frames(daemon_id TEXT,session_id TEXT,seq_id INTEGER,event TEXT,payload TEXT,status TEXT,PRIMARY KEY(daemon_id,session_id,seq_id)); INSERT INTO frames VALUES('d','s',1,'old','{}','idle')`)
	if err != nil {
		t.Fatal(err)
	}
	db.Close()
	s := Open(path)
	f, err := s.AppendAt("d", "s", "token", map[string]any{}, "running", 1234.5)
	if err != nil {
		t.Fatal(err)
	}
	if f.Timestamp != 1234.5 {
		t.Fatal(f)
	}
	s.Close()
	s = Open(path)
	defer s.Close()
	p, err := s.Page("d", "s", 0, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Frames) != 2 || p.Frames[1].Timestamp != 1234.5 {
		t.Fatal(p)
	}
	if p.Frames[0].Timestamp != 0 {
		t.Fatal("invented timestamp for legacy frame")
	}
	replay, err := s.Replay("d", "s", 1)
	if err != nil || replay[0].Timestamp != 1234.5 {
		t.Fatal(replay, err)
	}
}
