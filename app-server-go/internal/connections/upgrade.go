package connections

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"astrorder.dev/app-server/internal/envtarget"
)

// UpgradeExecutor executes the native updater and streams its actual output.
type UpgradeExecutor interface {
	Upgrade(context.Context, string, string, map[string]any, func(string), io.Writer) (int, error)
}

type NativeUpgradeExecutor struct{ SSHBinary string }

// Resolve known CLI locations without assuming Windows launchers are .cmd files.
func localAgentExecutable(ctx context.Context, name string) (string, error) {
	if name == "antigravity" {
		name = "agy"
	}
	env, err := (&envtarget.Resolver{}).Open(ctx, "local")
	if err != nil {
		return "", err
	}
	if path, ok := env.Which(ctx, name); ok {
		return path, nil
	}
	if name == "agy" {
		if base := os.Getenv("LOCALAPPDATA"); base != "" {
			p := filepath.Join(base, "agy", "bin", "agy.exe")
			if info, err := os.Stat(p); err == nil && !info.IsDir() {
				return p, nil
			}
		}
	}
	if name == "hermes" {
		if base := os.Getenv("LOCALAPPDATA"); base != "" {
			p := filepath.Join(base, "hermes", "bin", "hermes.exe")
			if info, err := os.Stat(p); err == nil && !info.IsDir() {
				return p, nil
			}
		}
	}
	return "", fmt.Errorf("%s executable not found", name)
}

// Shell source contains only a whitelisted CLI name; target settings remain argv.
func remoteAgentSetup(kind string) string {
	return `k=` + kind + `; { ` + remoteExecutableProbeBody + `; } >/dev/null; ` +
		`if [ -z "$p" ]; then printf '%s\n' "$k executable not found" >&2; exit 127; fi; ` +
		`PATH="$(dirname "$p"):$HOME/.local/bin:$HOME/.hermes/hermes-agent/venv/bin:$HOME/.vite-plus/bin:$HOME/.grok/bin:$PATH"; export PATH; `
}

func (e *NativeUpgradeExecutor) Upgrade(ctx context.Context, kind, connID string, conn map[string]any, init func(string), output io.Writer) (int, error) {
	var args []string
	switch kind {
	case "hermes":
		args = []string{"update", "--yes", "--no-gateway-restart"}
	case "grok", "antigravity":
		args = []string{"update"}
	case "codex":
	default:
		return -1, fmt.Errorf("unsupported agent kind %q", kind)
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Minute)
	defer cancel()
	var cmd *exec.Cmd
	if connID != "" && connID != "local" {
		if conn == nil {
			return -1, errors.New("SSH connection no longer exists")
		}
		target, base, err := sshProbeTarget(conn)
		if err != nil {
			return -1, err
		}
		script := remoteAgentSetup(kind)
		if kind == "codex" {
			script += `if command -v vp >/dev/null 2>&1; then exec vp install -g @openai/codex@latest; elif command -v npm >/dev/null 2>&1; then exec npm install -g @openai/codex@latest; else printf '%s\n' 'Codex upgrade requires vp or npm' >&2; exit 127; fi`
		} else {
			script += `exec "$p" ` + strings.Join(args, " ")
		}
		binary := e.SSHBinary
		if binary == "" {
			binary = "ssh"
		}
		cmd = exec.CommandContext(ctx, binary, append(base, target, script)...)
		init(kind + " update (SSH)")
	} else {
		binary, err := localAgentExecutable(ctx, kind)
		if err != nil {
			return -1, err
		}
		if kind == "codex" {
			binary, err = localAgentExecutable(ctx, "vp")
			if err != nil {
				binary, err = localAgentExecutable(ctx, "npm")
			}
			if err != nil {
				return -1, errors.New("Codex upgrade requires vp or npm")
			}
			args = []string{"install", "-g", "@openai/codex@latest"}
		}
		// Node's .cmd launchers cannot be passed to CreateProcess directly.
		// Run the adjacent JS entrypoint with node instead of shell interpolation.
		if runtime.GOOS == "windows" && strings.HasSuffix(strings.ToLower(binary), ".cmd") {
			if kind != "codex" {
				return -1, fmt.Errorf("unsupported updater launcher %s", binary)
			}
			entry := filepath.Join(filepath.Dir(binary), "node_modules", "npm", "bin", "npm-cli.js")
			if _, err := os.Stat(entry); err != nil {
				return -1, fmt.Errorf("npm CLI entrypoint unavailable: %w", err)
			}
			binary, err = localAgentExecutable(ctx, "node")
			if err != nil {
				return -1, err
			}
			args = append([]string{entry}, args...)
		}
		init(subprocessLabel(binary, args))
		cmd = exec.CommandContext(ctx, binary, args...)
	}
	return runUpgradeProcess(ctx, cmd, output)
}

func subprocessLabel(binary string, args []string) string {
	return fmt.Sprintf("%q %s", binary, strings.Join(args, " "))
}

func runUpgradeProcess(ctx context.Context, cmd *exec.Cmd, output io.Writer) (int, error) {
	configureChildProcess(cmd)
	cmd.Stdout = output
	cmd.Stderr = output
	cmd.WaitDelay = 3 * time.Second
	err := cmd.Run()
	if ctx.Err() != nil {
		return -1, ctx.Err()
	}
	if err == nil {
		return 0, nil
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return exit.ExitCode(), nil
	}
	return -1, err
}

type upgradeStream struct {
	mu       sync.Mutex
	encoder  *json.Encoder
	response http.ResponseWriter
	err      error
}

func (s *upgradeStream) send(frame map[string]any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return
	}
	s.err = s.encoder.Encode(frame)
	if f, ok := s.response.(http.Flusher); ok {
		f.Flush()
	}
}
func (s *upgradeStream) Write(p []byte) (int, error) {
	s.send(map[string]any{"type": "chunk", "data": string(p)})
	return len(p), s.err
}

func (h *Handler) streamAgentUpgrade(w http.ResponseWriter, r *http.Request, kind, connID string, conn map[string]any) {
	w.Header().Set("Content-Type", "application/x-ndjson; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Accel-Buffering", "no")
	// The HTTP server's ordinary write deadline must not truncate long updaters.
	_ = http.NewResponseController(w).SetWriteDeadline(time.Time{})
	stream := &upgradeStream{encoder: json.NewEncoder(w), response: w}
	code, err := h.upgrader.Upgrade(r.Context(), kind, connID, conn, func(command string) { stream.send(map[string]any{"type": "init", "command": command}) }, stream)
	if err != nil {
		stream.send(map[string]any{"type": "error", "error": err.Error()})
	}
	stream.send(map[string]any{"type": "done", "ok": err == nil && code == 0, "exit_code": code})
}
