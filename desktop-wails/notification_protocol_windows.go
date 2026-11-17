package main

import (
	"fmt"
	"net/url"
	"os"
	"strings"
	"syscall"

	"golang.org/x/sys/windows/registry"
)

// Register a protocol for this installation's state directory. Windows starts
// a short-lived second instance; Wails forwards its route to the existing window.
func (s *ShellService) notificationLaunchURL(path string) (string, error) {
	path, err := validNotificationPath(path)
	if err != nil {
		return "", err
	}
	executable, err := os.Executable()
	if err != nil {
		return "", err
	}
	args := []string{executable, "--state-dir", s.config.StateDir, fmt.Sprintf("--packaged=%t", s.isPackaged)}
	if s.config.Backend != nil {
		args = append(args, "--backend", s.config.Backend.String())
	}
	command := make([]string, 0, len(args)+2)
	for _, arg := range args {
		command = append(command, syscall.EscapeArg(arg))
	}
	command = append(command, "--notification", `"%1"`)
	scheme := notificationScheme(s.config.StateDir)
	key, _, err := registry.CreateKey(registry.CURRENT_USER, `Software\Classes\`+scheme, registry.SET_VALUE)
	if err != nil {
		return "", fmt.Errorf("注册通知跳转失败: %w", err)
	}
	defer key.Close()
	if err = key.SetStringValue("", "URL:星序通知"); err != nil {
		return "", err
	}
	if err = key.SetStringValue("URL Protocol", ""); err != nil {
		return "", err
	}
	handler, _, err := registry.CreateKey(registry.CURRENT_USER, `Software\Classes\`+scheme+`\shell\open\command`, registry.SET_VALUE)
	if err != nil {
		return "", err
	}
	defer handler.Close()
	if err = handler.SetStringValue("", strings.Join(command, " ")); err != nil {
		return "", err
	}
	return scheme + "://open?path=" + url.QueryEscape(path), nil
}
