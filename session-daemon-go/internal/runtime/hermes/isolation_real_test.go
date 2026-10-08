package hermes

import (
	core "astrorder.dev/session-daemon/internal/runtime"
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRealHermesIsolatedGatewayOwnership(t *testing.T) {
	executable, source := os.Getenv("ASTRORDER_REAL_HERMES_PYTHON"), os.Getenv("ASTRORDER_REAL_HERMES_SOURCE")
	if executable == "" || source == "" {
		t.Skip("explicit native Hermes executable/source required")
	}
	home, workspace := t.TempDir(), t.TempDir()
	pool := NewIsolated(Config{Executable: executable, Arguments: []string{"-m", "tui_gateway.entry"}, Environment: []string{"HERMES_HOME=" + home, "PYTHONPATH=" + source, "HERMES_DISABLE_AUTO_UPDATE=1", "HERMES_DISABLE_LAZY_INSTALLS=1"}, Workspace: workspace, Allowed: []string{workspace}, StateDBPath: filepath.Join(home, "state.db")})
	defer pool.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	first, err := pool.Create(ctx, core.Request{Fields: map[string]any{"cwd": workspace, "title": "Isolated first"}})
	if err != nil {
		t.Fatal(err)
	}
	second, err := pool.Create(ctx, core.Request{Fields: map[string]any{"cwd": workspace, "title": "Isolated second"}})
	if err != nil {
		t.Fatal(err)
	}
	if first.SessionID == second.SessionID || pool.workers[first.SessionID].cmd.Process.Pid == pool.workers[second.SessionID].cmd.Process.Pid {
		t.Fatal("native sessions share identity or process")
	}
	firstWorker := pool.workers[first.SessionID]
	if err = firstWorker.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Command(ctx, core.Request{SessionID: second.SessionID, Action: "session.rename", Fields: map[string]any{"title": "Unaffected second"}}); err != nil {
		t.Fatal("another native gateway was affected", err)
	}
	// Native empty drafts are intentionally persisted only at the first prompt.
	// A fresh gateway must reject a lost draft; never fabricate recovery success.
	recovery := NewIsolated(pool.config)
	defer recovery.Close()
	if _, err = recovery.Spawn(ctx, core.Request{SessionID: first.SessionID}); err == nil {
		t.Fatal("empty native draft was falsely recovered")
	}
	if _, owned := recovery.Snapshot()[first.SessionID]; owned {
		t.Fatal("failed draft recovery acquired idle ownership")
	}
	for _, target := range []struct {
		pool *Isolated
		id   string
	}{{pool, second.SessionID}} {
		worker := target.pool.workers[target.id]
		for {
			listing, readErr := worker.rpc(ctx, "session.active_list", nil)
			if readErr != nil {
				t.Fatal(readErr)
			}
			rows, ok := listing["sessions"].([]any)
			if !ok {
				t.Fatal("invalid active list")
			}
			safe := true
			for _, raw := range rows {
				row, ok := raw.(map[string]any)
				if !ok {
					t.Fatal("invalid active row")
				}
				if row["session_key"] == target.id && row["status"] != "idle" {
					safe = false
				}
			}
			if safe {
				break
			}
			select {
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			case <-time.After(25 * time.Millisecond):
			}
		}
		if _, err = target.pool.Command(ctx, core.Request{SessionID: target.id, Action: "session.delete"}); err != nil {
			t.Fatal("native delete", err)
		}
	}
}
