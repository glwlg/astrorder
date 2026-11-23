package connections

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// RemoteExecutableResolver resolves the absolute POSIX path of a remote agent
// CLI (codex/grok/antigravity) through one read-only SSH probe. It never starts an agent
// process, never submits a prompt, and never fabricates a path: an unresolved
// executable is an explicit error.
type RemoteExecutableResolver interface {
	Resolve(ctx context.Context, kind string, sshConn map[string]any) (string, error)
}

// RuntimeExecutable uses the connection's configured executable or a discovered
// path. An unresolved path stays unset so the daemon can apply its runtime rules.
func RuntimeExecutable(ctx context.Context, resolver RemoteExecutableResolver, kind string, sshConn map[string]any) *string {
	if explicit := explicitRemoteExecutable(kind, sshConn); explicit != "" {
		return &explicit
	}
	if resolver != nil {
		if found, err := resolver.Resolve(ctx, kind, sshConn); err == nil {
			if clean, err := validateRemoteExecutablePath(found); err == nil {
				return &clean
			}
		}
	}
	return nil
}

// RemoteWorkspace selects an explicit POSIX workspace or the SSH connection's
// configured directory. It never substitutes a path from the App's machine.
func RemoteWorkspace(requested, configured string) string {
	if strings.HasPrefix(requested, "/") {
		return requested
	}
	if strings.HasPrefix(configured, "/") {
		return configured
	}
	return ""
}

func IsRemoteAgent(agent map[string]any) bool {
	id, _ := agent["connection_id"].(string)
	return id != "" && id != "local"
}

// NativeRemoteExecutableResolver probes the remote host with a bounded
// OpenSSH command. Only the fixed kind whitelist below is supported.
// Resolved paths are cached briefly per target and concurrent identical probes
// share one SSH call.
type NativeRemoteExecutableResolver struct {
	SSHBinary string

	mu       sync.Mutex
	cache    map[string]cachedExecutablePath
	inFlight map[string]*executableProbeCall
}

type cachedExecutablePath struct {
	path      string
	expiresAt time.Time
}

type executableProbeCall struct {
	done chan struct{}
	path string
	err  error
}

const (
	remoteExecutableProbeTimeout = 6 * time.Second
	remoteExecutableCacheTTL     = 5 * time.Minute
	remoteExecutableStdoutLimit  = 64 << 10
	remoteExecutableStderrLimit  = 8 << 10
	remoteExecutablePathLimit    = 4096
)

// remoteExecutableProbeBody enumerates the installed CLI using only fixed
// constants. The kind is a whitelisted literal; host/user/workspace values are
// never interpolated into the shell source.
const remoteExecutableProbeBody = `p=$(command -v "$k" 2>/dev/null); ` +
	`if [ -z "$p" ]; then for c in "$HOME/.local/bin/$k" "$HOME/bin/$k" "$HOME/.vite-plus/bin/$k"; do if [ -x "$c" ]; then p="$c"; break; fi; done; fi; ` +
	`if [ -z "$p" ] && [ "$k" = codex ]; then for c in "$HOME"/.nvm/versions/node/*/bin/codex; do if [ -x "$c" ]; then p="$c"; fi; done; fi; ` +
	`if [ -z "$p" ] && [ "$k" = hermes ] && [ -x "$HOME/.hermes/hermes-agent/venv/bin/hermes" ]; then p="$HOME/.hermes/hermes-agent/venv/bin/hermes"; fi; ` +
	`if [ -z "$p" ] && [ "$k" = grok ] && [ -x "$HOME/.grok/bin/grok" ]; then p="$HOME/.grok/bin/grok"; fi; ` +
	`if [ -z "$p" ] && [ "$k" = agy ]; then for c in "$HOME/.antigravity/bin/agy" "/usr/local/bin/agy"; do if [ -x "$c" ]; then p="$c"; break; fi; done; fi; ` +
	`if [ -n "$p" ] && [ -x "$p" ]; then case "$p" in /*) printf '%s\n' "$p";; esac; fi`

var remoteExecutableProbeScripts = map[string]string{
	"codex":       `k=codex; ` + remoteExecutableProbeBody,
	"grok":        `k=grok; ` + remoteExecutableProbeBody,
	"antigravity": `k=agy; ` + remoteExecutableProbeBody,
}

