package desktopcontrol

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

const (
	// ModeInteractive allows both inspection and UI input actions (click, type, shortcut, etc.).
	ModeInteractive = "interactive"
	// ModeReadOnly allows only observation actions (status, displays, snapshot, screenshot, wait, clipboard get).
	ModeReadOnly = "read_only"

	DefaultUVXCommand       = "uvx"
	DefaultPackageSpec      = "windows-mcp"
	DefaultIdleTimeoutSec   = 300
	DefaultBlockedShortcuts = "win+r,alt+f4,ctrl+alt+del,win+l"
)

var (
	ErrUnsupportedPlatform = errors.New("desktop control is not supported on this platform yet")
	ErrReadOnlyMode        = errors.New("desktop control is currently in read-only mode")
	ErrBlockedShortcut     = errors.New("shortcut is blocked by desktop control safety policy")
	ErrInvalidInput        = errors.New("invalid desktop control input")
	ErrUnknownAction       = errors.New("unknown desktop control capability")
)

// Config holds normalized runtime settings for the desktop-control plugin.
type Config struct {
	Mode             string   `json:"mode"`
	BlockedShortcuts []string `json:"blocked_shortcuts"`
	UVXCommand       string   `json:"uvx_command"`
	PackageSpec      string   `json:"package_spec"`
	IdleTimeoutSec   int      `json:"idle_timeout_sec"`
}

// DefaultConfig returns safe default settings for the desktop-control plugin.
func DefaultConfig() Config {
	return Config{
		Mode:             ModeInteractive,
		BlockedShortcuts: parseBlockedShortcuts(DefaultBlockedShortcuts),
		UVXCommand:       DefaultUVXCommand,
		PackageSpec:      DefaultPackageSpec,
		IdleTimeoutSec:   DefaultIdleTimeoutSec,
	}
}

// ParseConfig overlays persisted plugin KV values onto DefaultConfig.
func ParseConfig(values map[string]any) Config {
	cfg := DefaultConfig()
	if len(values) == 0 {
		return cfg
	}
	if rawMode, ok := values["mode"].(string); ok {
		switch strings.ToLower(strings.TrimSpace(rawMode)) {
		case ModeReadOnly:
			cfg.Mode = ModeReadOnly
		case ModeInteractive:
			cfg.Mode = ModeInteractive
		}
	}
	if rawBlocked, exists := values["blocked_shortcuts"]; exists && rawBlocked != nil {
		switch v := rawBlocked.(type) {
		case string:
			cfg.BlockedShortcuts = parseBlockedShortcuts(v)
		case []string:
			cfg.BlockedShortcuts = normalizeBlockedShortcutList(v)
		case []any:
			parts := make([]string, 0, len(v))
			for _, item := range v {
				if s, ok := item.(string); ok {
					parts = append(parts, s)
				}
			}
			cfg.BlockedShortcuts = normalizeBlockedShortcutList(parts)
		}
	}
	if rawUVX, ok := values["uvx_command"].(string); ok && strings.TrimSpace(rawUVX) != "" {
		cfg.UVXCommand = strings.TrimSpace(rawUVX)
	}
	if rawPkg, ok := values["package_spec"].(string); ok && strings.TrimSpace(rawPkg) != "" {
		cfg.PackageSpec = strings.TrimSpace(rawPkg)
	}
	if rawIdle, exists := values["idle_timeout_sec"]; exists && rawIdle != nil {
		if n, ok := coerceInt(rawIdle); ok && n >= 0 {
			if n > 3600 {
				n = 3600
			}
			cfg.IdleTimeoutSec = n
		}
	}
	return cfg
}

// DriverInfo describes the active OS backend and its health state.
type DriverInfo struct {
	Platform  string `json:"platform"`
	Backend   string `json:"backend"`
	Supported bool   `json:"supported"`
	Running   bool   `json:"running"`
	Version   string `json:"version,omitempty"`
	Command   string `json:"command,omitempty"`
	Detail    string `json:"detail,omitempty"`
}

