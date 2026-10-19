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
