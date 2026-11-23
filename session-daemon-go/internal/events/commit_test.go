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

func TestDurableCommitBatchPreservesOrderAndChunking(t *testing.T) {
	p := filepath.Join(t.TempDir(), "journal-batch.db")
	j, err := Open(p, 16)
	if err != nil {
		t.Fatal(err)
	}
	sub := j.SubscribeWithCapacity(256)
	input := make([]Frame, 150)
	for i := range input {
		input[i] = Frame{
			SessionID: "stream-1",
			Event:     "codex.notification",
			Payload:   map[string]any{"n": i + 1},
			Status:    "running",
		}
	}
	committed, err := j.CommitBatch(input)
	if err != nil {
		t.Fatalf("CommitBatch: %v", err)
	}
	if len(committed) != len(input) {
		t.Fatalf("committed len=%d want %d", len(committed), len(input))
	}
	for i, f := range committed {
		if f.SeqID != int64(i+1) {
			t.Fatalf("committed[%d].SeqID=%d want %d", i, f.SeqID, i+1)
		}
	}
	for i := 0; i < len(input); i++ {
		f := <-sub.frames
		if f.SeqID != int64(i+1) {
			t.Fatalf("live[%d].SeqID=%d want %d", i, f.SeqID, i+1)
		}
	}
	if err = j.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(p, 16)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	replayed, err := reopened.Replay("stream-1", 0)
	if err != nil || len(replayed) != len(input) {
		t.Fatalf("replayed len=%d err=%v", len(replayed), err)
	}
	for i, f := range replayed {
		if f.SeqID != int64(i+1) {
			t.Fatalf("replayed[%d].SeqID=%d want %d", i, f.SeqID, i+1)
		}
	}
}
