package targetfs

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	maxRemoteHelperBytes = 64 * 1024 * 1024
	helperSetupTimeout   = 60 * time.Second
	helperBinaryName     = "astrorder-helper"
)

const helperPlatformScript = `set -eu
case "$(uname -s)" in Linux) ;; *) exit 1 ;; esac
case "$(uname -m)" in x86_64|amd64) platform=linux-amd64 ;; aarch64|arm64) platform=linux-arm64 ;; *) exit 1 ;; esac
printf '%s\n' "$platform"`

// The helper is installed under its content hash and never replaced in place.
const helperInstallScript = `set -eu
umask 077
base="$HOME/.local/lib/astrorder/helper"
test ! -L "$HOME/.local"
test ! -L "$HOME/.local/lib"
test ! -L "$HOME/.local/lib/astrorder"
test ! -L "$base"
mkdir -p -- "$base"
final="$base/$1"
test ! -L "$final"
if test -x "$final/astrorder-helper" && test ! -L "$final/astrorder-helper" && test "$(sha256sum "$final/astrorder-helper" | cut -d ' ' -f 1)" = "$1"; then
  cat > /dev/null
  printf '%s\n' "$final/astrorder-helper"
  exit 0
fi
test ! -e "$final"
stage=$(mktemp -d "$base/.install.XXXXXXXX")
trap 'rm -rf -- "$stage"' EXIT HUP INT TERM
cat > "$stage/astrorder-helper"
test "$(sha256sum "$stage/astrorder-helper" | cut -d ' ' -f 1)" = "$1"
chmod 700 "$stage/astrorder-helper"
if mv -T -- "$stage" "$final" 2>/dev/null; then :; else
  test -x "$final/astrorder-helper"
  test ! -L "$final/astrorder-helper"
  test "$(sha256sum "$final/astrorder-helper" | cut -d ' ' -f 1)" = "$1"
fi
printf '%s\n' "$final/astrorder-helper"`

// helperInstaller copies the bundled Linux astrorder-helper to a remote target
// on first use and remembers the installed path for the process lifetime.
type helperInstaller struct {
	assets string
	mu     sync.Mutex
	paths  map[string]string
}

func newHelperInstaller(assets string) *helperInstaller {
	return &helperInstaller{assets: assets, paths: map[string]string{}}
}

// shellRunner builds the local command that runs `sh -c script name args...`
// on the remote target.
type shellRunner func(ctx context.Context, script string, args ...string) (*exec.Cmd, error)

func (i *helperInstaller) ensure(ctx context.Context, key string, run shellRunner) (string, error) {
	i.mu.Lock()
	defer i.mu.Unlock()
	if path := i.paths[key]; path != "" {
		return path, nil
	}
	ctx, cancel := context.WithTimeout(ctx, helperSetupTimeout)
	defer cancel()
	output, err := runShell(ctx, run, nil, helperPlatformScript)
	if err != nil {
		return "", err
	}
	platform := lastLine(output)
	if platform != "linux-amd64" && platform != "linux-arm64" {
		return "", errorf(503, "helper_platform_unsupported", "Remote host platform is not supported", nil)
	}
	file, err := os.Open(filepath.Join(i.assets, platform, helperBinaryName))
	if err != nil {
		return "", errorf(503, "helper_asset_missing", "Bundled helper is unavailable for "+platform, err)
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxRemoteHelperBytes+1))
	if err != nil || len(data) == 0 || len(data) > maxRemoteHelperBytes {
		return "", errorf(503, "helper_asset_invalid", "Bundled helper is invalid", err)
	}
	sum := sha256.Sum256(data)
	digest := hex.EncodeToString(sum[:])
	installed, err := runShell(ctx, run, data, helperInstallScript, digest)
	if err != nil {
		return "", err
	}
	path := lastLine(installed)
	if !strings.HasPrefix(path, "/") || !strings.HasSuffix(path, "/helper/"+digest+"/"+helperBinaryName) || strings.ContainsRune(path, 0) {
		return "", errorf(503, "helper_install_invalid", "Remote helper installation returned an invalid path", nil)
	}
	i.paths[key] = path
	return path, nil
}

func runShell(ctx context.Context, run shellRunner, input []byte, script string, args ...string) ([]byte, error) {
	cmd, err := run(ctx, script, args...)
	if err != nil {
		return nil, err
	}
	cmd.Stdin = bytes.NewReader(input)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &limitedWriter{buffer: &stdout, limit: 64 * 1024}, &limitedWriter{buffer: &stderr, limit: 64 * 1024}
	configureChildProcess(cmd)
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return nil, errorf(503, "helper_install_timeout", "Remote helper installation timed out", ctx.Err())
		}
		return nil, errorf(503, "helper_install_failed", "Remote helper installation failed", err)
	}
	return stdout.Bytes(), nil
}

type limitedWriter struct {
	buffer *bytes.Buffer
	limit  int
}

func (w *limitedWriter) Write(p []byte) (int, error) {
	if room := w.limit - w.buffer.Len(); room > 0 {
		if len(p) < room {
			room = len(p)
		}
		w.buffer.Write(p[:room])
	}
	return len(p), nil
}

func lastLine(output []byte) string {
	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	return strings.TrimSpace(lines[len(lines)-1])
}

func sshShellRunner(config SSHConfig) shellRunner {
	return func(ctx context.Context, script string, args ...string) (*exec.Cmd, error) {
		config.HelperPath = "/bin/sh"
		argv, err := BuildSSHCommand(config)
		if err != nil {
			return nil, err
		}
		parts := []string{"sh", "-c", quotePOSIX(script), "astrorder-helper-setup"}
		for _, arg := range args {
			parts = append(parts, quotePOSIX(arg))
		}
		argv[len(argv)-1] = strings.Join(parts, " ")
		return exec.CommandContext(ctx, argv[0], argv[1:]...), nil
	}
}

func wslShellRunner(config WSLConfig) shellRunner {
	return func(ctx context.Context, script string, args ...string) (*exec.Cmd, error) {
		config.HelperPath = "/bin/sh"
		argv, err := BuildWSLCommand(config)
		if err != nil {
			return nil, err
		}
		// wsl.exe -d <distro> -- /bin/sh  → append the shell script and arguments.
		base := argv[:len(argv)-3]
		full := append(append([]string{}, base...), "-c", script, "astrorder-helper-setup")
		full = append(full, args...)
		return exec.CommandContext(ctx, full[0], full[1:]...), nil
	}
}
