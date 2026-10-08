package remote

import (
	"context"
	"crypto/sha256"
	"fmt"
	"strings"

	core "astrorder.dev/session-daemon/internal/runtime"
	hermesrt "astrorder.dev/session-daemon/internal/runtime/hermes"
	sshtrans "astrorder.dev/session-daemon/internal/transport/ssh"
)

// NewRemoteHermesFactory creates a ChildFactory for the "ssh" runtime (remote Hermes bridge).
func NewRemoteHermesFactory(emit func(string, string, map[string]any, string) error) core.ChildFactory {
	return func(connID string, settings map[string]any) (core.Adapter, error) {
		sshCfg, err := ParseSSHConfig(settings)
		if err != nil {
			return nil, err
		}
		transport, err := sshtrans.New(sshCfg)
		if err != nil {
			return nil, err
		}

		executable, _ := settings["hermes_executable"].(string)
		if executable == "" {
			executable, _ = settings["hermes_path"].(string)
		}
		if executable == "" {
			executable = "hermes"
		}
		if strings.ContainsRune(executable, 0) {
			return nil, fmt.Errorf("hermes executable is invalid")
		}

		workspace, _ := settings["workspace"].(string)
		if workspace == "" {
			workspace = "/"
		}

		arguments := []string{"--run-module", "tui_gateway.entry"}
		custom := false
		if rawArgs, ok := settings["arguments"].([]any); ok {
			var customArgs []string
			for _, a := range rawArgs {
				if s, ok := a.(string); ok {
					customArgs = append(customArgs, s)
				}
			}
			if len(customArgs) > 0 {
				arguments = customArgs
				custom = true
			}
		}

		profile, _ := settings["profile_name"].(string)
		if profile == "" {
			profile = "default"
		}
		if !custom && profile != "default" {
			arguments = []string{"--profile", profile, "--run-module", "tui_gateway.entry"}
		}
		digest := sha256.Sum256([]byte(connID + "|" + profile))
		starter := func(ctx context.Context, spec sshtrans.CommandSpec) (hermesrt.SSHSession, error) {
			if spec.Executable == "hermes" && !custom {
				// Non-interactive SSH shells do not load the user's CLI PATH.
				spec.Executable = "/bin/sh"
				spec.Args = append([]string{"-c", `p=$(command -v hermes) || p=""; if [ -z "$p" ]; then for candidate in "$HOME/.local/bin/hermes" "$HOME/.hermes/bin/hermes"; do if [ -x "$candidate" ]; then p="$candidate"; break; fi; done; fi; if [ -z "$p" ]; then printf 'Hermes launcher not found\n' >&2; exit 127; fi; exec "$p" "$@"`, "astrorder-hermes"}, spec.Args...)
			}
			return transport.Start(ctx, spec)
		}

		name, _ := settings["display_name"].(string)
		name = strings.TrimSpace(name)
		if name == "" {
			name = "远程"
		}
		if !strings.HasSuffix(name, "Hermes") {
			name += " · Hermes"
		}
		cfg := hermesrt.Config{
			Executable:   executable,
			Arguments:    arguments,
			Workspace:    workspace,
			Allowed:      []string{"/"},
			AgentID:      fmt.Sprintf("ssh-hermes-%s", connID),
			AgentName:    name,
			ConnectionID: connID,
			ProfileName:  profile,
			SourceID:     fmt.Sprintf("hermes-ssh-%x", digest[:12]),
			Emit:         emit,
		}

		return hermesrt.NewWithSSHStarter(cfg, starter), nil
	}
}
