package events

import (
	"path/filepath"
	"sync"
	"testing"
)

func TestConcurrentCommitsBroadcastInSequence(t *testing.T) {
	j, err := Open(filepath.Join(t.TempDir(), "events.db"), 2)
	if err != nil {
		t.Fatal(err)
	}
	defer j.Close()
	sub := j.SubscribeWithCapacity(64)
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := j.Commit("s", "token", nil, "running"); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	for seq := int64(1); seq <= 32; seq++ {
		if f := <-sub.Frames(); f.SeqID != seq {
			t.Fatalf("out of order: %d expected %d", f.SeqID, seq)
		}
	}
	if j.Statuses()["s"]["max_seq_id"] != int64(32) {
		t.Fatal("wrong high watermark")
	}
}
