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

// TestRealSSHRemoteAdaptersOnDebian exercises a configured OpenSSH host.
// It verifies remote Codex and remote Hermes adapter creation, spawn, command and teardown
// using disposable test workspaces and disposable session IDs without touching production.
func TestRealSSHRemoteAdaptersOnDebian(t *testing.T) {
	if os.Getenv("ASTRORDER_TEST_REAL_SSH") != "1" {
		t.Skip("skipping real SSH test; enable with ASTRORDER_TEST_REAL_SSH=1")
	}

	host := os.Getenv("ASTRORDER_TEST_SSH_HOST")
	user := os.Getenv("ASTRORDER_TEST_SSH_USER")
	codexBin := os.Getenv("ASTRORDER_TEST_SSH_CODEX")
	grokBin := os.Getenv("ASTRORDER_TEST_SSH_GROK")
	hermesPython := os.Getenv("ASTRORDER_TEST_SSH_HERMES_PYTHON")
	hermesRoot := os.Getenv("ASTRORDER_TEST_SSH_HERMES_ROOT")
	if host == "" || user == "" || !strings.HasPrefix(codexBin, "/") ||
		!strings.HasPrefix(grokBin, "/") || !strings.HasPrefix(hermesPython, "/") ||
		!strings.HasPrefix(hermesRoot, "/") {
		t.Fatal("configure ASTRORDER_TEST_SSH_HOST, ASTRORDER_TEST_SSH_USER and absolute ASTRORDER_TEST_SSH_CODEX, ASTRORDER_TEST_SSH_GROK, ASTRORDER_TEST_SSH_HERMES_PYTHON, ASTRORDER_TEST_SSH_HERMES_ROOT")
	}

	emit := func(sessionID, event string, payload map[string]any, status string) error {
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// 1. Test Remote Codex Factory & Adapter
	t.Run("RemoteCodex", func(t *testing.T) {
		codexFactory := NewRemoteCodexFactory(emit)
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
			"grok_executable": grokBin, "workspace": "/tmp",
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
		created, err := exec.Command("ssh", "-o", "BatchMode=yes", user+"@"+host, "mktemp -d /tmp/astrorder-hermes-validation-XXXXXXXX").Output()
		if err != nil {
			t.Fatal(err)
		}
		home := strings.TrimSpace(string(created))
		if !strings.HasPrefix(home, "/tmp/astrorder-hermes-validation-") || strings.ContainsAny(home, " '\"\n\r;") {
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
			"arguments":         []any{"HERMES_HOME=" + home, "PYTHONPATH=" + hermesRoot, "HERMES_DISABLE_AUTO_UPDATE=1", "HERMES_DISABLE_LAZY_INSTALLS=1", hermesPython, "-m", "tui_gateway.entry"},
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

func TestRealWindowsSSHRemoteAntigravity(t *testing.T) {
	if os.Getenv("ASTRORDER_TEST_REAL_AGY_SSH") != "1" {
		t.Skip("skipping real Antigravity SSH test; enable with ASTRORDER_TEST_REAL_AGY_SSH=1")
	}
	host := os.Getenv("ASTRORDER_TEST_AGY_HOST")
	user := os.Getenv("ASTRORDER_TEST_AGY_USER")
	exe := os.Getenv("ASTRORDER_TEST_AGY_EXECUTABLE")
	if host == "" || user == "" || !strings.HasPrefix(exe, "/") {
		t.Fatal("configure ASTRORDER_TEST_AGY_HOST, ASTRORDER_TEST_AGY_USER and an absolute ASTRORDER_TEST_AGY_EXECUTABLE")
	}
	helpersDir := os.Getenv("ASTRORDER_TEST_HELPER_ASSETS_DIR")

	emit := func(string, string, map[string]any, string) error { return nil }
	factory := NewRemoteAntigravityFactory(emit, helpersDir)
	adapter, err := factory("wsl-live-verify", map[string]any{
		"host":                   host,
		"user":                   user,
		"port":                   22,
		"antigravity_executable": exe,
		"workspace":              "/home/" + user,
	})
	if err != nil {
		t.Fatalf("create remote Antigravity adapter: %v", err)
	}
	if disc, ok := adapter.(core.RuntimeDisconnector); ok {
		defer func() { _, _ = disc.Shutdown(context.Background()) }()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// 1. Verify exact "接入" path (AttachSession runtime_control -> Spawn -> models over Windows ssh.exe).
	spawner := adapter.(core.Spawner)
	probe, err := spawner.Spawn(ctx, core.Request{
		SessionID: "probe-antigravity-wsl-live-verify",
		Fields:    map[string]any{"runtime_control": true},
	})
	if err != nil {
		t.Fatalf("remote Antigravity runtime_control attach failed: %v", err)
	}
	if probe.SessionID != "probe-antigravity-wsl-live-verify" {
		t.Fatalf("unexpected probe result: %+v", probe)
	}

	// 2. Verify session.models over live SSH.
	commander := adapter.(core.Commander)
	modelsRes, err := commander.Command(ctx, core.Request{
		SessionID: "probe-antigravity-wsl-live-verify",
		Action:    "session.models",
	})
	if err != nil {
		t.Fatalf("remote Antigravity session.models failed: %v", err)
	}
	t.Logf("remote Antigravity models count: %d", len(modelsRes.Payload["items"].([]map[string]any)))

	// 3. Verify astrorder-model-helper auto-install + session.list + missing-workspace resume + session.delete over live SSH.
	if helpersDir != "" {
		querier := adapter.(core.Querier)
		listRes, err := querier.Query(ctx, core.Request{
			Fields: map[string]any{"method": "session.list"},
		})
		if err != nil {
			t.Fatalf("remote Antigravity session.list via SSH helper failed: %v", err)
		}
		t.Logf("remote Antigravity session.list succeeded: %v", listRes["items"] != nil)

		quotaRes, err := querier.Query(ctx, core.Request{
			Fields: map[string]any{"method": "quota.read"},
		})
		if err != nil {
			t.Fatalf("remote Antigravity quota.read failed: %v", err)
		}
		t.Logf("remote Antigravity compliant tier metadata succeeded: provider=%v plan=%v", quotaRes["provider"], quotaRes["plan"])

		if items, ok := listRes["items"].([]map[string]any); ok && len(items) > 0 {
			firstID, _ := items[0]["id"].(string)
			if firstID != "" {
				usageRes, uErr := querier.Query(ctx, core.Request{
					Fields: map[string]any{"method": "session.usage", "conversation_id": firstID},
				})
				if uErr != nil {
					t.Fatalf("remote Antigravity session.usage(%s) failed: %v", firstID, uErr)
				}
				t.Logf("remote Antigravity session.usage(%s): context_available=%v model=%v last_in=%v window=%v",
					firstID, usageRes["context_available"], usageRes["model"], usageRes["last_input_tokens"], usageRes["context_window"])
			}
		}

		rawTmp, err := exec.Command("ssh", "-o", "BatchMode=yes", user+"@"+host, "mktemp -d /tmp/astrorder-agy-del-XXXXXXXX").Output()
		if err != nil {
			t.Fatalf("mktemp remote dir failed: %v", err)
		}
		tmpDir := strings.TrimSpace(string(rawTmp))
		if !strings.HasPrefix(tmpDir, "/tmp/astrorder-agy-del-") {
			t.Fatalf("unexpected temp dir: %q", tmpDir)
		}
		defer func() {
			_ = exec.Command("ssh", "-o", "BatchMode=yes", user+"@"+host, "rm -rf -- "+sshtrans.Quote(tmpDir)).Run()
		}()

		created, err := adapter.Create(ctx, core.Request{
			Fields: map[string]any{"cwd": tmpDir},
		})
		if err != nil || created.SessionID == "" {
			t.Fatalf("remote Antigravity disposable create failed: %v", err)
		}
		t.Logf("created disposable Antigravity session %s in %s", created.SessionID, tmpDir)

		if _, err := commander.Command(ctx, core.Request{
			SessionID: created.SessionID,
			Action:    "session.close",
		}); err != nil {
			t.Fatalf("remote Antigravity session.close failed: %v", err)
		}

		// Delete the session's original workspace directory on the remote host to
		// reproduce deleted temporary workspace paths (e.g. /tmp/magpie-agy-bridge-*).
		if err := exec.Command("ssh", "-o", "BatchMode=yes", user+"@"+host, "rm -rf -- "+sshtrans.Quote(tmpDir)).Run(); err != nil {
			t.Fatalf("remove remote temp workspace failed: %v", err)
		}

		resumed, err := spawner.Spawn(ctx, core.Request{
			SessionID: created.SessionID,
			Fields:    map[string]any{"cwd": tmpDir},
		})
		if err != nil || resumed.SessionID != created.SessionID {
			t.Fatalf("remote Antigravity resume with deleted workspace failed: %v", err)
		}

		delRes, err := commander.Command(ctx, core.Request{
			SessionID: created.SessionID,
			Action:    "session.delete",
		})
		if err != nil || delRes.Payload["deleted"] != created.SessionID {
			t.Fatalf("remote Antigravity session.delete failed: res=%+v err=%v", delRes, err)
		}
		t.Logf("deleted disposable Antigravity session %s after workspace removal", created.SessionID)

		if _, err := spawner.Spawn(ctx, core.Request{
			SessionID: created.SessionID,
			Fields:    map[string]any{"cwd": tmpDir},
		}); err == nil {
			t.Fatalf("deleted Antigravity session %s unexpectedly resumed", created.SessionID)
		}
	}
}
