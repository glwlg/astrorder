package pluginhost

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
)

var (
	pluginIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{1,63}$`)
	knownPerms      = map[string]struct{}{
		PermNetworkOutbound:   {},
		PermWorkspaceGitRead:  {},
		PermStorageKV:         {},
		PermStorageSecrets:    {},
		PermAgentCapabilities: {},
		PermUISlots:           {},
		PermSignalsProvide:    {},
	}
)

// Manifest describes a builtin or external Astrorder plugin (plugin.json).
type Manifest struct {
	SchemaVersion int             `json:"schema_version"`
	ID            string          `json:"id"`
	Name          string          `json:"name"`
	Version       string          `json:"version"`
	Category      string          `json:"category,omitempty"`
	Description   string          `json:"description,omitempty"`
	Author        string          `json:"author,omitempty"`
	Homepage      string          `json:"homepage,omitempty"`
	Icon          string          `json:"icon,omitempty"`
	Permissions   []string        `json:"permissions,omitempty"`
	Backend       BackendSpec     `json:"backend"`
	Settings      SettingsSpec    `json:"settings"`
	Contributes   ContributesSpec `json:"contributes"`
}

// BackendSpec describes how a plugin handles dynamic HTTP / agent / signal calls.
type BackendSpec struct {
	Type    string            `json:"type"`
	Command []string          `json:"command,omitempty"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
	Health  string            `json:"health,omitempty"`
	URL     string            `json:"url,omitempty"`
}

// SettingsSpec describes how a plugin's settings are rendered and persisted.
type SettingsSpec struct {
	Mode         string         `json:"mode"`
	Schema       []SettingField `json:"schema,omitempty"`
	UIEntry      string         `json:"ui_entry,omitempty"`
	TestEndpoint string         `json:"test_endpoint,omitempty"`
}

// SettingOption represents one option for a "select" setting field.
type SettingOption struct {
	Label string `json:"label"`
	Value string `json:"value"`
}

// SettingField defines one declarative setting item for mode="schema".
type SettingField struct {
	Key          string          `json:"key"`
	Label        string          `json:"label"`
	Description  string          `json:"description,omitempty"`
	Type         string          `json:"type"`
	Required     bool            `json:"required,omitempty"`
	Placeholder  string          `json:"placeholder,omitempty"`
	DefaultValue any             `json:"default_value,omitempty"`
	Options      []SettingOption `json:"options,omitempty"`
	Min          *float64        `json:"min,omitempty"`
	Max          *float64        `json:"max,omitempty"`
	Step         *float64        `json:"step,omitempty"`
}

// ContributesSpec declares UI slots, sidecar panels, agent capabilities, skills, and signal providers.
type ContributesSpec struct {
	UISlots           []UISlotContribution          `json:"ui_slots,omitempty"`
	SidecarPanels     []SidecarPanelContribution    `json:"sidecar_panels,omitempty"`
	AgentCapabilities []AgentCapabilityContribution `json:"agent_capabilities,omitempty"`
	AgentSkills       []string                      `json:"agent_skills,omitempty"`
	SignalProviders   []SignalProviderContribution  `json:"signal_providers,omitempty"`
}

// UISlotContribution registers a declarative item into an Astrorder UI slot.
type UISlotContribution struct {
	Slot              string           `json:"slot"`
	ID                string           `json:"id"`
	Renderer          string           `json:"renderer"`
	Label             string           `json:"label,omitempty"`
	Badge             string           `json:"badge,omitempty"`
	Icon              string           `json:"icon,omitempty"`
	PollEndpoint      string           `json:"poll_endpoint,omitempty"`
	PollIntervalMS    int              `json:"poll_interval_ms,omitempty"`
	RequiresWorkspace bool             `json:"requires_workspace,omitempty"`
	OnClick           *SlotClickAction `json:"on_click,omitempty"`
}

// SlotClickAction specifies what happens when a user clicks a declarative UI slot item.
type SlotClickAction struct {
	OpenPanel string `json:"open_panel,omitempty"`
	URL       string `json:"url,omitempty"`
	Endpoint  string `json:"endpoint,omitempty"`
}

