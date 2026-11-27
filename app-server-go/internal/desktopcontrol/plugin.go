package desktopcontrol

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"astrorder.dev/app-server/internal/pluginhost"
)

//go:embed SKILL.md
var desktopControlSkillMD string

const (
	// PluginID is the canonical extension ID for the builtin Desktop Control plugin.
	PluginID = "desktop-control"

	maxExtBodyBytes = 1 << 20
)

var (
	_ pluginhost.BuiltinPlugin        = (*Service)(nil)
	_ pluginhost.BuiltinSkillProvider = (*Service)(nil)
	_ pluginhost.BuiltinLifecycle     = (*Service)(nil)
)

// Options configures the desktop-control builtin plugin service.
type Options struct {
	// ReadConfig loads persisted plugin KV values (typically backed by pluginhost.Manager.GetConfig).
	ReadConfig func(ctx context.Context) (map[string]any, error)
	// Driver optionally injects a custom Driver (e.g. fake driver in unit tests).
	// When nil, the platform default driver is selected (Windows-MCP on Windows, stub on other OSes).
	Driver Driver
	Logger *slog.Logger
}

// Service implements Astrorder's platform-abstracted Desktop Control builtin plugin.
type Service struct {
	readConfigFn func(ctx context.Context) (map[string]any, error)
	driver       Driver
	logger       *slog.Logger
}

// NewService creates a new Desktop Control plugin service.
func NewService(opts Options) *Service {
	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}
	svc := &Service{
		readConfigFn: opts.ReadConfig,
		logger:       logger,
	}
	if opts.Driver != nil {
		svc.driver = opts.Driver
	} else {
		svc.driver = newPlatformDriver(svc.EffectiveConfig)
	}
	return svc
}

// EffectiveConfig returns the normalized plugin Config from persisted KV values.
func (s *Service) EffectiveConfig(ctx context.Context) Config {
	if s.readConfigFn == nil {
		return DefaultConfig()
	}
	values, err := s.readConfigFn(ctx)
	if err != nil {
		return DefaultConfig()
	}
	return ParseConfig(values)
}

// Skills returns the embedded astrorder-desktop-control SKILL.md when the plugin is enabled.
func (s *Service) Skills() []pluginhost.PluginSkillEntry {
	return []pluginhost.PluginSkillEntry{
		{
			PluginID:    PluginID,
			Name:        "astrorder-desktop-control",
			DisplayName: "电脑控制（桌面 GUI 自动化）",
			Description: "通过星序查看宿主机桌面屏幕、UI 控件树并操作原生桌面应用（Windows 端基于 Windows-MCP，已预留 macOS 驱动接口）",
			Content:     desktopControlSkillMD,
		},
	}
}

