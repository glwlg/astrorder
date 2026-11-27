package desktopcontrol

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

const (
	// WindowsMCPBackendName identifies CursorTouch/Windows-MCP as the active Windows driver.
	WindowsMCPBackendName = "windows-mcp"

	// ExcludedWindowsMCPTools strips non-GUI shell/filesystem/registry tools so Agents
	// cannot bypass Astrorder's workspace boundaries via the desktop control plugin.
	ExcludedWindowsMCPTools = "PowerShell,FileSystem,Registry,Process,Scrape,Notification"
)

// CommandSpec describes the executable and arguments used to launch an MCP stdio server.
type CommandSpec struct {
	Command string
	Args    []string
	Env     []string
}

// WindowsMCPDriver manages a lazy, idle-reaped CursorTouch/Windows-MCP stdio subprocess.
type WindowsMCPDriver struct {
	readConfig func(context.Context) Config
	// commandBuilder is overridable in unit tests to run a mock MCP stdio server.
	commandBuilder func(cfg Config) (CommandSpec, error)

	mu         sync.Mutex
	client     *MCPStdioClient
	runningKey string
	runningCmd string
	idleTimer  *time.Timer
}

// NewWindowsMCPDriver constructs a WindowsMCPDriver backed by uvx + CursorTouch/Windows-MCP.
func NewWindowsMCPDriver(readConfig func(context.Context) Config) *WindowsMCPDriver {
	if readConfig == nil {
		readConfig = func(context.Context) Config { return DefaultConfig() }
	}
	return &WindowsMCPDriver{
		readConfig:     readConfig,
		commandBuilder: buildWindowsMCPCommand,
	}
}

func (d *WindowsMCPDriver) Platform() string    { return "windows" }
func (d *WindowsMCPDriver) BackendName() string { return WindowsMCPBackendName }

func (d *WindowsMCPDriver) Info(ctx context.Context) DriverInfo {
	cfg := d.readConfig(ctx)
	d.mu.Lock()
	running := d.client != nil && d.client.Alive()
	version := ""
	cmdSummary := d.runningCmd
	if running {
		version = d.client.ServerInfo().Version
	}
	d.mu.Unlock()

	if cmdSummary == "" {
		if spec, err := d.commandBuilder(cfg); err == nil {
			cmdSummary = spec.Command + " " + strings.Join(spec.Args, " ")
		} else {
			cmdSummary = cfg.UVXCommand + " " + cfg.PackageSpec
		}
	}

	detail := "已就绪（按需拉起 Windows-MCP 子进程）"
	if running {
		detail = "Windows-MCP 子进程运行中"
		if version != "" {
			detail += " (v" + version + ")"
		}
	}
	return DriverInfo{
		Platform:  d.Platform(),
		Backend:   d.BackendName(),
		Supported: true,
		Running:   running,
		Version:   version,
		Command:   cmdSummary,
		Detail:    detail,
	}
}

func (d *WindowsMCPDriver) Invoke(ctx context.Context, action string, payload map[string]any) (*CallResult, error) {
	toolName, args, err := mapActionToWindowsMCP(action, payload)
	if err != nil {
		return nil, err
	}

	cfg := d.readConfig(ctx)
	client, err := d.ensureClient(ctx, cfg)
	if err != nil {
		return nil, err
	}

	res, err := client.CallTool(ctx, toolName, args)
	if err != nil {
		// If the subprocess died unexpectedly mid-call, drop reference so subsequent calls recover.
		if !client.Alive() {
			d.dropClient(client)
		}
		return nil, err
	}
	d.touchIdleTimer(cfg.IdleTimeoutSec)

	return &CallResult{
		Platform: d.Platform(),
		Backend:  d.BackendName(),
		Action:   action,
		Text:     res.Text,
		Images:   res.Images,
		IsError:  res.IsError,
	}, nil
}