// SidecarPanelContribution registers a sidecar viewer panel contributed by the plugin.
type SidecarPanelContribution struct {
	ID         string `json:"id"`
	Title      string `json:"title"`
	BadgeLabel string `json:"badge_label,omitempty"`
	TabColor   string `json:"tab_color,omitempty"`
	MediaType  string `json:"media_type,omitempty"`
	UIEntry    string `json:"ui_entry,omitempty"`
	Icon       string `json:"icon,omitempty"`
}

// AgentCapabilityContribution exposes a capability to Astrorder agents & agent CLI.
type AgentCapabilityContribution struct {
	ID             string         `json:"id"`
	CLIAlias       []string       `json:"cli_alias,omitempty"`
	Summary        string         `json:"summary"`
	Endpoint       string         `json:"endpoint,omitempty"`
	InputSchema    map[string]any `json:"input_schema,omitempty"`
	RequiresCaller bool           `json:"requires_caller,omitempty"`
}

// SignalProviderContribution registers a signal feed provider (e.g. workspace.failure_signals).
type SignalProviderContribution struct {
	Kind     string `json:"kind"`
	Endpoint string `json:"endpoint,omitempty"`
}

// IsKnownPermission reports whether perm is one of the canonical extension permissions.
func IsKnownPermission(perm string) bool {
	_, ok := knownPerms[strings.TrimSpace(perm)]
	return ok
}

// ValidatePluginID checks that an extension ID satisfies the strict identifier contract.
func ValidatePluginID(id string) error {
	clean := strings.TrimSpace(id)
	if !pluginIDPattern.MatchString(clean) {
		return fmt.Errorf("%w: plugin id %q must match ^[a-z0-9][a-z0-9._-]{1,63}$", ErrInvalid, id)
	}
	if strings.Contains(clean, "..") || clean == "_sdk" {
		return fmt.Errorf("%w: plugin id %q is reserved or contains invalid sequence", ErrInvalid, id)
	}
	return nil
}

// LoadManifestFile reads, parses, normalizes, and validates a plugin.json file.
func LoadManifestFile(manifestPath string) (Manifest, error) {
	raw, err := os.ReadFile(manifestPath)
	if err != nil {
		return Manifest{}, fmt.Errorf("%w: failed to read plugin.json: %v", ErrInvalid, err)
	}
	return ParseManifest(raw)
}

// ParseManifest unmarshals, normalizes, and validates plugin.json bytes.
func ParseManifest(raw []byte) (Manifest, error) {
	var m Manifest
	if err := json.Unmarshal(raw, &m); err != nil {
		return Manifest{}, fmt.Errorf("%w: invalid plugin.json: %v", ErrInvalid, err)
	}
	m.Normalize()
	if err := m.Validate(); err != nil {
		return Manifest{}, err
	}
	return m, nil
}

// Normalize fills sensible defaults and trims whitespace on manifest fields.
func (m *Manifest) Normalize() {
	m.ID = strings.TrimSpace(m.ID)
	m.Name = strings.TrimSpace(m.Name)
	m.Version = strings.TrimSpace(m.Version)
	m.Category = strings.TrimSpace(m.Category)
	if m.Category == "" {
		m.Category = "general"
	}
	m.Description = strings.TrimSpace(m.Description)
	m.Author = strings.TrimSpace(m.Author)
	m.Homepage = strings.TrimSpace(m.Homepage)
	m.Icon = strings.TrimSpace(m.Icon)

	if m.SchemaVersion == 0 {
		m.SchemaVersion = 1
	}

	m.Backend.Type = strings.ToLower(strings.TrimSpace(m.Backend.Type))
	if m.Backend.Type == "" {
		m.Backend.Type = BackendTypeNone
	}
	m.Backend.Health = strings.TrimSpace(m.Backend.Health)
	if m.Backend.Type == BackendTypeProcess && m.Backend.Health == "" {
		m.Backend.Health = "/health"
	}
	m.Backend.URL = strings.TrimRight(strings.TrimSpace(m.Backend.URL), "/")

	m.Settings.Mode = strings.ToLower(strings.TrimSpace(m.Settings.Mode))
	if m.Settings.Mode == "" {
		m.Settings.Mode = SettingsModeNone
	}
	m.Settings.UIEntry = strings.TrimSpace(m.Settings.UIEntry)
	m.Settings.TestEndpoint = strings.TrimSpace(m.Settings.TestEndpoint)

	seenPerm := make(map[string]struct{}, len(m.Permissions))
	deduped := make([]string, 0, len(m.Permissions))
	for _, p := range m.Permissions {
		trimmed := strings.TrimSpace(p)
		if trimmed == "" {
			continue
		}
		if _, dup := seenPerm[trimmed]; dup {
			continue
		}
		seenPerm[trimmed] = struct{}{}
		deduped = append(deduped, trimmed)
	}
	m.Permissions = deduped
}

