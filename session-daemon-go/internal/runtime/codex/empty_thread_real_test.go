package codex

import (
	core "astrorder.dev/session-daemon/internal/runtime"
	sshtrans "astrorder.dev/session-daemon/internal/transport/ssh"
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestRealCodexNewEmptyHistoryWithoutSending(t *testing.T) {
	executable := os.Getenv("ASTRORDER_REAL_CODEX")
	if executable == "" {
		t.Skip("ASTRORDER_REAL_CODEX is not configured")
	}
	workspace, home := t.TempDir(), t.TempDir()
	a := NewAdapter(Config{Executable: executable, Workspace: workspace, Allowed: []string{workspace}, AgentID: "empty-real", Environment: []string{"CODEX_HOME=" + home}}, nil)
	defer a.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	created, err := a.Create(ctx, core.Request{Fields: map[string]any{"cwd": workspace}})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		for _, action := range []string{"session.history_page", "session.processes"} {
			result, err := a.Command(ctx, core.Request{SessionID: created.SessionID, Action: action, Fields: map[string]any{"limit": 50}})
			if err != nil {
				t.Fatalf("%s: %v", action, err)
			}
			key := "items"
			if action == "session.processes" {
				key = "processes"
			}
			rows, ok := result.Payload[key].([]any)
			if !ok || len(rows) != 0 {
				t.Fatal(result)
			}
		}
	}
	t.Log("real Codex thread/start followed by six immediate empty reads; no turn/start or model request")
}

func TestRealSSHNewEmptyHistoryWithoutSending(t *testing.T) {
	if os.Getenv("ASTRORDER_TEST_EMPTY_SSH") != "1" {
		t.Skip("ASTRORDER_TEST_EMPTY_SSH is not configured")
	}
	target := "luwei@127.0.0.1"
	raw, err := exec.Command("ssh", "-o", "BatchMode=yes", target, "mktemp -d /tmp/astrorder-empty-XXXXXXXX").Output()
	if err != nil {
		t.Fatal(err)
	}
	home := strings.TrimSpace(string(raw))
	if !strings.HasPrefix(home, "/tmp/astrorder-empty-") || strings.ContainsAny(home, " '\"\r\n;") {
		t.Fatal("invalid isolated home")
	}
	defer func() {
		if err := exec.Command("ssh", "-o", "BatchMode=yes", target, "rm -rf -- "+home).Run(); err != nil {
			t.Error(err)
		}
	}()
	transport, err := sshtrans.New(sshtrans.Config{Binary: "ssh", Host: "127.0.0.1", User: "luwei"})
	if err != nil {
		t.Fatal(err)
	}
	a := NewSSHAdapter(Config{Executable: "/home/luwei/.vite-plus/bin/codex", Workspace: home, Allowed: []string{"/tmp"}, AgentID: "empty-ssh-real"}, nil, func(ctx context.Context, spec sshtrans.CommandSpec) (SSHSession, error) {
		spec.Environment = nil
		spec.Executable = "/usr/bin/env"
		spec.Args = append([]string{"CODEX_HOME=" + home, "/home/luwei/.vite-plus/bin/codex"}, spec.Args...)
		return transport.Start(ctx, spec)
	})
	defer a.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	created, err := a.Create(ctx, core.Request{Fields: map[string]any{"cwd": home}})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		for _, action := range []string{"session.history_page", "session.processes"} {
			result, err := a.Command(ctx, core.Request{SessionID: created.SessionID, Action: action, Fields: map[string]any{"limit": 50}})
			if err != nil {
				t.Fatalf("%s: %v", action, err)
			}
			key := "items"
			if action == "session.processes" {
				key = "processes"
			}
			rows, ok := result.Payload[key].([]any)
			if !ok || len(rows) != 0 {
				t.Fatal(result)
			}
		}
	}
	t.Log("real SSH Codex create and six immediate empty reads; isolated home, no input")
}
