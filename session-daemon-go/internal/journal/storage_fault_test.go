package journal

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestDiskFullDoesNotConsumeSequenceOrChangeStatus(t *testing.T) {
	s, err := OpenChecked(filepath.Join(t.TempDir(), "journal.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err = s.AppendChecked("daemon", "thread", "seed", map[string]any{}, "idle"); err != nil {
		t.Fatal(err)
	}
	var pages int
	if err = s.db.QueryRow("PRAGMA page_count").Scan(&pages); err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec("PRAGMA max_page_count=" + strconv.Itoa(pages)); err != nil {
		t.Fatal(err)
	}
	if _, err = s.AppendChecked("daemon", "thread", "large", map[string]any{"text": strings.Repeat("x", 1024*1024)}, "running"); err == nil {
		t.Fatal("disk-full insertion unexpectedly succeeded")
	}
	frames, err := s.Replay("daemon", "thread", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(frames) != 1 || frames[0].SeqID != 1 || frames[0].Status != "idle" {
		t.Fatalf("failed write changed durable state: %v", frames)
	}
	if _, err = s.db.Exec("PRAGMA max_page_count=10000"); err != nil {
		t.Fatal(err)
	}
	next, err := s.AppendChecked("daemon", "thread", "next", nil, "idle")
	if err != nil || next.SeqID != 2 {
		t.Fatalf("disk recovery: %v %v", next, err)
	}
}

func TestCorruptDatabaseRefusesStartup(t *testing.T) {
	p := filepath.Join(t.TempDir(), "journal.db")
	if err := os.WriteFile(p, []byte("corrupt SQLite database"), 0600); err != nil {
		t.Fatal(err)
	}
	s, err := OpenChecked(p)
	if s != nil {
		s.Close()
	}
	if err == nil {
		t.Fatal("corrupt journal accepted")
	}
}