// Validate checks manifest fields, permissions, backend spec, settings, and path safety.
func (m Manifest) Validate() error {
	if m.SchemaVersion != 1 {
		return fmt.Errorf("%w: unsupported schema_version %d (expected 1)", ErrInvalid, m.SchemaVersion)
	}
	if err := ValidatePluginID(m.ID); err != nil {
		return err
	}
	if strings.TrimSpace(m.Name) == "" {
		return fmt.Errorf("%w: plugin name is required", ErrInvalid)
	}
	if strings.TrimSpace(m.Version) == "" {
		return fmt.Errorf("%w: plugin version is required", ErrInvalid)
	}
	for _, perm := range m.Permissions {
		if !IsKnownPermission(perm) {
			return fmt.Errorf("%w: unknown permission %q", ErrInvalid, perm)
		}
	}

	// Backend validation.
	switch m.Backend.Type {
	case BackendTypeBuiltin, BackendTypeNone:
		// No extra requirements.
	case BackendTypeProcess:
		if len(m.Backend.Command) == 0 || strings.TrimSpace(m.Backend.Command[0]) == "" {
			return fmt.Errorf("%w: process backend requires non-empty command", ErrInvalid)
		}
		for _, tok := range m.Backend.Command {
			if err := validateNoPathTraversalToken(tok); err != nil {
				return fmt.Errorf("%w: invalid backend command token %q: %v", ErrInvalid, tok, err)
			}
		}
		for _, tok := range m.Backend.Args {
			if err := validateNoPathTraversalToken(tok); err != nil {
				return fmt.Errorf("%w: invalid backend arg %q: %v", ErrInvalid, tok, err)
			}
		}
		if m.Backend.Health != "" && !strings.HasPrefix(m.Backend.Health, "/") {
			return fmt.Errorf("%w: backend health endpoint must start with '/'", ErrInvalid)
		}
	case BackendTypeHTTP:
		if m.Backend.URL == "" {
			return fmt.Errorf("%w: http backend requires url", ErrInvalid)
		}
		parsed, err := url.Parse(m.Backend.URL)
		if err != nil || parsed.Hostname() == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
			return fmt.Errorf("%w: invalid http backend url %q", ErrInvalid, m.Backend.URL)
		}
	default:
		return fmt.Errorf("%w: unsupported backend type %q", ErrInvalid, m.Backend.Type)
	}

	// Settings validation.
	switch m.Settings.Mode {
	case SettingsModeNone:
		// OK
	case SettingsModeSchema:
		seenKeys := map[string]struct{}{}
		for _, f := range m.Settings.Schema {
			key := strings.TrimSpace(f.Key)
			if key == "" {
				return fmt.Errorf("%w: setting field key is required", ErrInvalid)
			}
			if _, dup := seenKeys[key]; dup {
				return fmt.Errorf("%w: duplicate setting field key %q", ErrInvalid, key)
			}
			seenKeys[key] = struct{}{}
			switch strings.ToLower(strings.TrimSpace(f.Type)) {
			case "string", "secret", "boolean", "number", "select":
			default:
				return fmt.Errorf("%w: unsupported setting field type %q for key %q", ErrInvalid, f.Type, key)
			}
		}
	case SettingsModeCustomUI:
		if m.Backend.Type != BackendTypeBuiltin {
			if m.Settings.UIEntry == "" {
				return fmt.Errorf("%w: custom_ui settings require ui_entry", ErrInvalid)
			}
			if _, err := ValidateRelativePluginPath(m.Settings.UIEntry); err != nil {
				return fmt.Errorf("%w: invalid settings.ui_entry: %v", ErrInvalid, err)
			}
		} else if m.Settings.UIEntry != "" {
			if _, err := ValidateRelativePluginPath(m.Settings.UIEntry); err != nil {
				return fmt.Errorf("%w: invalid settings.ui_entry: %v", ErrInvalid, err)
			}
		}
	default:
		return fmt.Errorf("%w: unsupported settings mode %q", ErrInvalid, m.Settings.Mode)
	}

	// Sidecar panel validation.
	seenPanels := map[string]struct{}{}
	for _, panel := range m.Contributes.SidecarPanels {
		pid := strings.TrimSpace(panel.ID)
		if pid == "" {
			return fmt.Errorf("%w: sidecar panel id is required", ErrInvalid)
		}
		if _, dup := seenPanels[pid]; dup {
			return fmt.Errorf("%w: duplicate sidecar panel id %q", ErrInvalid, pid)
		}
		seenPanels[pid] = struct{}{}
		if strings.TrimSpace(panel.UIEntry) != "" {
			if _, err := ValidateRelativePluginPath(panel.UIEntry); err != nil {
				return fmt.Errorf("%w: invalid sidecar panel %q ui_entry: %v", ErrInvalid, pid, err)
			}
		}
	}

	// UI slots validation.
	for _, slot := range m.Contributes.UISlots {
		if strings.TrimSpace(slot.Slot) == "" || strings.TrimSpace(slot.ID) == "" {
			return fmt.Errorf("%w: ui_slot requires non-empty slot and id", ErrInvalid)
		}
	}

	// Agent skills path traversal validation.
	for _, skillPath := range m.Contributes.AgentSkills {
		if _, err := ValidateRelativePluginPath(skillPath); err != nil {
			return fmt.Errorf("%w: invalid agent_skills path %q: %v", ErrInvalid, skillPath, err)
		}
	}

	// Agent capabilities validation.
	seenCaps := map[string]struct{}{}
	for _, capItem := range m.Contributes.AgentCapabilities {
		cid := strings.TrimSpace(capItem.ID)
		if cid == "" || strings.ContainsAny(cid, "/\\ \t\r\n") {
			return fmt.Errorf("%w: invalid agent capability id %q", ErrInvalid, capItem.ID)
		}
		if _, dup := seenCaps[cid]; dup {
			return fmt.Errorf("%w: duplicate agent capability id %q", ErrInvalid, cid)
		}
		seenCaps[cid] = struct{}{}
	}

	// Signal providers validation.
	for _, sp := range m.Contributes.SignalProviders {
		if strings.TrimSpace(sp.Kind) == "" {
			return fmt.Errorf("%w: signal provider kind is required", ErrInvalid)
		}
	}

	return nil
}

