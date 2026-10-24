package hermes

import (
	"context"
	"os"
	"runtime"
	"testing"
	"time"
)

func TestNativeHistoryMemoryProbe(t *testing.T) {
	path := os.Getenv("ASTRORDER_HISTORY_MEMORY_DB")
	if path == "" {
		t.Skip("read-only native history probe is opt-in")
	}
	sid := os.Getenv("ASTRORDER_HISTORY_MEMORY_SESSION")
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	start := time.Now()
	page, err := readNativePage(context.Background(), path, sid, "probe", "", 50)
	if err != nil {
		t.Fatal(err)
	}
	runtime.ReadMemStats(&after)
	t.Logf("rows=%d elapsed=%s allocated_bytes=%d heap_bytes=%d sys_bytes=%d", len(page["items"].([]map[string]any)), time.Since(start), after.TotalAlloc-before.TotalAlloc, after.HeapAlloc, after.Sys)
	runtime.KeepAlive(page)
}