func (d *WindowsMCPDriver) TestConnection(ctx context.Context, override *Config) (DriverInfo, string, error) {
	cfg := d.readConfig(ctx)
	if override != nil {
		cfg = *override
	}
	spec, err := d.commandBuilder(cfg)
	if err != nil {
		return DriverInfo{
			Platform:  d.Platform(),
			Backend:   d.BackendName(),
			Supported: true,
			Running:   false,
			Detail:    err.Error(),
		}, "", err
	}

	// If an override differs from the live client, run a dedicated one-shot probe without disrupting active sessions.
	key := specKey(spec)
	d.mu.Lock()
	reuseLive := override == nil || (d.client != nil && d.client.Alive() && d.runningKey == key)
	d.mu.Unlock()

	var client *MCPStdioClient
	var ephemeral bool
	if reuseLive {
		client, err = d.ensureClient(ctx, cfg)
		if err != nil {
			return DriverInfo{
				Platform:  d.Platform(),
				Backend:   d.BackendName(),
				Supported: true,
				Running:   false,
				Command:   spec.Command + " " + strings.Join(spec.Args, " "),
				Detail:    err.Error(),
			}, "", err
		}
	} else {
		client, err = StartMCPStdio(ctx, spec.Command, spec.Args, spec.Env)
		if err != nil {
			return DriverInfo{
				Platform:  d.Platform(),
				Backend:   d.BackendName(),
				Supported: true,
				Running:   false,
				Command:   spec.Command + " " + strings.Join(spec.Args, " "),
				Detail:    err.Error(),
			}, "", err
		}
		ephemeral = true
		defer func() { _ = client.Close() }()
	}

	probe, err := client.CallTool(ctx, "DisplayInventory", map[string]any{})
	if err != nil {
		if !ephemeral && !client.Alive() {
			d.dropClient(client)
		}
		return DriverInfo{
			Platform:  d.Platform(),
			Backend:   d.BackendName(),
			Supported: true,
			Running:   client.Alive(),
			Version:   client.ServerInfo().Version,
			Command:   spec.Command + " " + strings.Join(spec.Args, " "),
			Detail:    err.Error(),
		}, "", err
	}
	if !ephemeral {
		d.touchIdleTimer(cfg.IdleTimeoutSec)
	}

	version := client.ServerInfo().Version
	summary := strings.TrimSpace(probe.Text)
	firstLine := summary
	if idx := strings.IndexByte(firstLine, '\n'); idx >= 0 {
		firstLine = strings.TrimSpace(firstLine[:idx])
	}
	msg := fmt.Sprintf("连接成功：Windows-MCP %s", formatVersion(version))
	if firstLine != "" {
		msg += " · " + firstLine
	}
	return DriverInfo{
		Platform:  d.Platform(),
		Backend:   d.BackendName(),
		Supported: true,
		Running:   !ephemeral && client.Alive(),
		Version:   version,
		Command:   spec.Command + " " + strings.Join(spec.Args, " "),
		Detail:    msg,
	}, msg, nil
}

func (d *WindowsMCPDriver) Close(_ context.Context) error {
	d.mu.Lock()
	client := d.client
	d.client = nil
	d.runningKey = ""
	d.runningCmd = ""
	if d.idleTimer != nil {
		d.idleTimer.Stop()
		d.idleTimer = nil
	}
	d.mu.Unlock()
	if client != nil {
		return client.Close()
	}
	return nil
}

func (d *WindowsMCPDriver) ensureClient(ctx context.Context, cfg Config) (*MCPStdioClient, error) {
	spec, err := d.commandBuilder(cfg)
	if err != nil {
		return nil, err
	}
	key := specKey(spec)

	d.mu.Lock()
	if d.client != nil && d.client.Alive() && d.runningKey == key {
		c := d.client
		d.mu.Unlock()
		return c, nil
	}
	old := d.client
	d.client = nil
	d.runningKey = ""
	d.runningCmd = ""
	if d.idleTimer != nil {
		d.idleTimer.Stop()
		d.idleTimer = nil
	}
	d.mu.Unlock()

	if old != nil {
		_ = old.Close()
	}

	client, err := StartMCPStdio(ctx, spec.Command, spec.Args, spec.Env)
	if err != nil {
		return nil, err
	}

	d.mu.Lock()
	if d.client != nil && d.client.Alive() && d.runningKey == key {
		existing := d.client
		d.mu.Unlock()
		_ = client.Close()
		return existing, nil
	}
	d.client = client
	d.runningKey = key
	d.runningCmd = spec.Command + " " + strings.Join(spec.Args, " ")
	d.mu.Unlock()

	d.touchIdleTimer(cfg.IdleTimeoutSec)
	return client, nil
}

func (d *WindowsMCPDriver) dropClient(target *MCPStdioClient) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.client == target {
		d.client = nil
		d.runningKey = ""
		d.runningCmd = ""
		if d.idleTimer != nil {
			d.idleTimer.Stop()
			d.idleTimer = nil
		}
	}
}

