package pluginhost

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"astrorder.dev/app-server/internal/auth"
	"astrorder.dev/app-server/internal/store"
	"astrorder.dev/app-server/internal/targetfs"
)

const (
	BaseExtPath     = "/api/v1/ext"
	BaseAdminPath   = "/api/v1/extensions"
	BaseHostAPIPath = "/api/v1/plugin-host"
	SDKBridgePath   = "/api/v1/ext/_sdk/bridge.js"

	PermNetworkOutbound   = "network:outbound"
	PermWorkspaceGitRead  = "workspace:git.read"
	PermStorageKV         = "storage:kv"
	PermStorageSecrets    = "storage:secrets"
	PermAgentCapabilities = "agent:capabilities"
	PermUISlots           = "ui:slots"
	PermSignalsProvide    = "signals:provide"

	BackendTypeBuiltin = "builtin"
	BackendTypeProcess = "process"
	BackendTypeHTTP    = "http"
	BackendTypeNone    = "none"

	SettingsModeNone     = "none"
	SettingsModeSchema   = "schema"
	SettingsModeCustomUI = "custom_ui"

	SourceBuiltin  = "builtin"
	SourceExternal = "external"

	RuntimeStateBuiltin  = "builtin"
	RuntimeStateReady    = "ready"
	RuntimeStateStopped  = "stopped"
	RuntimeStateStarting = "starting"
	RuntimeStateError    = "error"
)

var (
	ErrNotFound                = errors.New("plugin not found")
	ErrInvalid                 = errors.New("invalid plugin input")
	ErrConflict                = errors.New("plugin conflict")
	ErrPermissionDenied        = errors.New("plugin permission denied")
	ErrPluginDisabled          = errors.New("plugin is disabled")
	ErrPermissionGrantRequired = errors.New("plugin permissions require user grant")
)

// BrowserAuthorizer authorizes browser HTTP requests.
type BrowserAuthorizer interface {
	AuthorizeBrowser(*http.Request) *auth.Error
}

// Options configures the extension platform Manager.
type Options struct {
	Store       *store.Store
	Authorizer  BrowserAuthorizer
	Resolver    *targetfs.Resolver
	PluginsDir  string
	HostBaseURL string
	Logger      *slog.Logger
}

// DeclarativePillPayload is the JSON payload returned by a status bar pill endpoint.
type DeclarativePillPayload struct {
	Visible     bool           `json:"visible"`
	Label       string         `json:"label,omitempty"`
	Sublabel    string         `json:"sublabel,omitempty"`
	Tone        string         `json:"tone,omitempty"`
	DotColor    string         `json:"dot_color,omitempty"`
	Pulsing     bool           `json:"pulsing,omitempty"`
	Tooltip     string         `json:"tooltip,omitempty"`
	Icon        string         `json:"icon,omitempty"`
	PanelParams map[string]any `json:"panel_params,omitempty"`
}

// SignalQuery defines parameters passed to signal provider plugins.
type SignalQuery struct {
	Workspace    string `json:"workspace,omitempty"`
	ConnectionID string `json:"connection_id,omitempty"`
	SessionID    string `json:"session_id,omitempty"`
	Limit        int    `json:"limit,omitempty"`
}

// SignalItem represents one signal (e.g. CI failure) produced by a plugin.
type SignalItem struct {
	SourcePlugin string         `json:"source_plugin"`
	Kind         string         `json:"kind"`
	Title        string         `json:"title"`
	Detail       string         `json:"detail,omitempty"`
	URL          string         `json:"url,omitempty"`
	Severity     string         `json:"severity,omitempty"`
	Timestamp    string         `json:"timestamp,omitempty"`
	Meta         map[string]any `json:"meta,omitempty"`
}

// WorkspaceGitInfo describes git repository metadata detected for a workspace.
type WorkspaceGitInfo struct {
	Host        string `json:"host"`
	Port        string `json:"port,omitempty"`
	ProjectPath string `json:"project_path"`
	Branch      string `json:"branch,omitempty"`
	HeadSHA     string `json:"head_sha,omitempty"`
	RemoteName  string `json:"remote_name,omitempty"`
	RemoteURL   string `json:"remote_url,omitempty"`
}

// PluginSkillEntry represents one SKILL.md contributed by an enabled plugin.
type PluginSkillEntry struct {
	PluginID    string `json:"plugin_id"`
	Name        string `json:"name"`
	DisplayName string `json:"display_name,omitempty"`
	Description string `json:"description,omitempty"`
	Content     string `json:"content"`
}

// AgentCapabilityInfo describes an agent-callable capability exposed by an enabled plugin.
type AgentCapabilityInfo struct {
	PluginID       string         `json:"plugin_id"`
	PluginName     string         `json:"plugin_name"`
	CapabilityID   string         `json:"capability_id"`
	FullID         string         `json:"id"`
	CLIAlias       []string       `json:"cli_alias,omitempty"`
	Summary        string         `json:"summary"`
	InputSchema    map[string]any `json:"input_schema,omitempty"`
	RequiresCaller bool           `json:"requires_caller,omitempty"`
}

// BuiltinPlugin is implemented by in-process official plugins registered with the Manager.
type BuiltinPlugin interface {
	Manifest() Manifest
	ServeExtension(w http.ResponseWriter, r *http.Request, subpath string)
	InvokeCapability(ctx context.Context, capID string, payload map[string]any) (any, error)
	CollectSignals(ctx context.Context, kind string, q SignalQuery) ([]SignalItem, error)
}

// BuiltinSkillProvider is an optional interface that a BuiltinPlugin can implement
// to contribute SKILL.md entries automatically when the plugin is enabled.
type BuiltinSkillProvider interface {
	Skills() []PluginSkillEntry
}

// PluginView is the public wire view of a builtin or external plugin.
type PluginView struct {
	ID                 string          `json:"id"`
	Name               string          `json:"name"`
	Version            string          `json:"version"`
	Category           string          `json:"category"`
	Description        string          `json:"description,omitempty"`
	Author             string          `json:"author,omitempty"`
	Homepage           string          `json:"homepage,omitempty"`
	Icon               string          `json:"icon,omitempty"`
	Source             string          `json:"source"`
	Enabled            bool            `json:"enabled"`
	Permissions        []string        `json:"permissions"`
	GrantedPermissions []string        `json:"granted_permissions"`
	PendingPermissions []string        `json:"pending_permissions"`
	PermissionsGranted bool            `json:"permissions_granted"`
	RuntimeState       string          `json:"runtime_state"`
	RuntimeError       string          `json:"runtime_error,omitempty"`
	BackendType        string          `json:"backend_type"`
	Settings           SettingsSpec    `json:"settings"`
	Contributes        ContributesSpec `json:"contributes"`
	UIBaseURL          string          `json:"ui_base_url,omitempty"`
	APIBaseURL         string          `json:"api_base_url,omitempty"`
}

// ExtensionConfigView is returned by GET /api/v1/extensions/{id}/config.
type ExtensionConfigView struct {
	PluginID      string            `json:"plugin_id"`
	Values        map[string]any    `json:"values"`
	SecretsSet    map[string]bool   `json:"secrets_set"`
	MaskedSecrets map[string]string `json:"masked_secrets"`
}
