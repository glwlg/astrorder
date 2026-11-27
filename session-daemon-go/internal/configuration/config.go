package configuration

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"astrorder.dev/session-daemon/internal/protocol"
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
	Home            string `json:"home"`
	HelperAssetsDir string `json:"helper_assets_dir,omitempty"`
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

type validatedConfig struct {
	journal      *JournalConfig
	runtimes     []Runtime
	modelHome    string
	helperAssets string
}

func validateRuntime(r Runtime) (Runtime, error) {
	if r.Type != "codex" && r.Type != "grok" && r.Type != "antigravity" && r.Type != "pty" && r.Type != "hermes" {
		return Runtime{}, fmt.Errorf("runtime is not implemented: %s", r.Type)
	}
	if len(r.Allowed) == 0 {
		return Runtime{}, fmt.Errorf("runtime requires allowed workspace roots")
	}
	roots := make([]string, 0, len(r.Allowed))
	for _, root := range r.Allowed {
		resolved, err := canonicalDirectory(root)
		if err != nil {
			return Runtime{}, err
		}
		roots = append(roots, resolved)
	}
	r.Allowed = roots
	if r.Type != "pty" {
		if r.Executable == "" || strings.ContainsRune(r.Executable, 0) {
			return Runtime{}, fmt.Errorf("runtime executable is invalid")
		}
		cwd, err := canonicalDirectory(r.Workspace)
		if err != nil {
			return Runtime{}, err
		}
		inside := false
		for _, root := range roots {
			rel, err := filepath.Rel(root, cwd)
			if err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator)) && !filepath.IsAbs(rel) {
				inside = true
			}
		}
		if !inside {
			return Runtime{}, fmt.Errorf("default workspace is outside allowlist")
		}
		r.Workspace = cwd
	}
	for _, arg := range r.Arguments {
		if strings.ContainsRune(arg, 0) {
			return Runtime{}, fmt.Errorf("runtime argument is invalid")
		}
	}
	r.Arguments = append([]string(nil), r.Arguments...)
	return r, nil
}

func validateConfig(c Config) (validatedConfig, error) {
	var out validatedConfig
	if c.Journal != nil {
		j := *c.Journal
		out.journal = &j
	}
	if c.ModelConfig != nil {
		var err error
		out.modelHome, err = canonicalDirectory(c.ModelConfig.Home)
		if err != nil {
			return validatedConfig{}, fmt.Errorf("model configuration home: %w", err)
		}
		if c.ModelConfig.HelperAssetsDir != "" {
			out.helperAssets, err = canonicalDirectory(c.ModelConfig.HelperAssetsDir)
			if err != nil {
				return validatedConfig{}, fmt.Errorf("model helper assets: %w", err)
			}
		}
	}
	out.runtimes = make([]Runtime, 0, len(c.Runtimes))
	seen := map[string]bool{}
	for _, r := range c.Runtimes {
		if seen[r.Type] {
			return validatedConfig{}, fmt.Errorf("duplicate runtime configuration: %s", r.Type)
		}
		seen[r.Type] = true
		vr, err := validateRuntime(r)
		if err != nil {
			return validatedConfig{}, err
		}
		out.runtimes = append(out.runtimes, vr)
	}
	return out, nil
}

func (c Config) Register(d *protocol.Daemon) error {
	manager := NewManager(d, ManagerOptions{})
	_, err := manager.Apply(context.Background(), c)
	return err
}
