package antigravity

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	core "astrorder.dev/session-daemon/internal/runtime"
)

func TestNativeUsageIsCumulativeAndMissingIsNotZero(t *testing.T) {
	exe, _ := os.Executable()
	dir := t.TempDir()
	var mu sync.Mutex
	var metrics []map[string]any
	a := New(Config{
		Executable:  exe,
		Arguments:   []string{"-test.run=TestStreamHelper", "--"},
		Environment: []string{"ASTRORDER_AGY_HELPER=1"},
		Workspace:   dir,
		Allowed:     []string{dir},
		Emit: func(id, kind string, p map[string]any, status string) error {
			if kind == "token.usage" {
				mu.Lock()
				metrics = append(metrics, p)
				mu.Unlock()
			}
			return nil
		},
	})
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
	for _, usage := range []map[string]any{
		{"input_tokens": float64(11), "output_tokens": float64(3), "thinking_tokens": float64(0), "cache_read_tokens": float64(0), "total_tokens": float64(14)},
		nil,
		{"input_tokens": float64(22), "output_tokens": float64(6), "thinking_tokens": float64(0), "cache_read_tokens": float64(0), "total_tokens": float64(28)},
	} {
		p := map[string]any{}
		if usage != nil {
			p["usage"] = usage
		}
		if err := a.emitUsage(s, p); err != nil {
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
		t.Fatalf("cumulative input without ACP usage_update or DB window is not context usage: %+v", metrics[1])
	}
}

func TestACPUsageUpdatePreservesCumulativeAndDefersResumeZero(t *testing.T) {
	home := t.TempDir()
	convDir := filepath.Join(home, ".gemini", "antigravity-cli", "conversations")
	if err := os.MkdirAll(convDir, 0o755); err != nil {
		t.Fatal(err)
	}
	id := "44444444-4444-4444-8444-444444444444"
	db, err := sql.Open("sqlite", filepath.Join(convDir, id+".db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		CREATE TABLE steps(idx INTEGER PRIMARY KEY,step_type INTEGER,status INTEGER,step_format INTEGER,step_payload BLOB,metadata BLOB);
		CREATE TABLE gen_metadata(idx INTEGER PRIMARY KEY,data BLOB,size INTEGER);
	`); err != nil {
		t.Fatal(err)
	}
	// Step 1: metadata column populated directly.
	step1Usage := append(pbVarint(2, 1000), pbVarint(3, 200)...)
	step1Usage = append(step1Usage, pbVarint(5, 4000)...)
	step1Meta := pbBytes(9, step1Usage)
	if _, err := db.Exec(`INSERT INTO steps VALUES(1,15,3,0,?,?)`, pbVarint(1, 15), step1Meta); err != nil {
		t.Fatal(err)
	}
	// Step 2: metadata empty, embedded inside step_payload field 5.
	step2Usage := append(pbVarint(2, 500), pbVarint(3, 100)...)
	step2Usage = append(step2Usage, pbVarint(5, 9500)...)
	step2Payload := append(pbVarint(1, 15), pbBytes(5, pbBytes(9, step2Usage))...)
	if _, err := db.Exec(`INSERT INTO steps VALUES(2,15,3,0,?,NULL)`, step2Payload); err != nil {
		t.Fatal(err)
	}
	db.Close()

	var mu sync.Mutex
	var metrics []map[string]any
	ad := New(Config{
		Home: home,
		Emit: func(_, kind string, p map[string]any, _ string) error {
			if kind == "token.usage" {
				mu.Lock()
				metrics = append(metrics, p)
				mu.Unlock()
			}
			return nil
		},
	})
	s := newSession(id, home, nil)
	s.isResumed = true
	s.model = "gemini-4-carbon-high"
	defer s.stopWorker()

	// 1. Initial session/resume usage_update with used=0 must record size=1048576 without emitting 0.
	ad.handleSessionUpdate(s, map[string]any{
		"sessionUpdate": "usage_update",
		"used":          float64(0),
		"size":          float64(1048576),
	})
	mu.Lock()
	if len(metrics) != 0 {
		t.Fatalf("expected initial resume used=0 to defer until snapshot, got %+v", metrics)
	}
	mu.Unlock()

	// 2. Snapshot combines ACP 1M window with SQLite historical context & cumulative totals.
	ad.emitSessionUsageSnapshot(context.Background(), s)
	mu.Lock()
	if len(metrics) != 1 {
		t.Fatalf("expected 1 snapshot usage event, got %+v", metrics)
	}
	snap := metrics[0]
	mu.Unlock()
	if snap["context_window"] != int64(1048576) || snap["last_input_tokens"] != int64(10000) || snap["total_tokens"] != int64(15300) {
		t.Fatalf("unexpected combined snapshot: %+v", snap)
	}

	// 3. Mid-turn ACP usage_update updates last_input_tokens while preserving cumulative totals.
	ad.handleSessionUpdate(s, map[string]any{
		"sessionUpdate": "usage_update",
		"used":          float64(81208),
		"size":          float64(1048576),
	})
	mu.Lock()
	defer mu.Unlock()
	if len(metrics) != 2 {
		t.Fatalf("expected 2 usage events, got %+v", metrics)
	}
	live := metrics[1]
	if live["last_input_tokens"] != int64(81208) || live["context_window"] != int64(1048576) || live["total_tokens"] != int64(15300) || live["cached_tokens"] != int64(13500) {
		t.Fatalf("live usage_update wiped cumulative counters: %+v", live)
	}
}
