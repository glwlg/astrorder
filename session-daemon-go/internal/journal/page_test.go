package journal

import (
	"path/filepath"
	"testing"
)

func TestReplayPagesKeepSnapshotBoundary(t *testing.T) {
	s := Open(filepath.Join(t.TempDir(), "events.db"))
	defer s.Close()
	for i := 0; i < 5; i++ {
		s.Append("d", "s", "token", nil, "running")
	}
	p, err := s.Page("d", "s", 0, 0, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Frames) != 2 || !p.HasMore || p.Through != 5 || p.Next != 2 || p.MinSeq != 1 {
		t.Fatalf("bad first page: %+v", p)
	}
	s.Append("d", "s", "done", nil, "idle")
	p, err = s.Page("d", "s", p.Next, p.Through, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Frames) != 2 || p.Next != 4 || !p.HasMore || p.Status != "running" {
		t.Fatalf("snapshot drift: %+v", p)
	}
	p, err = s.Page("d", "s", p.Next, p.Through, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Frames) != 1 || p.Next != 5 || p.HasMore {
		t.Fatalf("bad last page: %+v", p)
	}
	for _, args := range [][3]int64{{-1, 0, 2}, {0, 0, 0}, {0, 0, 1001}, {6, 5, 2}, {0, 99, 2}} {
		if _, err = s.Page("d", "s", args[0], args[1], int(args[2])); err == nil {
			t.Fatalf("accepted invalid cursor %v", args)
		}
	}
}