// ImageBlock carries an image returned by a desktop backend (materialized to disk by agentcli).
type ImageBlock struct {
	MimeType   string `json:"mime_type"`
	DataBase64 string `json:"data_base64"`
}

// CallResult is the normalized OS-neutral payload returned by Driver.Invoke.
type CallResult struct {
	Platform string       `json:"platform"`
	Backend  string       `json:"backend"`
	Action   string       `json:"action"`
	Text     string       `json:"text,omitempty"`
	Images   []ImageBlock `json:"images,omitempty"`
	IsError  bool         `json:"is_error,omitempty"`
}

// Driver abstracts OS-specific desktop automation backends behind a single cross-platform contract.
// Windows uses CursorTouch/Windows-MCP; future macOS support can implement this interface
// without changing the plugin manifest, safety policies, agent CLI, or SKILL.md.
type Driver interface {
	Platform() string
	BackendName() string
	Info(ctx context.Context) DriverInfo
	Invoke(ctx context.Context, action string, payload map[string]any) (*CallResult, error)
	TestConnection(ctx context.Context, override *Config) (DriverInfo, string, error)
	Close(ctx context.Context) error
}

// IsMutatingAction reports whether action + payload modifies desktop state and must be
// rejected when the plugin is configured in read_only mode.
func IsMutatingAction(action string, payload map[string]any) bool {
	switch strings.ToLower(strings.TrimSpace(action)) {
	case "status", "displays", "snapshot", "screenshot", "wait":
		return false
	case "clipboard":
		mode := strings.ToLower(strings.TrimSpace(payloadString(payload, "mode")))
		return mode == "set"
	case "app", "click", "type", "scroll", "move", "shortcut":
		return true
	default:
		return true
	}
}

// NormalizeShortcut canonicalizes a keyboard shortcut expression so ordering, spacing,
// casing, and modifier aliases compare consistently (e.g. "Alt + F4" == "f4+alt").
func NormalizeShortcut(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}
	replacer := strings.NewReplacer(" ", "", "_", "", "-", "+")
	parts := strings.Split(strings.ToLower(replacer.Replace(trimmed)), "+")
	seen := make(map[string]struct{}, len(parts))
	canonical := make([]string, 0, len(parts))
	for _, part := range parts {
		if part == "" {
			continue
		}
		tok := canonicalKeyToken(part)
		if _, dup := seen[tok]; dup {
			continue
		}
		seen[tok] = struct{}{}
		canonical = append(canonical, tok)
	}
	sort.Strings(canonical)
	return strings.Join(canonical, "+")
}

func canonicalKeyToken(tok string) string {
	switch tok {
	case "control", "lctrl", "rctrl":
		return "ctrl"
	case "menu", "option", "lalt", "ralt":
		return "alt"
	case "windows", "super", "meta", "cmd", "command", "lwin", "rwin":
		return "win"
	case "delete":
		return "del"
	case "escape":
		return "esc"
	case "return":
		return "enter"
	case "lshift", "rshift":
		return "shift"
	default:
		return tok
	}
}

func parseBlockedShortcuts(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	return normalizeBlockedShortcutList(strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == ';' || r == '\n'
	}))
}

func normalizeBlockedShortcutList(items []string) []string {
	seen := make(map[string]struct{}, len(items))
	out := make([]string, 0, len(items))
	for _, item := range items {
		norm := NormalizeShortcut(item)
		if norm == "" {
			continue
		}
		if _, dup := seen[norm]; dup {
			continue
		}
		seen[norm] = struct{}{}
		out = append(out, norm)
	}
	return out
}

// CheckBlockedShortcut returns an ErrBlockedShortcut error if raw matches any blocked shortcut.
func CheckBlockedShortcut(raw string, blocked []string) error {
	norm := NormalizeShortcut(raw)
	if norm == "" {
		return nil
	}
	for _, b := range blocked {
		if NormalizeShortcut(b) == norm {
			return fmt.Errorf("%w: %q matches blocked policy %q", ErrBlockedShortcut, raw, b)
		}
	}
	return nil
}

