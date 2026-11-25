package lifecycle

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

type daemonConfigJSON struct {
	Runtimes    []daemonRuntimeJSON    `json:"runtimes"`
	ModelConfig *daemonModelConfigJSON `json:"model_config,omitempty"`
}

type daemonRuntimeJSON struct {
	Type       string   `json:"type"`
	Executable string   `json:"executable,omitempty"`
	Arguments  []string `json:"arguments,omitempty"`
	Workspace  string   `json:"workspace,omitempty"`
	Allowed    []string `json:"allowed"`
	AgentID    string   `json:"agent_id,omitempty"`
	Shell      string   `json:"shell,omitempty"`
}

type daemonModelConfigJSON struct {
	Home            string `json:"home"`
	HelperAssetsDir string `json:"helper_assets_dir,omitempty"`
}

func discoverAntigravity() string {
	if p, err := exec.LookPath("agy"); err == nil {
		absolute, _ := filepath.Abs(p)
		return absolute
	}
	if base := os.Getenv("LOCALAPPDATA"); base != "" {
		p := filepath.Join(base, "agy", "bin", "agy.exe")
		if info, err := os.Stat(p); err == nil && !info.IsDir() {
			return p
		}
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		binName := "agy"
		if runtime.GOOS == "windows" {
			binName = "agy.exe"
		}
		for _, candidate := range []string{
			filepath.Join(home, ".local", "bin", binName),
			filepath.Join(home, ".antigravity", "bin", binName),
		} {
			if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
				return candidate
			}
		}
	}
	return ""
}

// EnsureDaemonConfig writes session-daemon.json with discovered native runtimes if not present.
func EnsureDaemonConfig(stateDir, rootDir string) (string, error) {
	cfgPath := filepath.Join(stateDir, "session-daemon.json")
	if _, err := os.Stat(cfgPath); err == nil {
		return cfgPath, nil
	}

	userHome, err := os.UserHomeDir()
	if err != nil || userHome == "" {
		userHome = filepath.Dir(stateDir)
	}

	allowed := []string{
		rootDir,
		userHome,
	}

	runtimes := make([]daemonRuntimeJSON, 0, 4)

	// Discover codex
	if codexPath, err := exec.LookPath("codex"); err == nil && codexPath != "" {
		if abs, err := filepath.Abs(codexPath); err == nil {
			runtimes = append(runtimes, daemonRuntimeJSON{
				Type:       "codex",
				Executable: abs,
				Arguments:  []string{},
				Workspace:  rootDir,
				Allowed:    allowed,
				AgentID:    "local-codex",
			})
		}
	}

	// Discover hermes
	if hermesPath, err := exec.LookPath("hermes"); err == nil && hermesPath != "" {
		if abs, err := filepath.Abs(hermesPath); err == nil {
			executable, arguments, resolveErr := resolveHermesRuntime(abs, probeNativeRuntime)
			if resolveErr != nil {
				return "", resolveErr
			}
			runtimes = append(runtimes, daemonRuntimeJSON{
				Type:       "hermes",
				Executable: executable,
				Arguments:  arguments,
				Workspace:  rootDir,
				Allowed:    allowed,
				AgentID:    "local-hermes-default",
			})
		}
	}

	// Discover grok
	if grokPath, err := exec.LookPath("grok"); err == nil && grokPath != "" {
		if abs, err := filepath.Abs(grokPath); err == nil {
			runtimes = append(runtimes, daemonRuntimeJSON{
				Type:       "grok",
				Executable: abs,
				Arguments:  []string{},
				Workspace:  rootDir,
				Allowed:    allowed,
				AgentID:    "local-grok",
			})
		}
	}

	// Discover the native Antigravity CLI without changing its settings.
	if agyPath := discoverAntigravity(); agyPath != "" {
		runtimes = append(runtimes, daemonRuntimeJSON{Type: "antigravity", Executable: agyPath, Workspace: rootDir, Allowed: allowed, AgentID: "local-antigravity"})
	}

	// PTY runtime is always enabled
	runtimes = append(runtimes, daemonRuntimeJSON{
		Type:    "pty",
		Allowed: allowed,
	})

	cfg := daemonConfigJSON{
		Runtimes: runtimes,
		ModelConfig: &daemonModelConfigJSON{
			Home: userHome,
		},
	}
	if info, err := os.Stat(filepath.Join(rootDir, "bin", "helpers")); err == nil && info.IsDir() {
		cfg.ModelConfig.HelperAssetsDir = filepath.Join(rootDir, "bin", "helpers")
	}

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal daemon config: %w", err)
	}
	data = append(data, '\n')

	if err := atomicWrite(cfgPath, data, 0o600); err != nil {
		return "", fmt.Errorf("write session-daemon.json: %w", err)
	}
	if runtime.GOOS == "windows" {
		_ = privateFile(cfgPath)
	}

	return cfgPath, nil
}

