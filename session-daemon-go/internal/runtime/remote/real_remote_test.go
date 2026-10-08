package remote

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	core "astrorder.dev/session-daemon/internal/runtime"
	sshtrans "astrorder.dev/session-daemon/internal/transport/ssh"
)

// TestRealSSHRemoteAdaptersOnDebian tests real OpenSSH transport against 192.168.1.100.
// It verifies remote Codex and remote Hermes adapter creation, spawn, command and teardown
// using disposable test workspaces and disposable session IDs without touching production.
func TestRealSSHRemoteAdaptersOnDebian(t *testing.T) {
	if os.Getenv("ASTRORDER_TEST_REAL_SSH") != "1" {
		t.Skip("skipping real SSH test; enable with ASTRORDER_TEST_REAL_SSH=1")
	}

	host := "192.168.1.100"
	user := "luwei"

	emit := func(sessionID, event string, payload map[string]any, status string) error {
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// 1. Test Remote Codex Factory & Adapter
	t.Run("RemoteCodex", func(t *testing.T) {
		codexFactory := NewRemoteCodexFactory(emit)
		codexBin := "/home/luwei/.vite-plus/bin/codex"
		settings := map[string]any{
			"host":             host,
			"user":             user,
			"port":             22,
			"codex_executable": codexBin,
			"workspace":        "/tmp",
		}

		adapter, err := codexFactory("conn-test-codex", settings)
		if err != nil {
			t.Fatalf("failed to create remote Codex adapter: %v", err)
		}
		if closer, ok := adapter.(interface{ Close() error }); ok {
			defer closer.Close()
		}

		spawner, ok := adapter.(core.Spawner)
		if !ok {
			t.Fatalf("adapter does not implement Spawner")
		}
		_ = spawner

		// Test create disposable session over SSH
		res, err := adapter.Create(ctx, core.Request{
			Fields: map[string]any{
				"cwd": "/tmp",
			},
		})
		if err != nil {
			t.Fatalf("failed to create remote Codex session: %v", err)
		}
		if res.SessionID == "" {
			t.Fatalf("expected non-empty session ID from remote Codex create")
		}

		sessionID := res.SessionID

		commander, ok := adapter.(core.Commander)
		if !ok {
			t.Fatalf("adapter does not implement Commander")
		}

		// Deletion must succeed, then native resume must reject the exact identity.
		if _, err = commander.Command(ctx, core.Request{
			SessionID: sessionID,
			Action:    "session.delete",
		}); err != nil {
			t.Fatalf("native deletion failed: %v", err)
		}
		if _, err = spawner.Spawn(ctx, core.Request{SessionID: sessionID, Fields: map[string]any{"cwd": "/tmp"}}); err == nil {
			t.Fatal("deleted Codex session resumed")
		}
	})

	// Exercise the real ACP transport through the remote factory as well.
	t.Run("RemoteGrok", func(t *testing.T) {
		adapter, err := NewRemoteGrokFactory(emit)("conn-test-grok", map[string]any{
			"host": host, "user": user, "port": 22,
			"grok_executable": "/home/luwei/.grok/bin/grok", "workspace": "/tmp",
		})
		if err != nil {
			t.Fatal(err)
		}
		if closer, ok := adapter.(interface{ Close() error }); ok {
			defer closer.Close()
		}
		created, err := adapter.Create(ctx, core.Request{Fields: map[string]any{"cwd": "/tmp"}})
		if err != nil || created.SessionID == "" {
			t.Fatalf("remote Grok create: %v", err)
		}
		commander, ok := adapter.(core.Commander)
		if !ok {
			t.Fatal("remote Grok lacks command interface")
		}
		if _, err = commander.Command(ctx, core.Request{SessionID: created.SessionID, Action: "session.models"}); err != nil {
			t.Fatal(err)
		}
		closed, err := commander.Command(ctx, core.Request{SessionID: created.SessionID, Action: "session.close"})
		if err != nil || closed.Payload["closed"] != true {
			t.Fatalf("remote Grok close: %v", err)
		}
		if _, err = commander.Command(ctx, core.Request{SessionID: created.SessionID, Action: "session.models"}); err == nil {
			t.Fatal("closed Grok handle still accepts commands")
		}
	})

	// 2. Test Remote Hermes Factory & Adapter
	t.Run("RemoteHermes", func(t *testing.T) {
		hermesFactory := NewRemoteHermesFactory(emit)
		hermesPython := "/home/luwei/.hermes/hermes-agent/venv/bin/python"
		created, err := exec.Command("ssh", "-o", "BatchMode=yes", user+"@"+host, "mktemp -d /home/luwei/astrorder-hermes-validation-XXXXXXXX").Output()
		if err != nil {
			t.Fatal(err)
		}
		home := strings.TrimSpace(string(created))
		if !strings.HasPrefix(home, "/home/luwei/astrorder-hermes-validation-") || strings.ContainsAny(home, " '\"\n\r;") {
			t.Fatal("invalid isolated home")
		}
		defer func() {
			if err := exec.Command("ssh", "-o", "BatchMode=yes", user+"@"+host, "rm -rf -- "+home).Run(); err != nil {
				t.Errorf("isolated home cleanup: %v", err)
			}
		}()
		settings := map[string]any{
			"host":              host,
			"user":              user,
			"port":              22,
			"hermes_executable": "/usr/bin/env",
			"arguments":         []any{"HERMES_HOME=" + home, "PYTHONPATH=/home/luwei/.hermes/hermes-agent", "HERMES_DISABLE_AUTO_UPDATE=1", "HERMES_DISABLE_LAZY_INSTALLS=1", hermesPython, "-m", "tui_gateway.entry"},
			"workspace":         home,
		}

		adapter, err := hermesFactory("conn-test-hermes", settings)
		if err != nil {
			t.Fatalf("failed to create remote Hermes adapter: %v", err)
		}
		if closer, ok := adapter.(interface{ Close() error }); ok {
			defer closer.Close()
		}

		// Remote Hermes 创建会话通过 Create 接口
		createRes, err := adapter.Create(ctx, core.Request{
			Fields: map[string]any{
				"cwd":   home,
				"title": "Disposable SSH Hermes Test",
			},
		})
		if err != nil {
			t.Fatalf("failed to create remote Hermes session over SSH: %v", err)
		}
		if createRes.SessionID == "" {
			t.Fatalf("expected non-empty session ID from remote Hermes create")
		}

		sessionID := createRes.SessionID

		commander, ok := adapter.(core.Commander)
		if !ok {
			t.Fatalf("adapter does not implement Commander")
		}

		// The gateway can still be constructing the agent after create returns.
		// Retry only the explicit conservative refusal; every attempt rereads
		// native active_list and still requires idle before closing the handle.
		for {
			_, err = commander.Command(ctx, core.Request{SessionID: sessionID, Action: "session.delete"})
			if err == nil {
				break
			}
			if !strings.Contains(err.Error(), "refusing to close an active or uncertain native handle") {
				t.Fatal(err)
			}
			select {
			case <-ctx.Done():
				t.Fatalf("native did not become deletable: %v", err)
			case <-time.After(50 * time.Millisecond):
			}
		}
		probe := "import sqlite3,sys; db=sqlite3.connect(sys.argv[1]); print(db.execute(\"SELECT COUNT(*) FROM sessions WHERE id=?\",(sys.argv[2],)).fetchone()[0])"
		output, err := exec.Command("ssh", "-o", "BatchMode=yes", user+"@"+host,
			hermesPython+" -c "+sshtrans.Quote(probe)+" "+sshtrans.Quote(home+"/state.db")+" "+sshtrans.Quote(sessionID)).Output()
		if err != nil || strings.TrimSpace(string(output)) != "0" {
			t.Fatalf("native deletion readback failed: %s %v", output, err)
		}
	})
}