// Flexible payload coercion helpers so both typed JSON (--json) and string CLI flags (--param / --flag) work cleanly.

func payloadString(payload map[string]any, keys ...string) string {
	for _, key := range keys {
		raw, ok := payload[key]
		if !ok || raw == nil {
			continue
		}
		switch v := raw.(type) {
		case string:
			if s := strings.TrimSpace(v); s != "" {
				return s
			}
		case json.Number:
			if s := strings.TrimSpace(v.String()); s != "" {
				return s
			}
		}
	}
	return ""
}

func payloadRawString(payload map[string]any, key string) (string, bool) {
	raw, ok := payload[key]
	if !ok || raw == nil {
		return "", false
	}
	if s, ok := raw.(string); ok {
		return s, true
	}
	return fmt.Sprint(raw), true
}

func coerceBool(raw any) (bool, bool) {
	switch v := raw.(type) {
	case bool:
		return v, true
	case string:
		b, err := strconv.ParseBool(strings.TrimSpace(v))
		return b, err == nil
	case int:
		return v != 0, true
	case int64:
		return v != 0, true
	case float64:
		return v != 0, true
	case json.Number:
		if n, err := v.Int64(); err == nil {
			return n != 0, true
		}
	}
	return false, false
}

func coerceInt(raw any) (int, bool) {
	switch v := raw.(type) {
	case int:
		return v, true
	case int64:
		return int(v), true
	case float64:
		return int(v), true
	case json.Number:
		if n, err := v.Int64(); err == nil {
			return int(n), true
		}
		if f, err := v.Float64(); err == nil {
			return int(f), true
		}
	case string:
		s := strings.TrimSpace(v)
		if s == "" {
			return 0, false
		}
		if n, err := strconv.Atoi(s); err == nil {
			return n, true
		}
		if f, err := strconv.ParseFloat(s, 64); err == nil {
			return int(f), true
		}
	}
	return 0, false
}

func coerceFloat(raw any) (float64, bool) {
	switch v := raw.(type) {
	case float64:
		return v, true
	case int:
		return float64(v), true
	case int64:
		return float64(v), true
	case json.Number:
		f, err := v.Float64()
		return f, err == nil
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
		return f, err == nil
	}
	return 0, false
}

func coerceIntSlice(raw any) ([]int, bool) {
	switch v := raw.(type) {
	case []int:
		return append([]int(nil), v...), true
	case []any:
		out := make([]int, 0, len(v))
		for _, item := range v {
			n, ok := coerceInt(item)
			if !ok {
				return nil, false
			}
			out = append(out, n)
		}
		return out, true
	case int, int64, float64, json.Number:
		if n, ok := coerceInt(v); ok {
			return []int{n}, true
		}
	case string:
		s := strings.TrimSpace(v)
		if s == "" {
			return nil, false
		}
		if strings.HasPrefix(s, "[") && strings.HasSuffix(s, "]") {
			var parsed []int
			if err := json.Unmarshal([]byte(s), &parsed); err == nil {
				return parsed, true
			}
			s = strings.TrimSpace(s[1 : len(s)-1])
		}
		if s == "" {
			return nil, false
		}
		parts := strings.Split(s, ",")
		out := make([]int, 0, len(parts))
		for _, p := range parts {
			n, ok := coerceInt(p)
			if !ok {
				return nil, false
			}
			out = append(out, n)
		}
		return out, true
	}
	return nil, false
}

func coerceIntPair(raw any) ([]int, error) {
	nums, ok := coerceIntSlice(raw)
	if !ok || len(nums) != 2 {
		return nil, fmt.Errorf("%w: expected 2-element integer coordinates [x, y], got %v", ErrInvalidInput, raw)
	}
	return nums, nil
}
