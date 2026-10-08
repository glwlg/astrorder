package journal

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRetentionReopenPrunesExpiredFrames(t *testing.T) {
	path := filepath.Join(t.TempDir(), "retention.db")
	s, err := OpenChecked(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.AppendChecked("d", "s", "text", map[string]any{}, "idle"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec(`UPDATE frame_retention SET received=0`); err != nil {
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = OpenChecked(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	p, err := s.Page("d", "s", 0, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Frames) != 0 || p.MinSeq != 2 || p.Through != 1 {
		t.Fatalf("expired data survived reopen: %+v", p)
	}
	next, err := s.AppendChecked("d", "s", "text", map[string]any{}, "idle")
	if err != nil || next.SeqID != 2 {
		t.Fatalf("reused sequence: %+v %v", next, err)
	}
}

func TestRetentionGlobalBudgetKeepsSessionSuffixes(t *testing.T) {
	s, err := OpenChecked(filepath.Join(t.TempDir(), "retention.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err = s.SetRetention(Retention{TotalBytes: 1200, SessionBytes: 900, MaxAge: time.Hour}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 10; i++ {
		for _, id := range []string{"a", "b", "c"} {
			if _, err = s.AppendChecked("d", id, "text", map[string]any{"text": strings.Repeat("x", 200)}, "idle"); err != nil {
				t.Fatal(err)
			}
		}
	}
	var used, actual int64
	if err = s.db.QueryRow(`SELECT bytes FROM retention_usage`).Scan(&used); err != nil {
		t.Fatal(err)
	}
	if err = s.db.QueryRow(`SELECT COALESCE(SUM(bytes),0) FROM frame_retention`).Scan(&actual); err != nil {
		t.Fatal(err)
	}
	if used != actual || used > 1200 {
		t.Fatalf("budget/accounting: %d %d", used, actual)
	}
	for _, id := range []string{"a", "b", "c"} {
		frames, err := s.Replay("d", id, 0)
		if err != nil {
			t.Fatal(err)
		}
		for i, f := range frames {
			if f.SeqID != int64(10-len(frames)+i+1) {
				t.Fatalf("noncontiguous tail: %+v", frames)
			}
		}
	}
}

func TestRetentionExpiresPayloadWithoutResettingSequence(t *testing.T) {
	s, err := OpenChecked(filepath.Join(t.TempDir(), "retention.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	// Configure tiny limits to exercise production pruning without large fixtures.
	if err = s.SetRetention(Retention{TotalBytes: 2048, SessionBytes: 600, MaxAge: time.Hour}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 6; i++ {
		if _, err = s.AppendChecked("d", "s", "text", map[string]any{"text": strings.Repeat("x", 200)}, "idle"); err != nil {
			t.Fatal(err)
		}
	}
	page, err := s.Page("d", "s", 0, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	if page.MinSeq <= 1 || page.Through != 6 || len(page.Frames) > 2 {
		t.Fatalf("retention not enforced: %+v", page)
	}
	if err = s.Prune(time.Now().Add(2 * time.Hour)); err != nil {
		t.Fatal(err)
	}
	page, err = s.Page("d", "s", 0, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Frames) != 0 || page.Through != 6 || page.MinSeq != 7 {
		t.Fatalf("expired cursor metadata lost: %+v", page)
	}
	next, err := s.AppendChecked("d", "s", "text", map[string]any{}, "idle")
	if err != nil || next.SeqID != 7 {
		t.Fatalf("sequence reused: %+v %v", next, err)
	}
}