func (d *WindowsMCPDriver) touchIdleTimer(timeoutSec int) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.idleTimer != nil {
		d.idleTimer.Stop()
		d.idleTimer = nil
	}
	if timeoutSec <= 0 || d.client == nil {
		return
	}
	target := d.client
	d.idleTimer = time.AfterFunc(time.Duration(timeoutSec)*time.Second, func() {
		d.mu.Lock()
		if d.client != target {
			d.mu.Unlock()
			return
		}
		d.client = nil
		d.runningKey = ""
		d.runningCmd = ""
		d.idleTimer = nil
		d.mu.Unlock()
		_ = target.Close()
	})
}

func specKey(spec CommandSpec) string {
	return spec.Command + "\x00" + strings.Join(spec.Args, "\x00")
}

func formatVersion(v string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return "(已启动)"
	}
	if strings.HasPrefix(v, "v") || strings.HasPrefix(v, "V") {
		return v
	}
	return "v" + v
}

func buildWindowsMCPCommand(cfg Config) (CommandSpec, error) {
	uvxBin, err := resolveUVXBinary(cfg.UVXCommand)
	if err != nil {
		return CommandSpec{}, err
	}
	pkgSpec := strings.TrimSpace(cfg.PackageSpec)
	if pkgSpec == "" {
		pkgSpec = DefaultPackageSpec
	}
	args := []string{
		pkgSpec,
		"serve",
		"--transport", "stdio",
		"--exclude-tools", ExcludedWindowsMCPTools,
	}
	return CommandSpec{
		Command: uvxBin,
		Args:    args,
	}, nil
}

