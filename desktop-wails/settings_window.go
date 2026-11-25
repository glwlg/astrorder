package main

import (
	"encoding/json"
	"errors"
	"net/url"
	"strings"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
)

var allowedSettingsTabs = map[string]bool{
	"general":      true,
	"connections":  true,
	"models":       true,
	"skills":       true,
	"rtk":          true,
	"network":      true,
	"shortcuts":    true,
	"appearance":   true,
	"desktop":      true,
	"integrations": true,
	"plugins":      true,
}

func sanitizeSettingsTab(tab string) string {
	trimmed := strings.TrimSpace(tab)
	if allowedSettingsTabs[trimmed] {
		return trimmed
	}
	return ""
}

func (s *ShellService) OpenSettings(tab string) error {
	if s.app == nil {
		return errors.New("desktop application not available")
	}
	cleanTab := sanitizeSettingsTab(tab)
	targetURL := "/settings"
	if cleanTab != "" {
		targetURL = "/settings?tab=" + url.QueryEscape(cleanTab)
	}

	s.settingsMu.Lock()
	win := s.settingsWindow
	if win == nil {
		win = s.app.Window.NewWithOptions(application.WebviewWindowOptions{
			Name:           "settings",
			Title:          "星序 · 设置",
			Width:          1280,
			Height:         820,
			MinWidth:       960,
			MinHeight:      620,
			URL:            targetURL,
			EnableFileDrop: true,
		})
		win.RegisterHook(events.Common.WindowClosing, func(e *application.WindowEvent) {
			if s.quitting == nil || !s.quitting.Load() {
				win.Hide()
				e.Cancel()
			}
		})
		s.settingsWindow = win
		s.settingsMu.Unlock()
		win.Show().Focus()
		return nil
	}
	s.settingsMu.Unlock()

	revealWindow(win)
	if cleanTab != "" {
		encoded, _ := json.Marshal(cleanTab)
		win.ExecJS("window.dispatchEvent(new CustomEvent('astrorder:settings-tab',{detail:{tab:" + string(encoded) + "}}))")
	}
	return nil
}

// revealWindow brings a window to the foreground without un-maximizing an
// already-maximized window (Wails Restore sends SW_RESTORE which strips
// WS_MAXIMIZE when the window is not minimized).
func revealWindow(win *application.WebviewWindow) {
	if win == nil {
		return
	}
	if win.IsMinimised() {
		win.Restore()
	}
	win.Show().Focus()
}

func (s *ShellService) FocusMain(path string) error {
	if s.window == nil {
		return errors.New("main window not available")
	}
	revealWindow(s.window)
	trimmed := strings.TrimSpace(path)
	if trimmed != "" && strings.HasPrefix(trimmed, "/") && !strings.HasPrefix(trimmed, "//") {
		encoded, _ := json.Marshal(trimmed)
		s.window.ExecJS("window.dispatchEvent(new CustomEvent('astrorder:navigate',{detail:{to:" + string(encoded) + "}}))")
	}
	return nil
}