// ValidateRelativePluginPath verifies that rel stays strictly inside the plugin directory
// with no path traversal, absolute prefixes, drive letters, backslashes, or null bytes.
func ValidateRelativePluginPath(rel string) (string, error) {
	trimmed := strings.TrimSpace(rel)
	if trimmed == "" {
		return "", fmt.Errorf("path is empty")
	}
	if strings.ContainsRune(trimmed, 0) {
		return "", fmt.Errorf("path contains null byte")
	}
	if strings.Contains(trimmed, "\\") {
		return "", fmt.Errorf("backslashes are not allowed in relative plugin path")
	}
	if strings.HasPrefix(trimmed, "/") || filepath.IsAbs(trimmed) {
		return "", fmt.Errorf("absolute path is not allowed")
	}
	if len(trimmed) >= 2 && trimmed[1] == ':' {
		return "", fmt.Errorf("drive-qualified path is not allowed")
	}
	for _, seg := range strings.Split(trimmed, "/") {
		if seg == ".." {
			return "", fmt.Errorf("path traversal '..' is not allowed")
		}
	}
	cleaned := path.Clean(trimmed)
	if cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, "../") || strings.HasPrefix(cleaned, "/") {
		return "", fmt.Errorf("path escapes plugin directory")
	}
	return cleaned, nil
}

func validateNoPathTraversalToken(tok string) error {
	if strings.ContainsRune(tok, 0) {
		return fmt.Errorf("contains null byte")
	}
	normalized := strings.ReplaceAll(tok, "\\", "/")
	for _, seg := range strings.Split(normalized, "/") {
		if seg == ".." {
			return fmt.Errorf("path traversal '..' is not allowed")
		}
	}
	return nil
}
