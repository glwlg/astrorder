package hermes

import (
	"astrorder.dev/session-daemon/internal/transport/ssh"
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestHistoryDBPathUsesWindowsDataRoot(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows platform default")
	}
	t.Setenv("HERMES_HOME", "")
	t.Setenv("HERMES_DATA_DIR_SUFFIX", "-history-test")
	root := t.TempDir()
	t.Setenv("LOCALAPPDATA", root)
	home := filepath.Join(root, "hermes-history-test")
	if err := os.MkdirAll(home, 0700); err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(home, "state.db")
	if err := os.WriteFile(want, nil, 0600); err != nil {
		t.Fatal(err)
	}
	a := New(Config{})
	defer a.Close()
	if got := a.historyDBPath(); got != want {
		t.Fatalf("path=%s want=%s", got, want)
	}
}

func TestRemoteHistoryNeverReadsLocalDB(t *testing.T) {
	a := New(Config{StateDBPath: filepath.Join(t.TempDir(), "state.db")})
	defer a.Close()
	a.sshStarter = func(context.Context, ssh.CommandSpec) (SSHSession, error) {
		t.Fatal("unexpected SSH launch")
		return nil, nil
	}
	if got := a.historyDBPath(); got != "" {
		t.Fatalf("remote history resolved local path %s", got)
	}
}