// Resolve returns the absolute POSIX path of the remote CLI for one kind.
func (r *NativeRemoteExecutableResolver) Resolve(ctx context.Context, kind string, sshConn map[string]any) (string, error) {
	script, supported := remoteExecutableProbeScripts[kind]
	if !supported {
		return "", fmt.Errorf("remote %s executable resolution is not supported", kind)
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	// An already-resolved explicit absolute path is used directly; it was
	// provided by a previous authoritative resolution, never guessed.
	if explicit := explicitRemoteExecutable(kind, sshConn); explicit != "" {
		return explicit, nil
	}
	target, argv, err := sshProbeTarget(sshConn)
	if err != nil {
		return "", err
	}
	binary := r.SSHBinary
	if binary == "" {
		binary = "ssh"
	}
	cacheKey := kind + "\x00" + binary + "\x00" + target + "\x00" + strings.Join(argv, "\x00")
	r.mu.Lock()
	if entry, ok := r.cache[cacheKey]; ok && time.Now().Before(entry.expiresAt) {
		r.mu.Unlock()
		return entry.path, nil
	}
	if call, ok := r.inFlight[cacheKey]; ok {
		r.mu.Unlock()
		select {
		case <-call.done:
			return call.path, call.err
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
	if r.inFlight == nil {
		r.inFlight = make(map[string]*executableProbeCall)
	}
	call := &executableProbeCall{done: make(chan struct{})}
	r.inFlight[cacheKey] = call
	r.mu.Unlock()

	path, err := r.probe(ctx, kind, binary, target, argv, script)

	r.mu.Lock()
	delete(r.inFlight, cacheKey)
	if err == nil {
		if r.cache == nil {
			r.cache = make(map[string]cachedExecutablePath)
		}
		r.cache[cacheKey] = cachedExecutablePath{
			path:      path,
			expiresAt: time.Now().Add(remoteExecutableCacheTTL),
		}
	}
	call.path = path
	call.err = err
	close(call.done)
	r.mu.Unlock()
	return path, err
}

func (r *NativeRemoteExecutableResolver) probe(ctx context.Context, kind, binary, target string, argv []string, script string) (string, error) {
	cmdCtx, cancel := context.WithTimeout(ctx, remoteExecutableProbeTimeout)
	defer cancel()
	cmd := exec.CommandContext(cmdCtx, binary, append(argv, target, script)...)
	configureChildProcess(cmd)
	stdout := &boundedProbeOutput{limit: remoteExecutableStdoutLimit}
	stderr := &boundedProbeOutput{limit: remoteExecutableStderrLimit}
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	if err := cmd.Run(); err != nil {
		if cmdCtx.Err() != nil {
			return "", cmdCtx.Err()
		}
		return "", fmt.Errorf("remote %s executable probe failed: %w", kind, err)
	}
	if stdout.overflow {
		return "", fmt.Errorf("remote %s executable probe output exceeded its bound", kind)
	}
	path, err := validateRemoteExecutablePath(stdout.buf.String())
	if err != nil {
		return "", fmt.Errorf("remote %s executable probe: %w", kind, err)
	}
	return path, nil
}

// explicitRemoteExecutable returns a validated explicit path carried by the
// connection itself, mirroring parseSSHConfig's settings-then-connection order.
func explicitRemoteExecutable(kind string, sshConn map[string]any) string {
	if sshConn == nil {
		return ""
	}
	settings, _ := sshConn["settings"].(map[string]any)
	for _, source := range []map[string]any{settings, sshConn} {
		if source == nil {
			continue
		}
		value, _ := source[kind+"_executable"].(string)
		if value == "" && kind == "hermes" {
			value, _ = source["hermes_path"].(string)
		}
		if value == "" {
			continue
		}
		if path, err := validateRemoteExecutablePath(value); err == nil {
			return path
		}
	}
	return ""
}

// validateRemoteExecutablePath accepts exactly one absolute POSIX path: no
// NUL/control bytes, no embedded newlines, no oversized or relative output.
func validateRemoteExecutablePath(raw string) (string, error) {
	path := strings.TrimSpace(raw)
	if path == "" {
		return "", errors.New("probe returned no executable path")
	}
	if len(path) > remoteExecutablePathLimit {
		return "", errors.New("probe returned an oversized executable path")
	}
	for _, r := range path {
		if r < 32 || r == 127 {
			return "", errors.New("probe returned a path with control characters")
		}
	}
	if !strings.HasPrefix(path, "/") {
		return "", errors.New("probe returned a non-absolute POSIX path")
	}
	return path, nil
}

// boundedProbeOutput keeps at most limit bytes and records overflow; it always
// accepts the full write so a noisy child can never block on a full pipe.
type boundedProbeOutput struct {
	limit    int
	buf      bytes.Buffer
	overflow bool
}

func (w *boundedProbeOutput) Write(p []byte) (int, error) {
	if !w.overflow {
		remaining := w.limit - w.buf.Len()
		if len(p) <= remaining {
			w.buf.Write(p)
		} else {
			w.buf.Write(p[:remaining])
			w.overflow = true
		}
	}
	return len(p), nil
}

// withResolvedExecutable returns a copy of the stored connection whose
// settings carry the resolved executable for one kind. The stored row is never
// mutated and no new store schema is introduced.
func withResolvedExecutable(sshConn map[string]any, kind, path string) map[string]any {
	settings, _ := sshConn["settings"].(map[string]any)
	if settings == nil {
		settings = sshConn
	}
	merged := make(map[string]any, len(sshConn))
	for key, value := range sshConn {
		merged[key] = value
	}
	settingsCopy := make(map[string]any, len(settings)+1)
	for key, value := range settings {
		settingsCopy[key] = value
	}
	settingsCopy[kind+"_executable"] = path
	merged["settings"] = settingsCopy
	return merged
}
