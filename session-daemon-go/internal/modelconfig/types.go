package modelconfig

import (
	"context"
	"fmt"
	"runtime"
	"sync"
)

// Managed file identifiers and their relative paths.
const (
	FileCodexConfig        = "codex_config"
	FileCodexCatalog       = "codex_catalog"
	FileCodexMagpieCatalog = "codex_magpie_catalog"
	FileGrokConfig         = "grok_config"
	FileHermesConfig       = "hermes_config"

	RelCodexConfig        = ".codex/config.toml"
	RelCodexCatalog       = ".codex/opencodex-catalog.json"
	RelCodexMagpieCatalog = ".codex/magpie-catalog.json"
	RelGrokConfig         = ".grok/config.toml"

	RelEnvConfig = ".config/environment.d/astrorder-opencodex.conf"
	RelMagpieEnv = ".config/environment.d/astrorder-magpie.conf"
	EnvTokenKey  = "OPENCODEX_API_AUTH_TOKEN"
	MagpieEnvKey = "MAGPIE_API_KEY"

	EmptySHA256 = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
)

var ManagedFiles = map[string]string{
	FileCodexConfig:        RelCodexConfig,
	FileCodexCatalog:       RelCodexCatalog,
	FileCodexMagpieCatalog: RelCodexMagpieCatalog,
	FileGrokConfig:         RelGrokConfig,
}

var CredentialFiles = map[string]string{
	EnvTokenKey:  RelEnvConfig,
	MagpieEnvKey: RelMagpieEnv,
}

func init() {
	if runtime.GOOS == "windows" {
		ManagedFiles[FileHermesConfig] = `AppData/Local/hermes/config.yaml`
	} else {
		ManagedFiles[FileHermesConfig] = `.hermes/config.yaml`
	}
}

// Config specifies the runtime configuration for the model_config service.
type Config struct {
	Credentials  CredentialStore
	AllowedRoots []string
	Busy         func() bool
	BusyFor      func(string) bool
	Reload       func(ctx context.Context, agentType string) error
}

// Set must return a restoration callback even after a partial failed write.
type CredentialStore interface {
	Set(context.Context, string) (func() error, error)
}

// Service provides the model_config plan, apply, and reload capabilities.
type Service struct {
	config       Config
	allowedRoots []string

	mu             sync.Mutex
	pendingMu      sync.Mutex
	pendingReloads map[string]bool
}

// ProtocolError represents an error conforming to DaemonProtocolError.
type ProtocolError struct {
	Message string
}

func (e *ProtocolError) Error() string {
	return e.Message
}

func newProtocolErrorf(format string, args ...any) error {
	return &ProtocolError{Message: fmt.Sprintf(format, args...)}
}
