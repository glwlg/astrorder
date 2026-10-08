package remote

import (
	"context"
	"fmt"
	"strings"

	"astrorder.dev/session-daemon/internal/process"
	core "astrorder.dev/session-daemon/internal/runtime"
	codexrt "astrorder.dev/session-daemon/internal/runtime/codex"
	grokrt "astrorder.dev/session-daemon/internal/runtime/grok"
	sshtrans "astrorder.dev/session-daemon/internal/transport/ssh"
)

// ParseSSHConfig extracts and validates ssh.Config from an untrusted map.
func ParseSSHConfig(settings map[string]any) (sshtrans.Config, error) {
	cfg := sshtrans.Config{Binary: "ssh"}
	host, _ := settings["host"].(string)
	if host == "" {
		return cfg, fmt.Errorf("ssh host is required")
	}
	cfg.Host = host

	if user, ok := settings["user"].(string); ok && user != "" {
		cfg.User = user
	}
	if port, ok := settings["port"].(float64); ok && port > 0 {
		cfg.Port = int(port)
	} else if port, ok := settings["port"].(int); ok && port > 0 {
		cfg.Port = port
	}
	if key, ok := settings["identity_file"].(string); ok && key != "" {
		cfg.IdentityFile = key
	}
	if err := cfg.Validate(); err != nil {
		return cfg, err
	}
	return cfg, nil
}

// NewRemoteCodexFactory returns a ChildFactory that spawns remote Codex app-server instances over SSH.
func NewRemoteCodexFactory(emit func(string, string, map[string]any, string) error) core.ChildFactory {
	return func(connID string, settings map[string]any) (core.Adapter, error) {
		executable, _ := settings["codex_executable"].(string)
		if executable == "" || strings.ContainsRune(executable, 0) || !strings.HasPrefix(executable, "/") {
			return nil, fmt.Errorf("remote Codex executable must be an absolute POSIX path")
		}
		sshCfg, err := ParseSSHConfig(settings)
		if err != nil {
			return nil, err
		}
		transport, err := sshtrans.New(sshCfg)
		if err != nil {
			return nil, err
		}

		starter := func(ctx context.Context, spec sshtrans.CommandSpec) (codexrt.SSHSession, error) {
			spec.Environment = map[string]string{}
			for _, entry := range process.NativeEnvironment() {
				name, value, _ := strings.Cut(entry, "=")
				switch strings.ToUpper(name) {
				case "OPENCODEX_API_AUTH_TOKEN", "MAGPIE_API_KEY", "STARSHIP_SESSION_KEY", "GROK45_API_KEY", "EMBED__API_KEY":
					if value != "" {
						spec.Environment[strings.ToUpper(name)] = value
					}
				}
			}
			return transport.Start(ctx, spec)
		}

		dispName, _ := settings["display_name"].(string)
		if dispName == "" {
			dispName = connID
		}

		workspace, _ := settings["workspace"].(string)
		if workspace == "" {
			workspace = "/"
		}

		codexConfig := codexrt.Config{
			Executable: executable,
			Workspace:  workspace,
			Allowed:    []string{"/"},
			AgentID:    fmt.Sprintf("ssh-codex-%s", connID),
		}

		return codexrt.NewSSHAdapter(codexConfig, emit, starter), nil
	}
}

// NewRemoteGrokFactory returns a ChildFactory that spawns remote Grok instances over SSH.
func NewRemoteGrokFactory(emit func(string, string, map[string]any, string) error) core.ChildFactory {
	return func(connID string, settings map[string]any) (core.Adapter, error) {
		executable, _ := settings["grok_executable"].(string)
		if executable == "" || strings.ContainsRune(executable, 0) || !strings.HasPrefix(executable, "/") {
			return nil, fmt.Errorf("remote Grok executable must be an absolute POSIX path")
		}
		sshCfg, err := ParseSSHConfig(settings)
		if err != nil {
			return nil, err
		}
		transport, err := sshtrans.New(sshCfg)
		if err != nil {
			return nil, err
		}

		starter := func(ctx context.Context, spec sshtrans.CommandSpec) (grokrt.SSHSession, error) {
			return transport.Start(ctx, spec)
		}

		workspace, _ := settings["workspace"].(string)
		if workspace == "" {
			workspace = "/"
		}

		grokConfig := grokrt.Config{
			Executable: executable,
			Workspace:  workspace,
			Allowed:    []string{"/"},
			AgentID:    fmt.Sprintf("ssh-grok-%s", connID),
			Emit:       emit,
		}

		return grokrt.NewWithSSHStarter(grokConfig, starter), nil
	}
}