func resolveUVXBinary(configured string) (string, error) {
	raw := strings.TrimSpace(configured)
	if raw == "" {
		raw = DefaultUVXCommand
	}
	// Explicit path configured by user.
	if strings.ContainsAny(raw, `/\`) {
		if info, err := os.Stat(raw); err == nil && !info.IsDir() {
			return raw, nil
		}
		return "", fmt.Errorf("uvx executable not found at configured path %q", raw)
	}
	if found, err := exec.LookPath(raw); err == nil && found != "" {
		return found, nil
	}
	// Fallback probe for common user-local uv installation locations on Windows.
	if runtime.GOOS == "windows" && (strings.EqualFold(raw, "uvx") || strings.EqualFold(raw, "uvx.exe")) {
		candidates := []string{}
		if home, err := os.UserHomeDir(); err == nil && home != "" {
			candidates = append(candidates,
				filepath.Join(home, ".local", "bin", "uvx.exe"),
				filepath.Join(home, ".cargo", "bin", "uvx.exe"),
			)
		}
		if localAppData := strings.TrimSpace(os.Getenv("LOCALAPPDATA")); localAppData != "" {
			candidates = append(candidates,
				filepath.Join(localAppData, "Programs", "uv", "uvx.exe"),
				filepath.Join(localAppData, "uv", "uvx.exe"),
			)
		}
		for _, c := range candidates {
			if info, err := os.Stat(c); err == nil && !info.IsDir() {
				return c, nil
			}
		}
	}
	return "", fmt.Errorf("未找到 uvx 命令（%q）；请先在「设置 → 功能与插件 → 本地运行时与工具链」安装 uv，或在电脑控制插件配置中指定 uvx 完整路径", raw)
}

// mapActionToWindowsMCP translates Astrorder's OS-neutral desktop actions into
// exact CursorTouch/Windows-MCP v4.2.6 tool names and typed argument maps.
func mapActionToWindowsMCP(action string, payload map[string]any) (string, map[string]any, error) {
	if payload == nil {
		payload = map[string]any{}
	}
	switch strings.ToLower(strings.TrimSpace(action)) {
	case "displays":
		return "DisplayInventory", map[string]any{}, nil

	case "snapshot":
		args := map[string]any{}
		for _, key := range []string{"use_vision", "use_dom", "use_annotation", "use_ui_tree"} {
			if raw, ok := payload[key]; ok && raw != nil {
				if b, ok := coerceBool(raw); ok {
					args[key] = b
				}
			}
		}
		for _, key := range []string{"width_reference_line", "height_reference_line"} {
			if raw, ok := payload[key]; ok && raw != nil {
				if n, ok := coerceInt(raw); ok {
					args[key] = n
				}
			}
		}
		if raw, ok := payload["display"]; ok && raw != nil {
			if ds, ok := coerceIntSlice(raw); ok && len(ds) > 0 {
				args["display"] = ds
			}
		}
		return "Snapshot", args, nil

	case "screenshot":
		args := map[string]any{}
		if raw, ok := payload["use_annotation"]; ok && raw != nil {
			if b, ok := coerceBool(raw); ok {
				args["use_annotation"] = b
			}
		}
		for _, key := range []string{"width_reference_line", "height_reference_line"} {
			if raw, ok := payload[key]; ok && raw != nil {
				if n, ok := coerceInt(raw); ok {
					args[key] = n
				}
			}
		}
		if raw, ok := payload["display"]; ok && raw != nil {
			if ds, ok := coerceIntSlice(raw); ok && len(ds) > 0 {
				args["display"] = ds
			}
		}
		return "Screenshot", args, nil

	case "app":
		args := map[string]any{}
		mode := payloadString(payload, "mode")
		if mode == "" {
			mode = "launch"
		}
		args["mode"] = mode
		if name := payloadString(payload, "name"); name != "" {
			args["name"] = name
		}
		if exe := payloadString(payload, "executable"); exe != "" {
			args["executable"] = exe
		}
		if cwd := payloadString(payload, "cwd"); cwd != "" {
			args["cwd"] = cwd
		}
		if raw, ok := payload["window_loc"]; ok && raw != nil {
			pair, err := coerceIntPair(raw)
			if err != nil {
				return "", nil, err
			}
			args["window_loc"] = pair
		}
		if raw, ok := payload["window_size"]; ok && raw != nil {
			pair, err := coerceIntPair(raw)
			if err != nil {
				return "", nil, err
			}
			args["window_size"] = pair
		}
		if raw, ok := payload["args"]; ok && raw != nil {
			switch v := raw.(type) {
			case []any, []string:
				args["args"] = v
			case string:
				trimmed := strings.TrimSpace(v)
				if strings.HasPrefix(trimmed, "[") {
					var parsed []string
					if err := json.Unmarshal([]byte(trimmed), &parsed); err == nil {
						args["args"] = parsed
					} else {
						args["args"] = trimmed
					}
				} else if trimmed != "" {
					args["args"] = trimmed
				}
			}
		}
		return "App", args, nil

	case "click":
		args := map[string]any{}
		hasTarget := false
		if raw, ok := payload["loc"]; ok && raw != nil {
			pair, err := coerceIntPair(raw)
			if err != nil {
				return "", nil, err
			}
			args["loc"] = pair
			hasTarget = true
		}
		if raw, ok := payload["label"]; ok && raw != nil {
			n, ok := coerceInt(raw)
			if !ok {
				return "", nil, fmt.Errorf("%w: label must be an integer", ErrInvalidInput)
			}
			args["label"] = n
			hasTarget = true
		}
		if !hasTarget {
			return "", nil, fmt.Errorf("%w: click requires either 'label' or 'loc' ([x,y])", ErrInvalidInput)
		}
		if btn := payloadString(payload, "button"); btn != "" {
			args["button"] = btn
		}
		if raw, ok := payload["clicks"]; ok && raw != nil {
			if n, ok := coerceInt(raw); ok {
				args["clicks"] = n
			}
		}
		return "Click", args, nil

	case "type":
		text, hasText := payloadRawString(payload, "text")
		if !hasText {
			return "", nil, fmt.Errorf("%w: type requires 'text'", ErrInvalidInput)
		}
		args := map[string]any{
			"text": text,
		}
		if raw, ok := payload["loc"]; ok && raw != nil {
			pair, err := coerceIntPair(raw)
			if err != nil {
				return "", nil, err
			}
			args["loc"] = pair
		}
		if raw, ok := payload["label"]; ok && raw != nil {
			n, ok := coerceInt(raw)
			if !ok {
				return "", nil, fmt.Errorf("%w: label must be an integer", ErrInvalidInput)
			}
			args["label"] = n
		}
		if raw, ok := payload["clear"]; ok && raw != nil {
			if b, ok := coerceBool(raw); ok {
				args["clear"] = b
			}
		}
		if cp := payloadString(payload, "caret_position"); cp != "" {
			args["caret_position"] = cp
		}
		if raw, ok := payload["press_enter"]; ok && raw != nil {
			if b, ok := coerceBool(raw); ok {
				args["press_enter"] = b
			}
		}
		return "Type", args, nil

	case "scroll":
		args := map[string]any{}
		if raw, ok := payload["loc"]; ok && raw != nil {
			pair, err := coerceIntPair(raw)
			if err != nil {
				return "", nil, err
			}
			args["loc"] = pair
		}
		if raw, ok := payload["label"]; ok && raw != nil {
			if n, ok := coerceInt(raw); ok {
				args["label"] = n
			}
		}
		if st := payloadString(payload, "type", "scroll_type"); st != "" {
			args["type"] = st
		}
		if dir := payloadString(payload, "direction"); dir != "" {
			args["direction"] = dir
		}
		if raw, ok := payload["wheel_times"]; ok && raw != nil {
			if n, ok := coerceInt(raw); ok && n > 0 {
				args["wheel_times"] = n
			}
		}
		return "Scroll", args, nil

	case "move":
		args := map[string]any{}
		hasTarget := false
		if raw, ok := payload["loc"]; ok && raw != nil {
			pair, err := coerceIntPair(raw)
			if err != nil {
				return "", nil, err
			}
			args["loc"] = pair
			hasTarget = true
		}
		if raw, ok := payload["label"]; ok && raw != nil {
			n, ok := coerceInt(raw)
			if !ok {
				return "", nil, fmt.Errorf("%w: label must be an integer", ErrInvalidInput)
			}
			args["label"] = n
			hasTarget = true
		}
		if !hasTarget {
			return "", nil, fmt.Errorf("%w: move requires either 'label' or 'loc' ([x,y])", ErrInvalidInput)
		}
		if raw, ok := payload["drag"]; ok && raw != nil {
			if b, ok := coerceBool(raw); ok {
				args["drag"] = b
			}
		}
		if raw, ok := payload["from_loc"]; ok && raw != nil {
			pair, err := coerceIntPair(raw)
			if err != nil {
				return "", nil, err
			}
			args["from_loc"] = pair
		}
		if raw, ok := payload["duration"]; ok && raw != nil {
			if f, ok := coerceFloat(raw); ok && f >= 0 {
				args["duration"] = f
			}
		}
		return "Move", args, nil

	case "shortcut":
		sc := payloadString(payload, "shortcut", "keys")
		if sc == "" {
			return "", nil, fmt.Errorf("%w: shortcut requires 'shortcut' (e.g. 'ctrl+c')", ErrInvalidInput)
		}
		return "Shortcut", map[string]any{"shortcut": sc}, nil

	case "wait":
		if cond := payloadString(payload, "condition"); cond != "" {
			args := map[string]any{"condition": cond}
			if text := payloadString(payload, "text"); text != "" {
				args["text"] = text
			}
			if winName := payloadString(payload, "window_name"); winName != "" {
				args["window_name"] = winName
			}
			if raw, ok := payload["timeout"]; ok && raw != nil {
				if f, ok := coerceFloat(raw); ok && f > 0 {
					args["timeout"] = f
				}
			}
			if raw, ok := payload["interval"]; ok && raw != nil {
				if f, ok := coerceFloat(raw); ok && f > 0 {
					args["interval"] = f
				}
			}
			if raw, ok := payload["use_dom"]; ok && raw != nil {
				if b, ok := coerceBool(raw); ok {
					args["use_dom"] = b
				}
			}
			return "WaitFor", args, nil
		}
		dur := 1
		if raw, ok := payload["duration"]; ok && raw != nil {
			n, ok := coerceInt(raw)
			if !ok || n < 0 {
				return "", nil, fmt.Errorf("%w: wait duration must be a non-negative integer (seconds)", ErrInvalidInput)
			}
			dur = n
		} else if raw, ok := payload["seconds"]; ok && raw != nil {
			n, ok := coerceInt(raw)
			if !ok || n < 0 {
				return "", nil, fmt.Errorf("%w: wait seconds must be a non-negative integer", ErrInvalidInput)
			}
			dur = n
		}
		return "Wait", map[string]any{"duration": dur}, nil

	case "clipboard":
		mode := strings.ToLower(payloadString(payload, "mode"))
		if mode == "" {
			mode = "get"
		}
		if mode != "get" && mode != "set" {
			return "", nil, fmt.Errorf("%w: clipboard mode must be 'get' or 'set'", ErrInvalidInput)
		}
		args := map[string]any{"mode": mode}
		if mode == "set" {
			text, ok := payloadRawString(payload, "text")
			if !ok {
				return "", nil, fmt.Errorf("%w: clipboard mode='set' requires 'text'", ErrInvalidInput)
			}
			args["text"] = text
		}
		return "Clipboard", args, nil

	default:
		return "", nil, fmt.Errorf("%w: %q", ErrUnknownAction, action)
	}
}

// Keep unused-import-free verification that errors package is used when needed.
var _ = errors.Is
