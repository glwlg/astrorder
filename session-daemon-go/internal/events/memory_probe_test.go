package events

import (
	"os"
	"runtime"
	"testing"
	"time"
)

func TestJournalMemoryProbe(t *testing.T) {
	path := os.Getenv("ASTRORDER_JOURNAL_MEMORY_DB")
	if path == "" {
		t.Skip("isolated copied journal only")
	}
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	start := time.Now()
	j, err := Open(path, 2000)
	if err != nil {
		t.Fatal(err)
	}
	defer j.Close()
	for i := 0; i < 10; i++ {
		b, err := j.BeginSubscription(1)
		if err != nil {
			t.Fatal(err)
		}
		j.Unsubscribe(b.Subscription)
	}
	runtime.ReadMemStats(&after)
	t.Logf("elapsed=%s allocations=%d heap=%d sys=%d", time.Since(start), after.TotalAlloc-before.TotalAlloc, after.HeapAlloc, after.Sys)
}
