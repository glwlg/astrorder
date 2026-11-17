//go:build !windows

package main

import "errors"

func (s *ShellService) notificationLaunchURL(string) (string, error) {
	return "", errors.New("当前系统不支持桌面通知")
}