// Manifest returns the declarative extension manifest for the Desktop Control builtin plugin.
func (s *Service) Manifest() pluginhost.Manifest {
	defaultOff := false
	minIdle := 0.0
	maxIdle := 3600.0
	stepIdle := 30.0

	return pluginhost.Manifest{
		SchemaVersion:  1,
		ID:             PluginID,
		Name:           "电脑控制",
		Version:        "1.0.0",
		Category:       "workspace",
		Description:    "让 Agent 通过统一抽象层查看与操控宿主机桌面应用（支持截屏、UI 控件树定位、点击、输入、快捷键与剪贴板）。Windows 端内置包装 CursorTouch/Windows-MCP 并裁剪高危系统工具，预留 macOS 驱动扩展位；默认关闭，开启后自动下发技能。",
		Author:         "Astrorder",
		Homepage:       "https://github.com/CursorTouch/Windows-MCP",
		Icon:           "device-desktop",
		DefaultEnabled: &defaultOff,
		Permissions: []string{
			pluginhost.PermAgentCapabilities,
			pluginhost.PermStorageKV,
			pluginhost.PermDesktopControl,
		},
		Backend: pluginhost.BackendSpec{
			Type: pluginhost.BackendTypeBuiltin,
		},
		Settings: pluginhost.SettingsSpec{
			Mode:         pluginhost.SettingsModeSchema,
			TestEndpoint: "/test-connection",
			Schema: []pluginhost.SettingField{
				{
					Key:          "mode",
					Label:        "控制模式",
					Description:  "「完整交互」允许点击、输入与快捷键；「仅观察（只读）」仅允许查看显示器、控件树、截屏与读取剪贴板",
					Type:         "select",
					DefaultValue: ModeInteractive,
					Options: []pluginhost.SettingOption{
						{Label: "完整交互（可操作桌面）", Value: ModeInteractive},
						{Label: "仅观察（只读，禁止输入与点击）", Value: ModeReadOnly},
					},
				},
				{
					Key:          "blocked_shortcuts",
					Label:        "禁止的快捷键组合",
					Description:  "逗号分隔的高危快捷键黑名单（忽略大小写与键序），命中时由星序安全层直接拦截",
					Type:         "string",
					Placeholder:  DefaultBlockedShortcuts,
					DefaultValue: DefaultBlockedShortcuts,
				},
				{
					Key:          "uvx_command",
					Label:        "uvx 可执行路径",
					Description:  "默认自动查找 PATH 与 ~/.local/bin/uvx.exe；如自定义安装位置可填写绝对路径",
					Type:         "string",
					Placeholder:  DefaultUVXCommand,
					DefaultValue: DefaultUVXCommand,
				},
				{
					Key:          "package_spec",
					Label:        "Windows-MCP 包规格",
					Description:  "传给 uvx 的包名或固定版本（例如 windows-mcp 或 windows-mcp==4.2.6）",
					Type:         "string",
					Placeholder:  DefaultPackageSpec,
					DefaultValue: DefaultPackageSpec,
				},
				{
					Key:          "idle_timeout_sec",
					Label:        "空闲自动回收时间（秒）",
					Description:  "超过该时长无调用时自动关闭后台桌面驱动子进程以节省内存（0 表示不自动回收）",
					Type:         "number",
					DefaultValue: DefaultIdleTimeoutSec,
					Min:          &minIdle,
					Max:          &maxIdle,
					Step:         &stepIdle,
				},
			},
		},
		Contributes: pluginhost.ContributesSpec{
			AgentCapabilities: []pluginhost.AgentCapabilityContribution{
				{
					ID:          "status",
					Summary:     "Inspect desktop control driver platform, backend version, running state, safety mode, and blocked shortcut policy.",
					InputSchema: map[string]any{},
				},
				{
					ID:          "displays",
					Summary:     "List connected host displays including monitor bounds, work area, resolution, primary flag, DPI, and scale.",
					InputSchema: map[string]any{},
				},
				{
					ID:      "snapshot",
					Summary: "Capture host desktop accessibility UI tree with labeled interactive elements and optional annotated screenshot.",
					InputSchema: map[string]any{
						"use_vision":            "optional bool (default false): attach screenshot image",
						"use_annotation":        "optional bool (default true): draw numbered element labels on screenshot",
						"use_ui_tree":           "optional bool (default true): include interactive/scrollable UI tree text",
						"use_dom":               "optional bool (default false): extract browser DOM elements when focused on a browser",
						"display":               "optional display index list (e.g. [0] or '0,1')",
						"width_reference_line":  "optional int reference grid columns",
						"height_reference_line": "optional int reference grid rows",
					},
				},
				{
					ID:      "screenshot",
					Summary: "Capture host desktop screenshot image (materialized to local file path by astrorder-agent).",
					InputSchema: map[string]any{
						"use_annotation":        "optional bool (default false): annotate UI elements",
						"display":               "optional display index list (e.g. [0] or '0,1')",
						"width_reference_line":  "optional int reference grid columns",
						"height_reference_line": "optional int reference grid rows",
					},
				},
				{
					ID:      "app",
					Summary: "Launch, switch focus to, or resize a native desktop application window.",
					InputSchema: map[string]any{
						"mode":        "action mode: launch | launch_executable | switch | resize (default launch)",
						"name":        "application or window name",
						"window_loc":  "optional [x,y] target top-left position for resize",
						"window_size": "optional [width,height] target size for resize",
						"executable":  "optional executable path for mode=launch_executable",
						"args":        "optional arguments array/string for mode=launch_executable",
						"cwd":         "optional working directory for mode=launch_executable",
					},
				},
				{
					ID:      "click",
					Summary: "Click or hover on a UI element by snapshot label (preferred) or screen coordinates [x,y].",
					InputSchema: map[string]any{
						"label":  "optional integer element label from snapshot (preferred)",
						"loc":    "optional [x,y] coordinates (accepts [x,y] or 'x,y')",
						"button": "optional mouse button: left | right | middle (default left)",
						"clicks": "optional click count: 0=hover, 1=single, 2=double (default 1)",
					},
				},
				{
					ID:      "type",
					Summary: "Type text into a UI element by label or coordinates, optionally clearing first or pressing Enter.",
					InputSchema: map[string]any{
						"text":           "required text to type",
						"label":          "optional integer element label from snapshot",
						"loc":            "optional [x,y] coordinates",
						"clear":          "optional bool: select-all and clear existing text first",
						"caret_position": "optional: start | idle | end (default idle)",
						"press_enter":    "optional bool: press Enter after typing",
					},
				},
				{
					ID:      "scroll",
					Summary: "Scroll vertically or horizontally at a UI element or screen coordinates.",
					InputSchema: map[string]any{
						"label":       "optional integer element label from snapshot",
						"loc":         "optional [x,y] coordinates",
						"type":        "optional scroll axis: vertical | horizontal (default vertical)",
						"direction":   "optional direction: up | down | left | right (default down)",
						"wheel_times": "optional integer number of scroll wheel ticks (default 1)",
					},
				},
				{
					ID:      "move",
					Summary: "Move mouse cursor or drag-and-drop to a UI element or screen coordinates.",
					InputSchema: map[string]any{
						"label":    "optional integer target element label from snapshot",
						"loc":      "optional target [x,y] coordinates",
						"drag":     "optional bool: hold left button while moving",
						"from_loc": "optional start [x,y] coordinates when drag=true",
						"duration": "optional movement duration in seconds",
					},
				},
				{
					ID:      "shortcut",
					Summary: "Press a keyboard shortcut combination (e.g. 'ctrl+c', 'alt+tab'), subject to Astrorder's blocked shortcut policy.",
					InputSchema: map[string]any{
						"shortcut": "required key combination string (e.g. 'ctrl+s', 'alt+tab')",
					},
				},
				{
					ID:      "wait",
					Summary: "Pause for a fixed duration (seconds) or wait until a UI condition is met (text_exists, active_window, element_exists, element_enabled, focused_element).",
					InputSchema: map[string]any{
						"duration":    "integer wait duration in seconds (when condition is omitted)",
						"condition":   "optional UI wait condition: text_exists | active_window | element_exists | element_enabled | focused_element",
						"text":        "optional text or element name to match for condition",
						"window_name": "optional window title for active_window condition",
						"timeout":     "optional max wait seconds for condition (default 10)",
						"interval":    "optional poll interval seconds (default 0.25)",
						"use_dom":     "optional bool: search browser DOM for condition",
					},
				},
				{
					ID:      "clipboard",
					Summary: "Read or set host system clipboard text ('set' requires interactive mode).",
					InputSchema: map[string]any{
						"mode": "get | set (default get)",
						"text": "required text when mode='set'",
					},
				},
			},
		},
	}
}

