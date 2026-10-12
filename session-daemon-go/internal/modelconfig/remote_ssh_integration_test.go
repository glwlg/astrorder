//go:build linux

package modelconfig

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// This opt-in fixture owns its SSH server, keys, port and configuration home.
// It never contacts a user-configured SSH host or reads an existing private key.
func TestRemoteModelConfigIsolatedSSH(t *testing.T) {
	if os.Getenv("ASTRORDER_TEST_ISOLATED_SSH") != "1" {
		t.Skip("explicit isolated SSH fixture opt-in required")
	}
	sshd, err := exec.LookPath("sshd")
	if err != nil {
		t.Fatal(err)
	}
	root, home := t.TempDir(), t.TempDir()
	for _, name := range []string{"host", "client"} {
		command := exec.Command("ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-f", filepath.Join(root, name))
		if err := command.Run(); err != nil {
			t.Fatal("fixture key generation failed", err)
		}
	}
	pub, err := os.ReadFile(filepath.Join(root, "client.pub"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "authorized_keys"), pub, 0600); err != nil {
		t.Fatal(err)
	}
	account, err := user.Current()
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	// Force the production-generated command to run with the fixture home.
	wrapper := filepath.Join(root, "forced-command")
	if err := os.WriteFile(wrapper, []byte("#!/bin/sh\nexport HOME="+strconv.Quote(home)+"\nexec sh -c \"$SSH_ORIGINAL_COMMAND\"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	config := fmt.Sprintf("ListenAddress 127.0.0.1\nPort %d\nHostKey %s\nPidFile %s\nAuthorizedKeysFile %s\nStrictModes no\nPasswordAuthentication no\nKbdInteractiveAuthentication no\nUsePAM no\nAllowUsers %s\nForceCommand %s\nLogLevel ERROR\n", port, filepath.Join(root, "host"), filepath.Join(root, "sshd.pid"), filepath.Join(root, "authorized_keys"), account.Username, wrapper)
	configPath := filepath.Join(root, "sshd_config")
	if err := os.WriteFile(configPath, []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(root, "sshd.log")
	log, err := os.Create(logPath)
	if err != nil {
		t.Fatal(err)
	}
	server := exec.Command(sshd, "-D", "-e", "-f", configPath)
	server.Stdout, server.Stderr = log, log
	if err := server.Start(); err != nil {
		log.Close()
		t.Fatal(err)
	}
	defer func() {
		_ = server.Process.Kill()
		_ = server.Wait()
		_ = log.Close()
		if server.ProcessState == nil {
			t.Error("owned isolated SSH server did not exit")
		}
		if err := syscall.Kill(server.Process.Pid, 0); err != syscall.ESRCH {
			t.Errorf("owned isolated SSH process still exists: %v", err)
		}
		probe, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
		if err != nil {
			t.Errorf("owned isolated SSH port was not released: %v", err)
		} else {
			probe.Close()
		}
	}()
	deadline := time.Now().Add(3 * time.Second)
	for {
		conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 50*time.Millisecond)
		if err == nil {
			conn.Close()
			break
		}
		if time.Now().After(deadline) {
			log.Sync()
			data, _ := os.ReadFile(logPath)
			t.Fatalf("isolated SSH server did not start: %s", data)
		}
		time.Sleep(20 * time.Millisecond)
	}
	// Client options are private and never alter the user's known_hosts.
	client := filepath.Join(root, "ssh-client")
	clientScript := "#!/bin/sh\nexec /usr/bin/ssh -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null \"$@\"\n"
	if err := os.WriteFile(client, []byte(clientScript), 0700); err != nil {
		t.Fatal(err)
	}
	assetDir := filepath.Join(root, "helpers", "linux-"+runtime.GOARCH)
	if err := os.MkdirAll(assetDir, 0700); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(buildModelConfigHelper(t))
	if err != nil {
		t.Fatal(err)
	}
	digest := fmt.Sprintf("%x", sha256.Sum256(data))
	if err := os.WriteFile(filepath.Join(assetDir, "astrorder-model-helper"), data, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(assetDir, "SHA256SUMS"), []byte(digest+"  astrorder-model-helper\n"), 0600); err != nil {
		t.Fatal(err)
	}
	svc, err := New(Config{AllowedRoots: []string{t.TempDir()}, HelperAssetsDir: filepath.Join(root, "helpers")})
	if err != nil {
		t.Fatal(err)
	}
	target := map[string]any{"kind": "ssh", "settings": map[string]any{"host": "127.0.0.1", "port": port, "user": account.Username, "identity_file": filepath.Join(root, "client"), "ssh_executable": client}}
	updated := "model = \"isolated-ssh-model\"\n"
	request := map[string]any{"target": target, "files": map[string]any{FileCodexConfig: updated}, "expected_digests": map[string]any{FileCodexConfig: EmptySHA256}}
	preview, err := svc.Execute(context.Background(), "model_config.plan", request)
	if err != nil || len(preview["changes"].([]any)) != 1 {
		t.Fatalf("real SSH preview failed: %v", err)
	}
	path := filepath.Join(home, RelCodexConfig)
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("real SSH preview changed native file")
	}
	_, err = svc.Execute(context.Background(), "model_config.apply", request)
	if err != nil {
		t.Fatal("real SSH apply failed", err)
	}
	if readback, err := os.ReadFile(path); err != nil || string(readback) != updated {
		t.Fatal("real SSH exact-target readback failed", err)
	}
	_, err = svc.Execute(context.Background(), "model_config.apply", request)
	if err == nil || !strings.Contains(err.Error(), "hash mismatch") {
		t.Fatalf("real SSH stale apply accepted: %v", err)
	}
	installed, err := os.ReadFile(filepath.Join(home, ".local/lib/astrorder/model-helper", digest, "astrorder-model-helper"))
	if err != nil || fmt.Sprintf("%x", sha256.Sum256(installed)) != digest {
		t.Fatal("real SSH installed binary differs from bundle", err)
	}
	if entries, err := os.ReadDir(svc.allowedRoots[0]); err != nil || len(entries) != 0 {
		t.Fatal("real SSH operation touched local home", err)
	}
}
