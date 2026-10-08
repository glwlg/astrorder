package events

import (
	"path/filepath"
	"testing"
)

func TestTrackedSessionWithoutFramesSurvivesReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.db")
	j, err := Open(path, 4)
	if err != nil {
		t.Fatal(err)
	}
	if err = j.TrackSession("empty", "idle"); err != nil {
		t.Fatal(err)
	}
	if j.Statuses()["empty"]["max_seq_id"] != int64(0) {
		t.Fatal(j.Statuses())
	}
	if err = j.Close(); err != nil {
		t.Fatal(err)
	}
	j, err = Open(path, 4)
	if err != nil {
		t.Fatal(err)
	}
	defer j.Close()
	page, err := j.Page("empty", 0, 0, 2)
	if err != nil || len(page.Frames) != 0 || page.Status != "idle" {
		t.Fatal(page, err)
	}
	if _, err = j.Commit("empty", "real.event", map[string]any{}, "running"); err != nil {
		t.Fatal(err)
	}
	if j.Statuses()["empty"]["max_seq_id"] != int64(1) {
		t.Fatal(j.Statuses())
	}
}
