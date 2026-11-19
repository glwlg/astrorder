package remote

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	core "astrorder.dev/session-daemon/internal/runtime"
)

func TestParseSSHConfig(t *testing.T) {
	settings := map[string]any{
		"host": "test.example",
		"user": "operator",
		"port": 2222.0,
	}
	cfg, err := ParseSSHConfig(settings)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Host != "test.example" || cfg.User != "operator" || cfg.Port != 2222 {
		t.Fatalf("unexpected parsed config: %+v", cfg)
	}

	// Host required
	_, err = ParseSSHConfig(map[string]any{"port": 22})
	if err == nil {
		t.Fatalf("expected error for missing host")
	}
}

func TestRemoteFactoriesValidateExecutable(t *testing.T) {
	emit := func(string, string, map[string]any, string) error { return nil }
	codexFactory := NewRemoteCodexFactory(emit)

	// Missing codex_executable
	_, err := codexFactory("conn-1", map[string]any{"host": "test.example"})
	if err == nil {
		t.Fatalf("expected error for missing codex_executable")
	}

	// Relative path
	_, err = codexFactory("conn-1", map[string]any{"host": "test.example", "codex_executable": "bin/codex"})
	if err == nil {
		t.Fatalf("expected error for relative codex_executable")
	}

	grokFactory := NewRemoteGrokFactory(emit)
	_, err = grokFactory("conn-1", map[string]any{"host": "test.example", "grok_executable": "bin/grok"})
	if err == nil {
		t.Fatalf("expected error for relative grok_executable")
	}

	// Hermes factory validates host
	hermesFactory := NewRemoteHermesFactory(emit)
	_, err = hermesFactory("conn-1", map[string]any{"host": ""})
	if err == nil {
		t.Fatalf("expected error for missing host in hermesFactory")
	}

	// Hermes factory constructs valid adapter
	adapter, err := hermesFactory("conn-1", map[string]any{"host": "test.example"})
	if err != nil {
		t.Fatalf("unexpected error creating hermes adapter: %v", err)
	}
	if adapter == nil {
		t.Fatalf("expected adapter, got nil")
	}

	// Antigravity factory validates antigravity_executable and constructs valid adapter
	agyFactory := NewRemoteAntigravityFactory(emit, "")
	if _, err := agyFactory("conn-1", map[string]any{"host": "test.example"}); err == nil {
		t.Fatalf("expected error for missing antigravity_executable")
	}
	if _, err := agyFactory("conn-1", map[string]any{"host": "test.example", "antigravity_executable": "bin/agy"}); err == nil {
		t.Fatalf("expected error for relative antigravity_executable")
	}
	if _, err := agyFactory("conn-1", map[string]any{"host": "test.example", "antigravity_executable": "/bin/agy\x00bad"}); err == nil {
		t.Fatalf("expected error for NUL in antigravity_executable")
	}
	agyAdapter, err := agyFactory("conn-1", map[string]any{
		"host":                   "test.example",
		"antigravity_executable": "/home/luwei/.local/bin/agy",
		"display_name":           "WSL Ubuntu",
		"workspace":              "/home/luwei/workspace",
	})
	if err != nil || agyAdapter == nil {
		t.Fatalf("unexpected error creating antigravity-ssh adapter: %v", err)
	}
}

func TestRemoteAntigravityFactorySpawnsThroughRealSSHTransport(t *testing.T) {
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("POSIX sh unavailable")
	}
	binDir := t.TempDir()
	fakeAgy := filepath.Join(binDir, "agy")
	agyScript := "#!/bin/sh\n" +
		"for arg in \"$@\"; do\n" +
		"  if [ \"$arg\" = \"models\" ]; then\n" +
		"    printf 'gemini-3.8-pro\\tGemini 3.8 Pro\\n'\n" +
		"    exit 0\n" +
		"  fi\n" +
		"done\n" +
		"while IFS= read -r line; do\n" +
		"  case \"$line\" in\n" +
		"    *'\"method\":\"initialize\"'*)\n" +
		"      printf '{\"jsonrpc\":\"2.0\",\"id\":1,\"result\":{\"protocolVersion\":1,\"agentCapabilities\":{\"loadSession\":true,\"promptCapabilities\":{\"image\":true},\"sessionCapabilities\":{\"resume\":{},\"close\":{}}}}}\\n'\n" +
		"      ;;\n" +
		"    *'\"method\":\"session/new\"'*)\n" +
		"      printf '{\"jsonrpc\":\"2.0\",\"id\":2,\"result\":{\"sessionId\":\"11111111-1111-4111-8111-111111111111\",\"models\":{\"currentModelId\":\"gemini-3.8-pro\",\"availableModels\":[{\"modelId\":\"gemini-3.8-pro\",\"name\":\"Gemini 3.8 Pro\"}]},\"modes\":{\"currentModeId\":\"default\"}}}\\n'\n" +
		"      ;;\n" +
		"    *'\"method\":\"session/close\"'*)\n" +
		"      printf '{\"jsonrpc\":\"2.0\",\"id\":3,\"result\":{}}\\n'\n" +
		"      ;;\n" +
		"  esac\n" +
		"done\n" +
		"exit 0\n"
	if err := os.WriteFile(fakeAgy, []byte(agyScript), 0o755); err != nil {
		t.Fatal(err)
	}
	fakeSSH := filepath.Join(binDir, "ssh")
	// Execute the last argument (remote shell command) via local POSIX sh.
	sshScript := "#!" + sh + "\n" +
		"last=\"\"\n" +
		"for a in \"$@\"; do last=\"$a\"; done\n" +
		"exec " + sh + " -c \"$last\"\n"
	if err := os.WriteFile(fakeSSH, []byte(sshScript), 0o755); err != nil {
		t.Fatal(err)
	}

	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("MAGPIE_API_KEY", "test-gateway-key")

	emit := func(string, string, map[string]any, string) error { return nil }
	factory := NewRemoteAntigravityFactory(emit, "")
	adapter, err := factory("wsl", map[string]any{
		"host":                   "127.0.0.1",
		"user":                   "tester",
		"port":                   22,
		"antigravity_executable": fakeAgy,
		"workspace":              binDir,
	})
	if err != nil {
		t.Fatalf("factory error: %v", err)
	}
	spawner, ok := adapter.(core.Spawner)
	if !ok {
		t.Fatal("adapter does not implement Spawner")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// 1. Runtime control probe ("接入") must execute real ssh.Transport without provider env errors.
	probeRes, err := spawner.Spawn(ctx, core.Request{
		SessionID: "probe-antigravity-wsl",
		Fields:    map[string]any{"runtime_control": true},
	})
	if err != nil {
		t.Fatalf("runtime_control Spawn over real ssh.Transport failed: %v", err)
	}
	if probeRes.SessionID != "probe-antigravity-wsl" {
		t.Fatalf("unexpected probe result: %+v", probeRes)
	}

	// 2. Create session + graceful close must also succeed over real ssh.Transport.
	created, err := adapter.Create(ctx, core.Request{
		Fields: map[string]any{"cwd": binDir},
	})
	if err != nil || created.SessionID != "11111111-1111-4111-8111-111111111111" {
		t.Fatalf("Create over real ssh.Transport failed: res=%+v err=%v", created, err)
	}
	commander := adapter.(core.Commander)
	closed, err := commander.Command(ctx, core.Request{
		SessionID: created.SessionID,
		Action:    "session.close",
	})
	if err != nil || closed.Payload["closed"] != true {
		t.Fatalf("session.close over real ssh.Transport failed: res=%+v err=%v", closed, err)
	}
}
