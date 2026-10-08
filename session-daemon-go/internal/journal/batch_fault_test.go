package journal

import (
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestBatchDiskFullRollsBackEveryMember(t *testing.T) {
	s, err := OpenChecked(filepath.Join(t.TempDir(), "journal.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err = s.AppendChecked("d", "seed", "seed", nil, "idle"); err != nil {
		t.Fatal(err)
	}
	var pages int
	if err = s.db.QueryRow("PRAGMA page_count").Scan(&pages); err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec("PRAGMA max_page_count=" + strconv.Itoa(pages)); err != nil {
		t.Fatal(err)
	}
	_, err = s.AppendBatch([]Frame{
		{DaemonID: "d", SessionID: "small", Event: "data", Status: "running", Payload: map[string]any{"text": "small"}},
		{DaemonID: "d", SessionID: "large", Event: "data", Status: "running", Payload: map[string]any{"text": strings.Repeat("x", 1024*1024)}},
	})
	if err == nil {
		t.Fatal("full database accepted batch")
	}
	for _, id := range []string{"small", "large"} {
		frames, e := s.Replay("d", id, 0)
		if e != nil || len(frames) != 0 {
			t.Fatalf("partial batch persisted for %s: %v %v", id, frames, e)
		}
	}
	latest, err := s.Latest("d")
	if err != nil || len(latest) != 1 || latest[0].SessionID != "seed" {
		t.Fatalf("catalog changed: %v %v", latest, err)
	}
	if _, err = s.db.Exec("PRAGMA max_page_count=10000"); err != nil {
		t.Fatal(err)
	}
	result, err := s.AppendBatch([]Frame{{DaemonID: "d", SessionID: "small", Event: "data", Status: "idle"}, {DaemonID: "d", SessionID: "small", Event: "data", Status: "idle"}})
	if err != nil || len(result) != 2 || result[0].SeqID != 1 || result[1].SeqID != 2 {
		t.Fatalf("recovered sequence: %v %v", result, err)
	}
}
