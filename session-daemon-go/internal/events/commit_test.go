package events

import (
	"fmt"
	"path/filepath"
	"sync"
	"testing"
)

func TestDurableBatchOrderRetirementAndClose(t *testing.T) {
	p := filepath.Join(t.TempDir(), "journal.db")
	j, err := Open(p, 16)
	if err != nil {
		t.Fatal(err)
	}
	if err = j.TrackSession("retired", "idle"); err != nil {
		t.Fatal(err)
	}
	if err = j.ForgetSession("retired"); err != nil {
		t.Fatal(err)
	}
	sub := j.SubscribeWithCapacity(256)
	var wg sync.WaitGroup
	for s := 0; s < 8; s++ {
		wg.Add(1)
		go func(s int) {
			defer wg.Done()
			id := fmt.Sprint(s)
			for n := 1; n <= 16; n++ {
				f, e := j.Commit(id, "data", map[string]any{"n": n}, "running")
				if e != nil || f.SeqID != int64(n) {
					t.Errorf("commit: %v %v", f, e)
					return
				}
			}
		}(s)
	}
	if _, err = j.Commit("retired", "bad", nil, "running"); err == nil {
		t.Fatal("retired frame accepted")
	}
	if _, err = j.Commit("invalid", "bad", map[string]any{"bad": make(chan int)}, "running"); err == nil {
		t.Fatal("invalid frame accepted")
	}
	wg.Wait()
	seen := map[string]int64{}
	for len(sub.frames) > 0 {
		f := <-sub.frames
		if f.SeqID != seen[f.SessionID]+1 {
			t.Fatal("live order lost")
		}
		seen[f.SessionID] = f.SeqID
	}
	if len(seen) != 8 {
		t.Fatal(seen)
	}
	if err = j.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err = j.Commit("late", "bad", nil, "running"); err == nil {
		t.Fatal("closed commit accepted")
	}
	reopened, err := Open(p, 16)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	for id := range seen {
		frames, e := reopened.Replay(id, 0)
		if e != nil || len(frames) != 16 {
			t.Fatalf("durable replay: %v %v", frames, e)
		}
		for n, f := range frames {
			if f.SeqID != int64(n+1) {
				t.Fatal("durable order lost")
			}
		}
	}
}