// ServeExtension serves /api/v1/ext/desktop-control/status and /test-connection.
func (s *Service) ServeExtension(w http.ResponseWriter, r *http.Request, subpath string) {
	clean := "/" + strings.Trim(strings.TrimSpace(subpath), "/")
	switch clean {
	case "/status":
		if r.Method != http.MethodGet {
			writeJSONError(w, http.StatusMethodNotAllowed, "Method Not Allowed")
			return
		}
		writeJSON(w, http.StatusOK, s.buildStatus(r.Context()))
	case "/test-connection":
		if r.Method != http.MethodPost {
			writeJSONError(w, http.StatusMethodNotAllowed, "Method Not Allowed")
			return
		}
		s.handleTestConnection(w, r)
	default:
		writeJSONError(w, http.StatusNotFound, "Not Found")
	}
}

// InvokeCapability executes an OS-neutral desktop control action after enforcing
// Astrorder's cross-platform safety policies (read-only mode + blocked shortcuts).
func (s *Service) InvokeCapability(ctx context.Context, capID string, payload map[string]any) (any, error) {
	action := strings.ToLower(strings.TrimSpace(capID))
	if action == "status" {
		return s.buildStatus(ctx), nil
	}

	// Validate action name before checking mode so typos surface clearly.
	switch action {
	case "displays", "snapshot", "screenshot", "app", "click", "type", "scroll", "move", "shortcut", "wait", "clipboard":
	default:
		return nil, fmt.Errorf("%w: %q", ErrUnknownAction, capID)
	}

	cfg := s.EffectiveConfig(ctx)
	if cfg.Mode == ModeReadOnly && IsMutatingAction(action, payload) {
		return nil, fmt.Errorf("%w: action %q is blocked while mode=%s (switch to interactive mode in plugin settings)", ErrReadOnlyMode, action, cfg.Mode)
	}
	if action == "shortcut" {
		sc := payloadString(payload, "shortcut", "keys")
		if err := CheckBlockedShortcut(sc, cfg.BlockedShortcuts); err != nil {
			return nil, err
		}
	}

	return s.driver.Invoke(ctx, action, payload)
}

