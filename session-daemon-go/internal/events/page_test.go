package events

import (
	"path/filepath"
	"testing"
)

func TestDurableWatermarksIgnoreMemoryEviction(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.db")
	j, err := Open(path, 1)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if _, err = j.Commit("s", "token", nil, "running"); err != nil {
			t.Fatal(err)
		}
	}
	if j.Statuses()["s"]["min_seq_id"] != int64(1) {
		t.Fatal("durable low watermark used memory eviction")
	}
	j.Close()
	j, err = Open(path, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer j.Close()
	if j.Statuses()["s"]["min_seq_id"] != int64(1) {
		t.Fatal("reopen lost low watermark")
	}
	page, err := j.Page("s", 0, 0, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Frames) != 2 || page.Through != 3 || !page.HasMore {
		t.Fatalf("bad durable page %+v", page)
	}
}
