package observe

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

// InstallOptions configures InstallHooks. Every field has a host-local default,
// so the same call works on Windows, WSL and SSH hosts.
type InstallOptions struct {
	// CodexHome defaults to $CODEX_HOME, then ~/.codex.
	CodexHome string
	// Spool defaults to <CodexHome>/astrorder-observer/events.
	Spool string
	// Helper is the helper binary the hooks will run; it defaults to this process.
	Helper string
	// Codex is the Codex executable used to record trust; it is discovered when empty.
	Codex string
	// Trust asks Codex to record trust for the hooks it just rewrote.
	Trust bool
}

// InstallResult is what `astrorder-helper install-hooks` prints.
type InstallResult struct {
	OK         bool          `json:"ok"`
	Error      string        `json:"error,omitempty"`
	Command    string        `json:"command,omitempty"`
	Rewrite    RewriteResult `json:"rewrite"`
	Trust      *TrustResult  `json:"trust,omitempty"`
	TrustError string        `json:"trust_error,omitempty"`
}

// InstallHooks points Astrorder's Codex hooks at the helper and, when asked,
// has Codex trust them. Rewriting is idempotent; a trust failure is reported
// but does not undo the rewrite, so the hooks stay current and Codex's own
// review prompt remains the fallback.
func InstallHooks(ctx context.Context, opts InstallOptions) InstallResult {
	home := opts.CodexHome
	if home == "" {
		home = os.Getenv("CODEX_HOME")
	}
	if home == "" {
		userHome, err := os.UserHomeDir()
		if err != nil {
			return InstallResult{Error: "cannot resolve the home directory"}
		}
		home = filepath.Join(userHome, ".codex")
	}
	spool := opts.Spool
	if spool == "" {
		spool = filepath.Join(home, "astrorder-observer", "events")
	}
	helper := opts.Helper
	if helper == "" {
		executable, err := os.Executable()
		if err != nil {
			return InstallResult{Error: "cannot resolve the helper path"}
		}
		helper = executable
	}
	command, err := HookCommand(helper, spool)
	if err != nil {
		return InstallResult{Error: err.Error()}
	}
	if err := os.MkdirAll(spool, 0o700); err != nil {
		return InstallResult{Error: "cannot create the event spool"}
	}
	result := InstallResult{Command: command}
	if result.Rewrite, err = RewriteHooks(home, command); err != nil {
		result.Error = err.Error()
		return result
	}
	result.OK = true
	if !opts.Trust {
		return result
	}
	codex := opts.Codex
	if codex == "" {
		codex = FindCodex()
	}
	if codex == "" {
		result.TrustError = "codex executable was not found"
		return result
	}
	trust, err := TrustHooks(ctx, codex, home, command)
	result.Trust = &trust
	if err != nil {
		result.TrustError = err.Error()
	}
	return result
}

// FindCodex locates the Codex executable without a login shell: PATH first,
// then the install locations Astrorder's remote probe also knows.
func FindCodex() string {
	if path, err := exec.LookPath("codex"); err == nil {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	name := "codex"
	if runtime.GOOS == "windows" {
		name = "codex.exe"
	}
	for _, relative := range []string{".vite-plus/bin", ".local/bin", "bin", ".bun/bin", ".npm-global/bin"} {
		candidate := filepath.Join(home, filepath.FromSlash(relative), name)
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate
		}
	}
	matches, _ := filepath.Glob(filepath.Join(home, ".nvm", "versions", "node", "*", "bin", name))
	if len(matches) > 0 {
		return matches[len(matches)-1]
	}
	return ""
}