// ReadLocalRuntimeExecutable reads the executable configured in session-daemon.json
// for a given local runtime kind ("codex" | "hermes" | "grok" | "antigravity").
func ReadLocalRuntimeExecutable(stateDir, kind string) string {
	if stateDir == "" || kind == "" {
		return ""
	}
	raw, err := os.ReadFile(filepath.Join(stateDir, "session-daemon.json"))
	if err != nil {
		return ""
	}
	var cfg daemonConfigJSON
	if json.Unmarshal(raw, &cfg) != nil {
		return ""
	}
	for _, rt := range cfg.Runtimes {
		if rt.Type == kind {
			return rt.Executable
		}
	}
	return ""
}

// UpsertLocalRuntime atomically inserts or updates one local Agent runtime
// entry in session-daemon.json without disturbing existing allowed roots or
// other runtimes. It returns changed=true when the file was modified.
func UpsertLocalRuntime(stateDir, rootDir, kind, executable string) (bool, error) {
	if stateDir == "" {
		return false, fmt.Errorf("stateDir is required")
	}
	switch kind {
	case "codex", "hermes", "grok", "antigravity":
	default:
		return false, fmt.Errorf("unsupported local runtime kind: %s", kind)
	}
	if executable == "" {
		return false, fmt.Errorf("executable is required")
	}
	absExec, err := filepath.Abs(executable)
	if err != nil {
		return false, fmt.Errorf("resolve executable: %w", err)
	}

	userHome, _ := os.UserHomeDir()
	if rootDir == "" {
		rootDir = userHome
	}
	if rootDir == "" {
		rootDir = filepath.Dir(stateDir)
	}

	cfgPath, err := EnsureDaemonConfig(stateDir, rootDir)
	if err != nil {
		return false, err
	}
	raw, err := os.ReadFile(cfgPath)
	if err != nil {
		return false, fmt.Errorf("read session-daemon.json: %w", err)
	}
	var cfg daemonConfigJSON
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return false, fmt.Errorf("parse session-daemon.json: %w", err)
	}

	resolvedExec := absExec
	args := []string{}
	if kind == "hermes" {
		hermesExec, hermesArgs, resolveErr := resolveHermesRuntime(absExec, probeNativeRuntime)
		if resolveErr == nil && hermesExec != "" {
			resolvedExec = hermesExec
			args = hermesArgs
		}
	}

	agentIDs := map[string]string{
		"codex":       "local-codex",
		"hermes":      "local-hermes-default",
		"grok":        "local-grok",
		"antigravity": "local-antigravity",
	}

	// Preserve existing allowed roots from any existing runtime entry.
	var allowed []string
	var workspace string
	for _, rt := range cfg.Runtimes {
		if len(allowed) == 0 && len(rt.Allowed) > 0 {
			allowed = append([]string(nil), rt.Allowed...)
		}
		if workspace == "" && rt.Workspace != "" {
			workspace = rt.Workspace
		}
	}
	if len(allowed) == 0 {
		allowed = []string{rootDir}
		if userHome != "" && userHome != rootDir {
			allowed = append(allowed, userHome)
		}
	}
	if workspace == "" {
		workspace = allowed[0]
	}

	updated := false
	changed := false
	for i := range cfg.Runtimes {
		if cfg.Runtimes[i].Type == kind {
			updated = true
			if cfg.Runtimes[i].Executable != resolvedExec || !sameStringSlice(cfg.Runtimes[i].Arguments, args) {
				cfg.Runtimes[i].Executable = resolvedExec
				cfg.Runtimes[i].Arguments = args
				changed = true
			}
			break
		}
	}
	if !updated {
		cfg.Runtimes = append(cfg.Runtimes, daemonRuntimeJSON{
			Type:       kind,
			Executable: resolvedExec,
			Arguments:  args,
			Workspace:  workspace,
			Allowed:    allowed,
			AgentID:    agentIDs[kind],
		})
		changed = true
	}
	if !changed {
		return false, nil
	}

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return false, fmt.Errorf("marshal session-daemon.json: %w", err)
	}
	data = append(data, '\n')
	if err := atomicWrite(cfgPath, data, 0o600); err != nil {
		return false, fmt.Errorf("write session-daemon.json: %w", err)
	}
	if runtime.GOOS == "windows" {
		_ = privateFile(cfgPath)
	}
	return true, nil
}

func sameStringSlice(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
