package modelconfig

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"astrorder.dev/session-daemon/internal/transport/ssh"
)

// HelperInvokeError carries structured failure metadata from a remote helper run.
type HelperInvokeError struct {
	Detail         string
	BeforeDispatch bool
}

func (e *HelperInvokeError) Error() string {
	return e.Detail
}

// SSHStreamRunner executes one remote command with optional stdin bytes and
// bounded stdout capture.
type SSHStreamRunner func(ctx context.Context, spec ssh.CommandSpec, stdin []byte, maxStdout int) ([]byte, ssh.ExitStatus, error)

// SSHHelperInstaller stages the hash-verified astrorder-model-helper binary onto
// an SSH host on first use and invokes structured JSON subcommands over stdin/stdout.
type SSHHelperInstaller struct {
	runner          SSHStreamRunner
	helperAssetsDir string

	installMu         sync.Mutex
	mu                sync.Mutex
	installedPath     string
	installedDigest   string
	installedPlatform string
}

// NewSSHHelperInstaller constructs an installer backed by an *ssh.Transport.
func NewSSHHelperInstaller(transport *ssh.Transport, helperAssetsDir string) *SSHHelperInstaller {
	return NewSSHHelperInstallerWithRunner(func(ctx context.Context, spec ssh.CommandSpec, stdin []byte, maxStdout int) ([]byte, ssh.ExitStatus, error) {
		sess, err := transport.Start(ctx, spec)
		if err != nil {
			return nil, ssh.ExitStatus{}, err
		}
		defer sess.Close()

		writeErrCh := make(chan error, 1)
		go func() {
			var wErr error
			if len(stdin) > 0 {
				_, wErr = sess.Write(stdin)
			}
			closeErr := sess.CloseWrite()
			if wErr == nil {
				wErr = closeErr
			}
			writeErrCh <- wErr
		}()

		stdoutBuf := ssh.NewBoundedBuffer(maxStdout)
		_, readErr := io.Copy(stdoutBuf, sess.Stdout())
		status, waitErr := sess.Wait()
		<-writeErrCh

		if stdoutBuf.Truncated() {
			return nil, status, fmt.Errorf("remote helper stdout exceeded limit")
		}
		if readErr != nil && waitErr == nil {
			return stdoutBuf.Bytes(), status, readErr
		}
		return stdoutBuf.Bytes(), status, waitErr
	}, helperAssetsDir)
}

// NewSSHHelperInstallerWithRunner constructs an installer with an explicit runner seam.
func NewSSHHelperInstallerWithRunner(runner SSHStreamRunner, helperAssetsDir string) *SSHHelperInstaller {
	return &SSHHelperInstaller{
		runner:          runner,
		helperAssetsDir: helperAssetsDir,
	}
}

func (i *SSHHelperInstaller) resolveAssetsDir() string {
	if i.helperAssetsDir != "" {
		return i.helperAssetsDir
	}
	executable, err := os.Executable()
	if err != nil {
		return ""
	}
	return filepath.Join(filepath.Dir(executable), "helpers")
}

func (i *SSHHelperInstaller) currentCachedPath(assets string) string {
	i.mu.Lock()
	path, digest, platform := i.installedPath, i.installedDigest, i.installedPlatform
	i.mu.Unlock()
	if path == "" {
		return ""
	}
	if assets != "" && platform != "" && digest != "" {
		manifest, err := os.ReadFile(filepath.Join(assets, platform, "SHA256SUMS"))
		if err != nil || strings.TrimSpace(string(manifest)) != digest+"  astrorder-model-helper" {
			return ""
		}
	}
	return path
}