// CollectSignals returns no signals for the desktop-control plugin.
func (s *Service) CollectSignals(_ context.Context, _ string, _ pluginhost.SignalQuery) ([]pluginhost.SignalItem, error) {
	return nil, nil
}

// OnEnabledChanged immediately tears down any running desktop driver subprocess when disabled.
func (s *Service) OnEnabledChanged(ctx context.Context, enabled bool) {
	if !enabled {
		if err := s.driver.Close(ctx); err != nil && s.logger != nil {
			s.logger.Warn("failed to close desktop control driver on disable", "error", err)
		}
	}
}

// Close releases the underlying desktop driver process on server shutdown.
func (s *Service) Close(ctx context.Context) error {
	return s.driver.Close(ctx)
}

func (s *Service) buildStatus(ctx context.Context) map[string]any {
	cfg := s.EffectiveConfig(ctx)
	info := s.driver.Info(ctx)
	return map[string]any{
		"plugin_id":         PluginID,
		"platform":          info.Platform,
		"backend":           info.Backend,
		"supported":         info.Supported,
		"running":           info.Running,
		"version":           info.Version,
		"command":           info.Command,
		"detail":            info.Detail,
		"mode":              cfg.Mode,
		"blocked_shortcuts": cfg.BlockedShortcuts,
		"idle_timeout_sec":  cfg.IdleTimeoutSec,
		"excluded_tools":    ExcludedWindowsMCPTools,
	}
}

func (s *Service) handleTestConnection(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Values map[string]any `json:"values"`
	}
	if r.Body != nil {
		raw, err := io.ReadAll(io.LimitReader(r.Body, maxExtBodyBytes))
		if err == nil && len(strings.TrimSpace(string(raw))) > 0 {
			_ = json.Unmarshal(raw, &body)
		}
	}

	var override *Config
	if len(body.Values) > 0 {
		merged := DefaultConfig()
		if s.readConfigFn != nil {
			if saved, err := s.readConfigFn(r.Context()); err == nil {
				merged = ParseConfig(saved)
			}
		}
		// Apply draft overrides on top of saved config.
		combined := map[string]any{
			"mode":              merged.Mode,
			"blocked_shortcuts": merged.BlockedShortcuts,
			"uvx_command":       merged.UVXCommand,
			"package_spec":      merged.PackageSpec,
			"idle_timeout_sec":  merged.IdleTimeoutSec,
		}
		for k, v := range body.Values {
			combined[k] = v
		}
		parsed := ParseConfig(combined)
		override = &parsed
	}

	probeCtx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
	defer cancel()

	info, msg, err := s.driver.TestConnection(probeCtx, override)
	if err != nil {
		detail := msg
		if detail == "" {
			detail = err.Error()
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":        false,
			"message":   detail,
			"platform":  info.Platform,
			"backend":   info.Backend,
			"supported": info.Supported,
			"version":   info.Version,
			"command":   info.Command,
		})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"ok":        true,
		"message":   msg,
		"platform":  info.Platform,
		"backend":   info.Backend,
		"supported": info.Supported,
		"running":   info.Running,
		"version":   info.Version,
		"command":   info.Command,
	})
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func writeJSONError(w http.ResponseWriter, status int, detail string) {
	writeJSON(w, status, map[string]string{"detail": detail})
}

var _ = errors.Is
