package configuration

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"astrorder.dev/session-daemon/internal/modelconfig"
	"astrorder.dev/session-daemon/internal/protocol"
	core "astrorder.dev/session-daemon/internal/runtime"
	"astrorder.dev/session-daemon/internal/runtime/codex"
	"astrorder.dev/session-daemon/internal/runtime/grok"
	"astrorder.dev/session-daemon/internal/runtime/hermes"
	"astrorder.dev/session-daemon/internal/runtime/pty"
	"astrorder.dev/session-daemon/internal/runtime/remote"
)

type Runtime struct {
	Type       string   `json:"type"`
	Executable string   `json:"executable,omitempty"`
	Arguments  []string `json:"arguments,omitempty"`
	Workspace  string   `json:"workspace,omitempty"`
	Allowed    []string `json:"allowed"`
	AgentID    string   `json:"agent_id,omitempty"`
	Shell      string   `json:"shell,omitempty"`
}
type JournalConfig struct {
	TotalBytes    int64 `json:"total_bytes"`
	SessionBytes  int64 `json:"session_bytes"`
	MaxAgeSeconds int64 `json:"max_age_seconds"`
}

type Config struct {
	Journal     *JournalConfig `json:"journal,omitempty"`
	Runtimes    []Runtime      `json:"runtimes"`
	ModelConfig *ModelConfig   `json:"model_config,omitempty"`
}
type ModelConfig struct {
	Home string `json:"home"`
}

func Parse(reader io.Reader) (Config, error) {
	var config Config
	decoder := json.NewDecoder(io.LimitReader(reader, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&config); err != nil {
		return config, err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return config, fmt.Errorf("configuration must contain exactly one object")
	}
	return config, nil
}
func Load(path string) (Config, error) {
	file, err := os.Open(path)
	if err != nil {
		return Config{}, err
	}
	defer file.Close()
	return Parse(file)
}
func canonicalDirectory(path string) (string, error) {
	if strings.ContainsRune(path, 0) || path == "" {
		return "", fmt.Errorf("workspace is invalid")
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(resolved)
	if err != nil || !info.IsDir() {
		return "", fmt.Errorf("workspace must be an existing directory")
	}
	return resolved, nil
}
func (c Config) Register(d *protocol.Daemon) error {
	var modelHome string
	if c.ModelConfig != nil {
		var err error
		modelHome, err = canonicalDirectory(c.ModelConfig.Home)
		if err != nil {
			return fmt.Errorf("model configuration home: %w", err)
		}
	}
	validated := make([]Runtime, 0, len(c.Runtimes))
	seen := map[string]bool{}
	for _, r := range c.Runtimes {
		if seen[r.Type] {
			return fmt.Errorf("duplicate runtime configuration: %s", r.Type)
		}
		seen[r.Type] = true
		if r.Type != "codex" && r.Type != "grok" && r.Type != "pty" && r.Type != "hermes" {
			return fmt.Errorf("runtime is not implemented: %s", r.Type)
		}
		if len(r.Allowed) == 0 {
			return fmt.Errorf("runtime requires allowed workspace roots")
		}
		roots := make([]string, 0, len(r.Allowed))
		for _, root := range r.Allowed {
			resolved, err := canonicalDirectory(root)
			if err != nil {
				return err
			}
			roots = append(roots, resolved)
		}
		r.Allowed = roots
		if r.Type != "pty" {
			if r.Executable == "" || strings.ContainsRune(r.Executable, 0) {
				return fmt.Errorf("runtime executable is invalid")
			}
			cwd, err := canonicalDirectory(r.Workspace)
			if err != nil {
				return err
			}
			inside := false
			for _, root := range roots {
				rel, err := filepath.Rel(root, cwd)
				if err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator)) && !filepath.IsAbs(rel) {
					inside = true
				}
			}
			if !inside {
				return fmt.Errorf("default workspace is outside allowlist")
			}
			r.Workspace = cwd
		}
		for _, arg := range r.Arguments {
			if strings.ContainsRune(arg, 0) {
				return fmt.Errorf("runtime argument is invalid")
			}
		}
		validated = append(validated, r)
	}
	if c.Journal != nil {
		if err := d.ConfigureRetention(c.Journal.TotalBytes, c.Journal.SessionBytes, c.Journal.MaxAgeSeconds); err != nil {
			return err
		}
	}
	adapters := map[string]core.Adapter{}
	for _, r := range validated {
		var adapter core.Adapter
		switch r.Type {
		case "codex":
			cfg := codex.Config{Executable: r.Executable, Arguments: r.Arguments, Workspace: r.Workspace, Allowed: r.Allowed, AgentID: r.AgentID}
			adapter = codex.NewAdapter(cfg, d.Emit)
			obsCtx, obsCancel := context.WithCancel(context.Background())
			go codex.StartDesktopStopObserver(obsCtx, cfg, d.Emit, 500*time.Millisecond)
			d.AddCloser(func() error {
				obsCancel()
				return nil
			})
		case "grok":
			adapter = grok.New(grok.Config{Executable: r.Executable, Arguments: r.Arguments, Workspace: r.Workspace, Allowed: r.Allowed, AgentID: r.AgentID, Emit: d.Emit})
		case "hermes":
			adapter = hermes.NewIsolated(hermes.Config{Executable: r.Executable, Arguments: r.Arguments, Workspace: r.Workspace, Allowed: r.Allowed, AgentID: r.AgentID, Emit: d.Emit})
		case "pty":
			instance, err := pty.New(pty.Config{Allowed: r.Allowed, Shell: r.Shell, Emit: d.Emit})
			if err != nil {
				return err
			}
			adapter = instance
		}
		adapters[r.Type] = adapter
	}
	for kind, adapter := range adapters {
		d.RegisterRuntime(kind, adapter)
	}

	// Always provide multiplexed SSH child registries for remote agents
	codexSSH := core.NewMultiplexRegistry(remote.NewRemoteCodexFactory(d.Emit))
	d.RegisterRuntime("codex-ssh", codexSSH)

	grokSSH := core.NewMultiplexRegistry(remote.NewRemoteGrokFactory(d.Emit))
	d.RegisterRuntime("grok-ssh", grokSSH)

	sshHermes := core.NewMultiplexRegistry(remote.NewRemoteHermesFactory(d.Emit))
	d.RegisterRuntime("ssh", sshHermes)

	if modelHome != "" {
		if err := d.ConfigureModelConfig(modelconfig.Config{AllowedRoots: []string{modelHome}, Credentials: modelconfig.NativeCredentialStore()}); err != nil {
			return err
		}
	}
	return nil
}
