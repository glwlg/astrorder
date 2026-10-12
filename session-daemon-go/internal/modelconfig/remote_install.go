package modelconfig

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"strings"

	"astrorder.dev/session-daemon/internal/process"
	"astrorder.dev/session-daemon/internal/transport/ssh"
)

const maxModelHelperBytes = 64 * 1024 * 1024

const modelHelperPlatformScript = `set -eu
case "$(uname -s)" in Linux) ;; *) exit 1 ;; esac
case "$(uname -m)" in x86_64|amd64) platform=linux-amd64 ;; aarch64|arm64) platform=linux-arm64 ;; *) exit 1 ;; esac
printf '%s\n' "$platform"`

// The helper is installed under its content hash. Existing versions are never
// replaced in place, and the requested bytes are checked before publication.
const modelHelperInstallScript = `set -eu
umask 077
base="$HOME/.local/lib/astrorder/model-helper"
test ! -L "$HOME/.local"
test ! -L "$HOME/.local/lib"
test ! -L "$HOME/.local/lib/astrorder"
test ! -L "$base"
mkdir -p -- "$base"
final="$base/$1"
test ! -L "$final"
if test -x "$final/astrorder-model-helper" && test ! -L "$final/astrorder-model-helper" && test "$(sha256sum "$final/astrorder-model-helper" | cut -d ' ' -f 1)" = "$1"; then
  cat > /dev/null
  printf '%s\n' "$final/astrorder-model-helper"
  exit 0
fi
test ! -e "$final"
stage=$(mktemp -d "$base/.install.XXXXXXXX")
trap 'rm -rf -- "$stage"' EXIT HUP INT TERM
cat > "$stage/astrorder-model-helper"
test "$(sha256sum "$stage/astrorder-model-helper" | cut -d ' ' -f 1)" = "$1"
chmod 700 "$stage/astrorder-model-helper"
if mv -T -- "$stage" "$final" 2>/dev/null; then :; else
  test -x "$final/astrorder-model-helper"
  test ! -L "$final/astrorder-model-helper"
  test "$(sha256sum "$final/astrorder-model-helper" | cut -d ' ' -f 1)" = "$1"
fi
printf '%s\n' "$final/astrorder-model-helper"`

func (s *Service) helperAssetsDirectory() string {
	if s.config.HelperAssetsDir != "" {
		return s.config.HelperAssetsDir
	}
	executable, err := os.Executable()
	if err != nil {
		return ""
	}
	return filepath.Join(filepath.Dir(executable), "helpers")
}

func (s *Service) ensureRemoteHelper(ctx context.Context, target map[string]any) (string, error) {
	assets := s.helperAssetsDirectory()
	if _, err := os.Stat(assets); os.IsNotExist(err) && s.config.HelperAssetsDir == "" {
		// Existing installations can use their preinstalled helper; bundled
		// candidates always select and verify their shipped target binary.
		return "astrorder-model-helper", nil
	}
	platformOutput, err := s.runHelperSetup(ctx, target, nil, modelHelperPlatformScript)
	if err != nil {
		return "", err
	}
	platform := lastOutputLine(platformOutput)
	if platform != "linux-amd64" && platform != "linux-arm64" {
		return "", newProtocolErrorf("remote model helper platform is unavailable")
	}
	dir := filepath.Join(assets, platform)
	file, err := os.Open(filepath.Join(dir, "astrorder-model-helper"))
	if err != nil {
		return "", newProtocolErrorf("bundled model helper is unavailable for %s", platform)
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxModelHelperBytes+1))
	if err != nil || len(data) == 0 || len(data) > maxModelHelperBytes {
		return "", newProtocolErrorf("bundled model helper is invalid")
	}
	hash := sha256.Sum256(data)
	digest := hex.EncodeToString(hash[:])
	manifest, err := os.ReadFile(filepath.Join(dir, "SHA256SUMS"))
	if err != nil || strings.TrimSpace(string(manifest)) != digest+"  astrorder-model-helper" {
		return "", newProtocolErrorf("bundled model helper checksum mismatch")
	}
	installed, err := s.runHelperSetup(ctx, target, data, modelHelperInstallScript, digest)
	if err != nil {
		return "", err
	}
	path := lastOutputLine(installed)
	if !strings.HasPrefix(path, "/") || !strings.HasSuffix(path, "/model-helper/"+digest+"/astrorder-model-helper") || strings.ContainsRune(path, 0) {
		return "", newProtocolErrorf("remote model helper installation returned invalid path")
	}
	return path, nil
}

func lastOutputLine(output []byte) string {
	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	return strings.TrimSpace(lines[len(lines)-1])
}

func (s *Service) runHelperSetup(ctx context.Context, target map[string]any, input []byte, script string, args ...string) ([]byte, error) {
	arguments := append([]string{"-c", script, "astrorder-model-helper-setup"}, args...)
	cmd, err := s.remoteCommand(ctx, target, "sh", arguments...)
	if err != nil {
		return nil, err
	}
	cmd.Stdin = bytes.NewReader(input)
	cmd.Env = process.NativeEnvironment()
	prepareCmdSysProcAttr(cmd)
	stdout, stderr := ssh.NewBoundedBuffer(64*1024), ssh.NewBoundedBuffer(64*1024)
	cmd.Stdout, cmd.Stderr = stdout, stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, newProtocolErrorf("remote model helper installation failed")
	}
	if stdout.Truncated() {
		return nil, newProtocolErrorf("remote model helper installation output exceeds limit")
	}
	return stdout.Bytes(), nil
}
