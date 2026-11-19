package remote

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"astrorder.dev/session-daemon/internal/modelconfig"
	"astrorder.dev/session-daemon/internal/process"
	core "astrorder.dev/session-daemon/internal/runtime"
	agyrt "astrorder.dev/session-daemon/internal/runtime/antigravity"
	sshtrans "astrorder.dev/session-daemon/internal/transport/ssh"
)

// NewRemoteAntigravityFactory returns a ChildFactory that spawns remote
// Antigravity (agy) stream-json sessions over SSH and executes remote SQLite /
// loopback LanguageServerService control calls via the hash-verified helper.
func NewRemoteAntigravityFactory(emit func(string, string, map[string]any, string) error, helperAssetsDir string) core.ChildFactory {
	return func(connID string, settings map[string]any) (core.Adapter, error) {
		executable, _ := settings["antigravity_executable"].(string)
		if executable == "" || strings.ContainsRune(executable, 0) || !strings.HasPrefix(executable, "/") {
			return nil, fmt.Errorf("remote Antigravity executable must be an absolute POSIX path")
		}
		sshCfg, err := ParseSSHConfig(settings)
		if err != nil {
			return nil, err
		}
		transport, err := sshtrans.New(sshCfg)
		if err != nil {
			return nil, err
		}

		starter := func(ctx context.Context, spec sshtrans.CommandSpec) (agyrt.SSHSession, error) {
			env := map[string]string{}
			for _, entry := range process.NativeEnvironment() {
				name, value, _ := strings.Cut(entry, "=")
				switch strings.ToUpper(name) {
				case "OPENCODEX_API_AUTH_TOKEN", "MAGPIE_API_KEY", "STARSHIP_SESSION_KEY", "GROK45_API_KEY", "EMBED__API_KEY":
					if value != "" {
						env[strings.ToUpper(name)] = value
					}
				}
			}
			for k, v := range spec.Environment {
				env[k] = v
			}
			spec.Environment = env
			return transport.Start(ctx, spec)
		}

		installer := modelconfig.NewSSHHelperInstaller(transport, helperAssetsDir)
		helper := func(ctx context.Context, payload map[string]any) (map[string]any, error) {
			res, invokeErr := installer.Invoke(ctx, "antigravity", payload)
			if invokeErr != nil {
				var hie *modelconfig.HelperInvokeError
				if errors.As(invokeErr, &hie) {
					return nil, &agyrt.RemoteHelperError{
						Message:        hie.Detail,
						BeforeDispatch: hie.BeforeDispatch,
					}
				}
				return nil, invokeErr
			}
			return res, nil
		}

		dispName, _ := settings["display_name"].(string)
		dispName = strings.TrimSpace(dispName)
		if dispName == "" {
			dispName = connID
		}

		workspace, _ := settings["workspace"].(string)
		if workspace == "" || !strings.HasPrefix(workspace, "/") {
			workspace = "/"
		}

		cfg := agyrt.Config{
			Executable:   executable,
			Workspace:    workspace,
			Allowed:      []string{"/"},
			AgentID:      fmt.Sprintf("ssh-antigravity-%s", connID),
			AgentName:    "Antigravity · " + dispName,
			ConnectionID: connID,
			Emit:         emit,
		}
		return agyrt.NewWithSSH(cfg, starter, helper), nil
	}
}
