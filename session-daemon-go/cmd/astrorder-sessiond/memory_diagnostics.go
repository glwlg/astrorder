package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"runtime/pprof"
	"time"
)

// Capture allocation stacks, never application values or raw process memory.
// At most one incident per process bounds disk usage and forced-GC overhead.
func monitorMemory(database string, done <-chan struct{}) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	captured := false
	for {
		select {
		case <-done:
			return
		case <-ticker.C:
			var stats runtime.MemStats
			runtime.ReadMemStats(&stats)
			managed := stats.Sys - stats.HeapReleased
			log.Printf("memory heap_alloc=%d heap_inuse=%d heap_idle=%d heap_released=%d managed=%d goroutines=%d gc=%d", stats.HeapAlloc, stats.HeapInuse, stats.HeapIdle, stats.HeapReleased, managed, runtime.NumGoroutine(), stats.NumGC)
			if captured || managed < 768<<20 {
				continue
			}
			dir := filepath.Join(filepath.Dir(database), "diagnostics", fmt.Sprintf("memory-%d-%s", os.Getpid(), time.Now().UTC().Format("20060102T150405Z")))
			if err := os.MkdirAll(dir, 0700); err != nil {
				log.Printf("memory diagnostics: %v", err)
				continue
			}
			captured = true
			if err := captureMemory(dir, stats); err != nil {
				log.Printf("memory diagnostics: %v", err)
			} else {
				log.Printf("memory diagnostics saved: %s", dir)
			}
		}
	}
}
func captureMemory(dir string, before runtime.MemStats) error {
	writeProfile := func(name string) error {
		f, err := os.OpenFile(filepath.Join(dir, name+".pprof"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			return err
		}
		err = pprof.Lookup(name).WriteTo(f, 0)
		closeErr := f.Close()
		if err != nil {
			return err
		}
		return closeErr
	}
	if err := writeProfile("allocs"); err != nil {
		return err
	}
	runtime.GC()
	var after runtime.MemStats
	runtime.ReadMemStats(&after)
	if err := writeProfile("heap"); err != nil {
		return err
	}
	report := map[string]any{"pid": os.Getpid(), "before_gc": before, "after_gc": after, "goroutines": runtime.NumGoroutine()}
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "metrics.json"), data, 0600)
}
