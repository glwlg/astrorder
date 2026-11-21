package codex

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

func realCodexSSHSettings(t *testing.T) (host, user, executable string) {
	t.Helper()
	host = os.Getenv("ASTRORDER_TEST_SSH_HOST")
	user = os.Getenv("ASTRORDER_TEST_SSH_USER")
	executable = os.Getenv("ASTRORDER_TEST_SSH_CODEX")
	if host == "" || user == "" || !strings.HasPrefix(executable, "/") {
		t.Fatal("ASTRORDER_TEST_SSH_HOST, ASTRORDER_TEST_SSH_USER and an absolute ASTRORDER_TEST_SSH_CODEX are required")
	}
	return
}

func TestRealSSHIdleWakeWithoutSending(t *testing.T) {
	if os.Getenv("ASTRORDER_TEST_EMPTY_SSH") != "1" {
		t.Skip("opt-in real SSH")
	}
	host, user, executable := realCodexSSHSettings(t)
	target := user + "@" + host
	raw, err := exec.Command("ssh", "-o", "BatchMode=yes", target, "mktemp -d /tmp/astrorder-wake-XXXXXXXX").Output()
	if err != nil {
		t.Fatal(err)
	}
	home := strings.TrimSpace(string(raw))
	if !strings.HasPrefix(home, "/tmp/astrorder-wake-") || strings.ContainsAny(home, " '\"\r\n;") {
		t.Fatal("invalid isolated home")
	}
	defer exec.Command("ssh", "-o", "BatchMode=yes", target, "rm -rf -- "+home).Run()
	transport, err := sshtrans.New(sshtrans.Config{Binary: "ssh", Host: host, User: user})
	if err != nil {
		t.Fatal(err)
	}
	a := NewSSHAdapter(Config{Executable: executable, Workspace: home, Allowed: []string{"/tmp"}, AgentID: "wake-real"}, nil, func(ctx context.Context, spec sshtrans.CommandSpec) (SSHSession, error) {
		spec.Executable = "/usr/bin/env"
		spec.Args = append([]string{"CODEX_HOME=" + home, executable}, spec.Args...)
		return transport.Start(ctx, spec)
	})
	defer a.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	sid := os.Getenv("ASTRORDER_WAKE_SOURCE_SESSION")
	if sid == "" {
		t.Skip("ASTRORDER_WAKE_SOURCE_SESSION is required")
	}
	if strings.ContainsAny(sid, " '\"\r\n;/") {
		t.Fatal("invalid source session identity")
	}
	copyScript := `import pathlib, shutil, sys
source = pathlib.Path.home() / '.codex' / 'sessions'
session = next(source.rglob('*' + sys.argv[1] + '*.jsonl'))
fixture = pathlib.Path(sys.argv[2])
destination = fixture / 'sessions' / session.relative_to(source)
destination.parent.mkdir(parents=True, exist_ok=True)
shutil.copyfile(session, destination)
(fixture / 'config.toml').write_text('[model_providers.magpie]\nname="fixture"\nbase_url="http://127.0.0.1:1"\nwire_api="responses"\n')
`
	copyCommand := "python3 -c " + sshtrans.Quote(copyScript) + " " + sshtrans.Quote(sid) + " " + sshtrans.Quote(home)
	if output, copyErr := exec.Command("ssh", "-o", "BatchMode=yes", target, copyCommand).CombinedOutput(); copyErr != nil {
		t.Fatalf("copy fixture: %v %s", copyErr, output)
	}
	created, err := a.Spawn(ctx, core.Request{SessionID: sid, Fields: map[string]any{"cwd": home}})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if _, err = a.Command(ctx, core.Request{SessionID: created.SessionID, Action: "session.close"}); err != nil {
			t.Fatalf("close %d: %v", i, err)
		}
		if _, err = a.Spawn(ctx, core.Request{SessionID: created.SessionID, Fields: map[string]any{"cwd": home}}); err != nil {
			t.Fatalf("wake %d: %v", i, err)
		}
		if _, err = a.Command(ctx, core.Request{SessionID: created.SessionID, Action: "session.model.read"}); err != nil {
			t.Fatalf("model %d: %v", i, err)
		}
	}
}
