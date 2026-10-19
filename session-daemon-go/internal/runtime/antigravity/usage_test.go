package antigravity

import (
	core "astrorder.dev/session-daemon/internal/runtime"
	"context"
	"os"
	"sync"
	"testing"
	"time"
)

func TestNativeUsageIsCumulativeAndMissingIsNotZero(t *testing.T) {
	exe, _ := os.Executable()
	dir := t.TempDir()
	var mu sync.Mutex
	var metrics []map[string]any
	a := New(Config{Executable: exe, Arguments: []string{"-test.run=TestStreamHelper", "--"}, Environment: []string{"ASTRORDER_AGY_HELPER=1"}, Workspace: dir, Allowed: []string{dir}, Emit: func(id, kind string, p map[string]any, status string) error {
		if kind == "token.usage" {
			mu.Lock()
			metrics = append(metrics, p)
			mu.Unlock()
		}
		return nil
	}})
	defer a.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	created, err := a.Create(ctx, core.Request{Fields: map[string]any{}})
	if err != nil {
		t.Fatal(err)
	}
	a.mu.Lock()
	s := a.sessions[created.SessionID]
	a.mu.Unlock()
	for _, usage := range []map[string]any{{"input_tokens": float64(11), "output_tokens": float64(3), "thinking_tokens": float64(0), "cache_read_tokens": float64(0), "total_tokens": float64(14)}, nil, {"input_tokens": float64(22), "output_tokens": float64(6), "thinking_tokens": float64(0), "cache_read_tokens": float64(0), "total_tokens": float64(28)}} {
		s.mu.Lock()
		s.status = "running"
		s.mu.Unlock()
		p := map[string]any{"status": "SUCCESS"}
		if usage != nil {
			p["usage"] = usage
		}
		if err := a.finish(s, p); err != nil {
			t.Fatal(err)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if len(metrics) != 2 {
		t.Fatalf("want two native usage events, missing usage must not emit zero: %+v", metrics)
	}
	if metrics[1]["total_tokens"] != int64(28) || metrics[1]["reasoning_tokens"] != int64(0) {
		t.Fatalf("usage must overwrite cumulative counters and preserve measured zero: %+v", metrics[1])
	}
	if metrics[1]["context_available"] != false {
		t.Fatalf("cumulative input is not context usage: %+v", metrics[1])
	}
}
