package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// daemonStopNeedsConfirmation mirrors the Electron shell: active sessions or
// any non-running/unreadable daemon state require explicit user consent.
func daemonStopNeedsConfirmation(daemon map[string]any) (bool, string) {
	if daemon == nil {
		return true, "无法读取小内核权威状态。"
	}
	active, complete := daemon["active_sessions"].(map[string]any)
	if !complete {
		return true, "小内核状态未提供完整的活动会话明细。"
	}
	if len(active) > 0 {
		return true, fmt.Sprintf("当前有 %d 个运行中或等待审批的会话。", len(active))
	}
	if state, _ := daemon["state"].(string); state != "running" {
		return true, "无法确认小内核中的会话状态。"
	}
	return false, ""
}

type ShellInfo struct {
	Framework         string `json:"framework"`
	BackendConfigured bool   `json:"backendConfigured"`
	StateDir          string `json:"stateDir"`
	KernelManagement  bool   `json:"kernelManagement"`
}

type ShellService struct {
	app         *application.App
	window      *application.WebviewWindow
	config      Config
	ctl         *CtlClient
	smokeReport string
	quit        func()
	preview     *PreviewService
	statistics  *StatisticsView
	opMu        sync.Mutex
	isPackaged  bool
	refreshTray func()
}

func (s *ShellService) Info() ShellInfo {
	return ShellInfo{
		Framework:         "Wails v3.0.0-beta.28",
		BackendConfigured: s.config.Backend != nil,
		StateDir:          s.config.StateDir,
		KernelManagement:  s.ctl != nil,
	}
}

// Window controls
func (s *ShellService) Minimize() {
	if s.window != nil {
		s.window.Minimise()
	}
}

func (s *ShellService) Maximize() bool {
	if s.window != nil {
		s.window.ToggleMaximise()
		return s.window.IsMaximised()
	}
	return false
}

func (s *ShellService) Close() {
	if s.preview != nil {
		s.preview.close()
	}
	if s.window != nil {
		s.window.Hide()
	}
}

func (s *ShellService) IsMaximized() bool {
	if s.window != nil {
		return s.window.IsMaximised()
	}
	return false
}

func (s *ShellService) Flash(flash bool) {
	if s.window != nil {
		s.window.Flash(flash)
	}
}

