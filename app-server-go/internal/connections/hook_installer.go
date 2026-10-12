package connections

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"astrorder.dev/app-server/internal/observe"
	"astrorder.dev/app-server/internal/targetfs"
)

// HookInstaller points an Agent's native hooks at the Astrorder helper.
type HookInstaller interface {
	Install(ctx context.Context, agent map[string]any) (observe.InstallResult, error)
}

// helperRunner is the part of targetfs.Resolver the installer needs.
type helperRunner interface {
	RunHelper(ctx context.Context, connectionID string, args ...string) ([]byte, error)
}

// NativeHookInstaller installs Codex hooks locally in-process and on SSH/WSL
// hosts by running `astrorder-helper install-hooks` there, so every host gets
// the same logic and the same trust handling.
type NativeHookInstaller struct {
	Resolver helperRunner
	// LocalHelper is the helper binary local hooks will run.
	LocalHelper string
}

// NewNativeHookInstaller builds the installer next to the running executable.
func NewNativeHookInstaller(resolver *targetfs.Resolver) *NativeHookInstaller {
	installer := &NativeHookInstaller{Resolver: resolver}
	if executable, err := os.Executable(); err == nil {
		name := "astrorder-helper"
		if runtime.GOOS == "windows" {
			name += ".exe"
		}
		installer.LocalHelper = filepath.Join(filepath.Dir(executable), name)
	}
	return installer
}

func (i *NativeHookInstaller) Install(ctx context.Context, agent map[string]any) (observe.InstallResult, error) {
	if kind, _ := agent["kind"].(string); kind != "codex" {
		return observe.InstallResult{}, errors.New("hook installation is only available for Codex Agents")
	}
	connectionID, _ := agent["connection_id"].(string)
	if connectionID == "" || connectionID == targetfs.LocalConnectionID {
		if i.LocalHelper == "" {
			return observe.InstallResult{}, errors.New("local helper path is unknown")
		}
		if _, err := os.Stat(i.LocalHelper); err != nil {
			return observe.InstallResult{}, errors.New("local astrorder-helper is missing")
		}
		return observe.InstallHooks(ctx, observe.InstallOptions{Helper: i.LocalHelper, Trust: true}), nil
	}
	if i.Resolver == nil {
		return observe.InstallResult{}, errors.New("remote helper access is not configured")
	}
	output, err := i.Resolver.RunHelper(ctx, connectionID, "install-hooks")
	if err != nil {
		return observe.InstallResult{}, err
	}
	return parseInstallResult(output)
}

// parseInstallResult reads the helper's JSON line. Login-shell noise before it
// is tolerated; the last line that decodes is the result.
func parseInstallResult(output []byte) (observe.InstallResult, error) {
	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	for index := len(lines) - 1; index >= 0; index-- {
		var result observe.InstallResult
		if json.Unmarshal([]byte(strings.TrimSpace(lines[index])), &result) == nil && (result.OK || result.Error != "" || result.Command != "") {
			return result, nil
		}
	}
	return observe.InstallResult{}, errors.New("remote helper returned no install result; the installed helper may be older than this App")
}
