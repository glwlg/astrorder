package modelconfig

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"astrorder.dev/session-daemon/internal/transport/ssh"
)

func TestSSHHelperInstallerCachesAndRecoversMissingBinary(t *testing.T) {
	assets := t.TempDir()
	linuxDir := filepath.Join(assets, "linux-amd64")
	if err := os.MkdirAll(linuxDir, 0o700); err != nil {
		t.Fatal(err)
	}
	binaryBytes := []byte("#!/bin/sh\necho fake-helper\n")
	sum := sha256.Sum256(binaryBytes)
	digest := hex.EncodeToString(sum[:])
	if err := os.WriteFile(filepath.Join(linuxDir, "astrorder-model-helper"), binaryBytes, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(linuxDir, "SHA256SUMS"), []byte(digest+"  astrorder-model-helper\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	remotePath := "/home/fixture/.local/lib/astrorder/model-helper/" + digest + "/astrorder-model-helper"
	installCount := 0
	invokeCount := 0
	failNext127 := false

	installer := NewSSHHelperInstallerWithRunner(func(_ context.Context, spec ssh.CommandSpec, stdin []byte, _ int) ([]byte, ssh.ExitStatus, error) {
		if spec.Executable == "sh" {
			if len(spec.Args) == 3 {
				return []byte("linux-amd64\n"), ssh.ExitStatus{ConfirmedRemoteExit: true, ExitCode: 0}, nil
			}
			installCount++
			if string(stdin) != string(binaryBytes) {
				t.Fatalf("unexpected staged binary bytes len=%d", len(stdin))
			}
			return []byte(remotePath + "\n"), ssh.ExitStatus{ConfirmedRemoteExit: true, ExitCode: 0}, nil
		}
		if spec.Executable != remotePath || len(spec.Args) != 1 || spec.Args[0] != "antigravity" {
			t.Fatalf("unexpected invoke spec: %+v", spec)
		}
		invokeCount++
		if failNext127 {
			failNext127 = false
			return nil, ssh.ExitStatus{ConfirmedRemoteExit: true, ExitCode: 127}, errors.New("exit 127")
		}
		if strings.Contains(string(stdin), `"reject"`) {
			return []byte(`{"ok":false,"detail":"listener missing","before_dispatch":true}`), ssh.ExitStatus{ConfirmedRemoteExit: true, ExitCode: 2}, errors.New("exit 2")
		}
		return []byte(`{"ok":true,"result":{"count":42}}`), ssh.ExitStatus{ConfirmedRemoteExit: true, ExitCode: 0}, nil
	}, assets)

	ctx := context.Background()
	res1, err := installer.Invoke(ctx, "antigravity", map[string]any{"action": "ping"})
	if err != nil || res1 == nil {
		t.Fatalf("first invoke: %+v %v", res1, err)
	}
	res2, err := installer.Invoke(ctx, "antigravity", map[string]any{"action": "ping"})
	if err != nil || res2 == nil {
		t.Fatalf("second invoke: %+v %v", res2, err)
	}
	if installCount != 1 || invokeCount != 2 {
		t.Fatalf("expected cached helper path (install=1, invoke=2), got install=%d invoke=%d", installCount, invokeCount)
	}

	// Simulate remote helper deletion (exit 127) -> must re-stage once automatically.
	failNext127 = true
	if _, err := installer.Invoke(ctx, "antigravity", map[string]any{"action": "ping"}); err != nil {
		t.Fatalf("invoke after 127 recovery: %v", err)
	}
	if installCount != 2 {
		t.Fatalf("expected re-install after exit 127, got installCount=%d", installCount)
	}

	// Verify BeforeDispatch error metadata is preserved.
	_, err = installer.Invoke(ctx, "antigravity", map[string]any{"action": "reject"})
	var hie *HelperInvokeError
	if !errors.As(err, &hie) || !hie.BeforeDispatch || hie.Detail != "listener missing" {
		t.Fatalf("expected HelperInvokeError with BeforeDispatch=true, got %#v", err)
	}

	// Simulate hot-updating local helper binary & SHA256SUMS without restarting sessiond.
	binaryBytes = []byte("#!/bin/sh\necho fake-helper-v2\n")
	sum2 := sha256.Sum256(binaryBytes)
	digest = hex.EncodeToString(sum2[:])
	remotePath = "/home/fixture/.local/lib/astrorder/model-helper/" + digest + "/astrorder-model-helper"
	if err := os.WriteFile(filepath.Join(linuxDir, "astrorder-model-helper"), binaryBytes, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(linuxDir, "SHA256SUMS"), []byte(digest+"  astrorder-model-helper\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := installer.Invoke(ctx, "antigravity", map[string]any{"action": "ping"}); err != nil {
		t.Fatalf("invoke after local helper asset hot-update: %v", err)
	}
	if installCount != 3 {
		t.Fatalf("expected automatic re-stage after SHA256SUMS update, got installCount=%d", installCount)
	}
}

func TestSSHHelperInstallerRejectsChecksumMismatch(t *testing.T) {
	assets := t.TempDir()
	linuxDir := filepath.Join(assets, "linux-amd64")
	if err := os.MkdirAll(linuxDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(linuxDir, "astrorder-model-helper"), []byte("binary"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(linuxDir, "SHA256SUMS"), []byte("0000  astrorder-model-helper\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	installer := NewSSHHelperInstallerWithRunner(func(_ context.Context, spec ssh.CommandSpec, _ []byte, _ int) ([]byte, ssh.ExitStatus, error) {
		return []byte("linux-amd64\n"), ssh.ExitStatus{ConfirmedRemoteExit: true, ExitCode: 0}, nil
	}, assets)
	if _, err := installer.Ensure(context.Background()); err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("expected checksum mismatch error, got %v", err)
	}
}