// Services management
func (s *ShellService) Status() (map[string]any, error) {
	if s.ctl == nil {
		return nil, errors.New("kernel controller not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return s.ctl.Status(ctx)
}

// Run never accepts a confirmation flag from the page. Stopping or restarting
// the Session Daemon is confirmed by the user in a native dialog whenever the
// authoritative daemon state shows active sessions or cannot be read
// (AGENTS.md: ambiguous state counts as active).
func (s *ShellService) Run(target string, action string) (map[string]any, error) {
	if !s.opMu.TryLock() {
		return nil, errors.New("另一个服务操作正在执行")
	}
	defer s.opMu.Unlock()
	defer func() {
		if s.refreshTray != nil {
			s.refreshTray()
		}
	}()
	if s.ctl == nil {
		return nil, errors.New("kernel controller not configured")
	}
	if target == "daemon" && action == "update" {
		checkCtx, cancelCheck := context.WithTimeout(context.Background(), 10*time.Second)
		update, err := s.ctl.Run(checkCtx, target, "update-check", false)
		cancelCheck()
		if err != nil {
			return nil, err
		}
		if update["update_available"] != true {
			return update, nil
		}
	}
	confirmActive := false
	if target == "daemon" && (action == "stop" || action == "restart" || action == "update") {
		statusCtx, cancelStatus := context.WithTimeout(context.Background(), 10*time.Second)
		status, err := s.ctl.Status(statusCtx)
		cancelStatus()
		var daemon map[string]any
		if err == nil {
			daemon, _ = status["daemon"].(map[string]any)
		}
		if needed, detail := daemonStopNeedsConfirmation(daemon); needed {
			if !s.confirm(action, detail) {
				return map[string]any{"ok": false, "state": "cancelled", "message": "用户取消了小内核操作"}, nil
			}
			confirmActive = true
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	return s.ctl.Run(ctx, target, action, confirmActive)
}

func (s *ShellService) confirm(action, detail string) bool {
	if s.app == nil || s.app.Dialog == nil {
		return false
	}
	verb := "停止"
	if action == "restart" {
		verb = "重启"
	} else if action == "update" {
		verb = "更新并重启"
	}
	approved := false
	dialog := s.app.Dialog.Question().
		SetTitle("确认操作小内核").
		SetMessage(fmt.Sprintf("确认%s小内核？\n%s此操作可能中断任务。", verb, detail))
	// Windows maps MessageBox results to these exact labels.
	dialog.AddButton("Yes").OnClick(func() { approved = true })
	no := dialog.AddButton("No")
	dialog.SetDefaultButton(no)
	dialog.SetCancelButton(no)
	if s.window != nil {
		dialog.AttachToWindow(s.window)
	}
	dialog.Show()
	return approved
}

// CopyAccessToken writes the browser credential to the clipboard. The token is
// never returned to page JavaScript.
func (s *ShellService) CopyAccessToken() (bool, error) {
	if s.ctl == nil {
		return false, errors.New("kernel controller not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	token, err := s.ctl.Login(ctx)
	if err != nil {
		return false, err
	}
	if s.app == nil || s.app.Clipboard == nil {
		return false, errors.New("clipboard not available")
	}
	return s.app.Clipboard.SetText(token), nil
}

// Startup settings
func (s *ShellService) GetStartupSettings() (StartupSettings, error) {
	return getStartupSettings(s.isPackaged)
}

func (s *ShellService) SetStartupEnabled(enabled bool) (StartupSettings, error) {
	return setStartupEnabled(s.isPackaged, enabled)
}

// Notifications
func (s *ShellService) Notify(title, body string) bool {
	if s.window != nil {
		s.window.Flash(true)
	}

	// Agent-controlled text reaches PowerShell only through environment
	// variables; it is never interpolated into the script source.
	if runtime.GOOS == "windows" {
		go func() {
			const psScript = `
[Windows.UI.Notifications.ToastNotificationManager, Windows.UI.Notifications, ContentType = WindowsRuntime] > $null
$template = [Windows.UI.Notifications.ToastNotificationManager]::GetTemplateContent([Windows.UI.Notifications.ToastTemplateType]::ToastText02)
$textNodes = $template.GetElementsByTagName("text")
$textNodes.Item(0).AppendChild($template.CreateTextNode($env:ASTRORDER_TOAST_TITLE)) > $null
$textNodes.Item(1).AppendChild($template.CreateTextNode($env:ASTRORDER_TOAST_BODY)) > $null
$toast = [Windows.UI.Notifications.ToastNotification]::new($template)
[Windows.UI.Notifications.ToastNotificationManager]::CreateToastNotifier("Astrorder").Show($toast)
`
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, "powershell", "-NoProfile", "-NonInteractive", "-STA", "-Command", psScript)
			cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}
			cmd.Env = append(os.Environ(), "ASTRORDER_TOAST_TITLE="+title, "ASTRORDER_TOAST_BODY="+body)
			_ = cmd.Run()
		}()
	}
	return true
}

// Dialogs
func (s *ShellService) OpenDirectoryDialog(defaultPath string) (string, error) {
	if s.app == nil || s.app.Dialog == nil {
		return "", errors.New("dialog service not available")
	}
	d := s.app.Dialog.OpenFile().
		CanChooseDirectories(true).
		CanChooseFiles(false).
		SetTitle("选择目录")
	if defaultPath != "" {
		d.SetDirectory(defaultPath)
	}
	return d.PromptForSingleSelection()
}

func (s *ShellService) OpenFileDialog(defaultPath string) (string, error) {
	if s.app == nil || s.app.Dialog == nil {
		return "", errors.New("dialog service not available")
	}
	d := s.app.Dialog.OpenFile().
		CanChooseFiles(true).
		CanChooseDirectories(false).
		SetTitle("选择文件")
	if defaultPath != "" {
		d.SetDirectory(defaultPath)
	}
	return d.PromptForSingleSelection()
}

func (s *ShellService) SaveImageDialog(defaultFilename string) (string, error) {
	if s.app == nil || s.app.Dialog == nil {
		return "", errors.New("dialog service not available")
	}
	if defaultFilename == "" {
		defaultFilename = fmt.Sprintf("astrorder-image-%d.png", time.Now().UnixMilli())
	}
	d := s.app.Dialog.SaveFile().
		SetMessage("保存图片").
		SetFilename(defaultFilename).
		AddFilter("PNG 图像 (*.png)", "*.png").
		AddFilter("JPEG 图像 (*.jpg;*.jpeg)", "*.jpg;*.jpeg").
		AddFilter("所有文件 (*.*)", "*.*")
	return d.PromptForSingleSelection()
}

func (s *ShellService) SetClipboardFiles(paths []string) (bool, error) {
	if runtime.GOOS == "windows" {
		return setClipboardFilesWindows(paths)
	}
	return false, errors.New("setClipboardFiles only supported on windows")
}

// OpenExternal only hands absolute http(s) URLs to the system browser; file:,
// shell: and bare paths would otherwise launch arbitrary local programs.
func (s *ShellService) OpenExternal(targetURL string) error {
	parsed, err := url.Parse(strings.TrimSpace(targetURL))
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return fmt.Errorf("refusing to open non-http(s) URL")
	}
	if s.app == nil || s.app.Browser == nil {
		return errors.New("browser integration not available")
	}
	return s.app.Browser.OpenURL(parsed.String())
}

// ReportReady is called by the loaded page, proving JS-to-Go transport.
func (s *ShellService) ReportReady() error {
	if s.smokeReport == "" {
		return nil
	}
	data, err := json.Marshal(map[string]any{
		"started":          true,
		"bindingReady":     true,
		"framework":        s.Info().Framework,
		"kernelManagement": s.Info().KernelManagement,
	})
	if err != nil {
		return err
	}
	if err := os.WriteFile(s.smokeReport, data, 0600); err != nil {
		return err
	}
	time.AfterFunc(250*time.Millisecond, s.quit)
	return nil
}
