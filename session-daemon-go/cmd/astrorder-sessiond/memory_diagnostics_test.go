package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestMemoryDiagnosticArtifacts(t *testing.T) {
	dir := t.TempDir()
	var before runtime.MemStats
	runtime.ReadMemStats(&before)
	if err := captureMemory(dir, before); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"heap.pprof", "allocs.pprof"} {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil || len(data) < 2 || data[0] != 0x1f || data[1] != 0x8b {
			t.Fatalf("invalid profile %s: %v", name, err)
		}
	}
	data, err := os.ReadFile(filepath.Join(dir, "metrics.json"))
	if err != nil {
		t.Fatal(err)
	}
	var report map[string]any
	if json.Unmarshal(data, &report) != nil || report["before_gc"] == nil || report["after_gc"] == nil {
		t.Fatal("missing GC comparison")
	}
}
