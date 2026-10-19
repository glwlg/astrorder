package kernelcontrol

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestConcurrentRequestsAcceptOnlyOneOperation(t *testing.T) {
	s := &Service{StateDir: t.TempDir(), Launch: func(string) error { return nil }}
	var wg sync.WaitGroup
	results := make(chan error, 20)
	for range 20 {
		wg.Add(1)
		go func() { defer wg.Done(); _, err := s.Start("app", "restart", true); results <- err }()
	}
	wg.Wait()
	close(results)
	accepted := 0
	for err := range results {
		if err == nil {
			accepted++
		} else if err != ErrBusy {
			t.Fatal(err)
		}
	}
	if accepted != 1 {
		t.Fatal(accepted)
	}
}
func TestReceiptsAreBoundedAndExpiredJobsAreNotRunningForever(t *testing.T) {
	s := &Service{StateDir: t.TempDir(), Launch: func(string) error { return nil }}
	op, _ := s.Start("app", "restart", true)
	op.CreatedAt = time.Now().Add(-4 * time.Minute)
	if err := s.save(op); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Get(op.ID)
	if got.Status != "failed" {
		t.Fatal(got)
	}
	for i := 0; i < 120; i++ {
		if err := s.save(Operation{ID: fmt.Sprintf("%032x", i), Status: "success", CreatedAt: time.Now()}); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Remove(filepath.Join(s.dir(), "active.json")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Start("app", "restart", true); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(s.dir())
	if len(entries) > 101 {
		t.Fatalf("unbounded receipts: %d", len(entries))
	}
}