// Ensure verifies and installs astrorder-model-helper on the remote Linux host,
// caching the content-addressed path for subsequent calls on this connection
// while automatically refreshing if local helper assets are updated on disk.
func (i *SSHHelperInstaller) Ensure(ctx context.Context) (string, error) {
	assets := i.resolveAssetsDir()
	if cached := i.currentCachedPath(assets); cached != "" {
		return cached, nil
	}

	i.installMu.Lock()
	defer i.installMu.Unlock()

	if cached := i.currentCachedPath(assets); cached != "" {
		return cached, nil
	}

	if _, err := os.Stat(assets); os.IsNotExist(err) && i.helperAssetsDir == "" {
		return "astrorder-model-helper", nil
	}

	platformOut, st, err := i.runner(ctx, ssh.CommandSpec{
		Executable: "sh",
		Args:       []string{"-c", modelHelperPlatformScript, "astrorder-model-helper-setup"},
	}, nil, 64*1024)
	if err != nil || !st.ConfirmedRemoteExit || st.ExitCode != 0 {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return "", &HelperInvokeError{Detail: "remote helper platform check failed", BeforeDispatch: true}
	}
	platform := lastOutputLine(platformOut)
	if platform != "linux-amd64" && platform != "linux-arm64" {
		return "", &HelperInvokeError{Detail: "remote helper platform is unavailable", BeforeDispatch: true}
	}

	dir := filepath.Join(assets, platform)
	file, err := os.Open(filepath.Join(dir, "astrorder-model-helper"))
	if err != nil {
		return "", &HelperInvokeError{Detail: fmt.Sprintf("bundled helper is unavailable for %s", platform), BeforeDispatch: true}
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxModelHelperBytes+1))
	if err != nil || len(data) == 0 || len(data) > maxModelHelperBytes {
		return "", &HelperInvokeError{Detail: "bundled helper binary is invalid", BeforeDispatch: true}
	}
	hash := sha256.Sum256(data)
	digest := hex.EncodeToString(hash[:])
	manifest, err := os.ReadFile(filepath.Join(dir, "SHA256SUMS"))
	if err != nil || strings.TrimSpace(string(manifest)) != digest+"  astrorder-model-helper" {
		return "", &HelperInvokeError{Detail: "bundled helper checksum mismatch", BeforeDispatch: true}
	}

	installOut, st, err := i.runner(ctx, ssh.CommandSpec{
		Executable: "sh",
		Args:       []string{"-c", modelHelperInstallScript, "astrorder-model-helper-setup", digest},
	}, data, 64*1024)
	if err != nil || !st.ConfirmedRemoteExit || st.ExitCode != 0 {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return "", &HelperInvokeError{Detail: "remote helper installation failed", BeforeDispatch: true}
	}
	path := lastOutputLine(installOut)
	if !strings.HasPrefix(path, "/") || !strings.HasSuffix(path, "/model-helper/"+digest+"/astrorder-model-helper") || strings.ContainsRune(path, 0) {
		return "", &HelperInvokeError{Detail: "remote helper installation returned invalid path", BeforeDispatch: true}
	}

	i.mu.Lock()
	i.installedPath = path
	i.installedDigest = digest
	i.installedPlatform = platform
	i.mu.Unlock()
	return path, nil
}

// Invoke runs `<helper> [subcommand]` on the remote host with payload serialized
// as JSON on stdin and parses the single JSON envelope on stdout.
func (i *SSHHelperInstaller) Invoke(ctx context.Context, subcommand string, payload map[string]any) (map[string]any, error) {
	reqBytes, err := json.Marshal(payload)
	if err != nil {
		return nil, &HelperInvokeError{Detail: err.Error(), BeforeDispatch: true}
	}

	for attempt := 0; attempt < 2; attempt++ {
		ensureCtx, ensureCancel := context.WithTimeout(ctx, 45*time.Second)
		helperPath, ensureErr := i.Ensure(ensureCtx)
		ensureCancel()
		if ensureErr != nil {
			return nil, ensureErr
		}
		var args []string
		if subcommand != "" {
			args = []string{subcommand}
		}
		subCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		stdoutBytes, st, runErr := i.runner(subCtx, ssh.CommandSpec{
			Executable: helperPath,
			Args:       args,
		}, reqBytes, 32_000_000)
		subErr := subCtx.Err()
		cancel()
		if subErr != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			return nil, subErr
		}
		if attempt == 0 && st.ConfirmedRemoteExit && st.ExitCode == 127 {
			i.mu.Lock()
			if i.installedPath == helperPath {
				i.installedPath = ""
			}
			i.mu.Unlock()
			continue
		}
		trimmed := strings.TrimSpace(string(stdoutBytes))
		if trimmed == "" {
			if runErr != nil {
				return nil, &HelperInvokeError{Detail: "remote helper execution failed", BeforeDispatch: !st.ConfirmedRemoteExit}
			}
			return nil, &HelperInvokeError{Detail: "remote helper returned empty output", BeforeDispatch: false}
		}
		lines := strings.Split(trimmed, "\n")
		lastLine := lines[len(lines)-1]
		var resp map[string]any
		decoder := json.NewDecoder(strings.NewReader(lastLine))
		decoder.UseNumber()
		if err := decoder.Decode(&resp); err != nil {
			return nil, &HelperInvokeError{Detail: "remote helper returned invalid JSON", BeforeDispatch: false}
		}
		if err := decoder.Decode(new(any)); err != io.EOF {
			return nil, &HelperInvokeError{Detail: "remote helper returned trailing data", BeforeDispatch: false}
		}
		ok, _ := resp["ok"].(bool)
		if !ok || runErr != nil || !st.ConfirmedRemoteExit || st.ExitCode != 0 {
			detail, _ := resp["detail"].(string)
			if detail == "" {
				detail = "remote helper execution failed"
			}
			beforeDispatch, _ := resp["before_dispatch"].(bool)
			return nil, &HelperInvokeError{Detail: detail, BeforeDispatch: beforeDispatch}
		}
		result, ok := resp["result"].(map[string]any)
		if !ok {
			return nil, &HelperInvokeError{Detail: "remote helper result is invalid", BeforeDispatch: false}
		}
		return result, nil
	}
	return nil, &HelperInvokeError{Detail: "remote helper binary is unavailable", BeforeDispatch: true}
}
