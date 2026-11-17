package main

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
)

func notificationScheme(stateDir string) string {
	hash := sha256.Sum256([]byte(strings.ToLower(stateDir)))
	return fmt.Sprintf("astrorder-notice-%x", hash[:8])
}

func validNotificationPath(value string) (string, error) {
	u, err := url.Parse(value)
	if err != nil || u.IsAbs() || u.Host != "" || u.Fragment != "" || len(value) > 4096 || strings.ContainsAny(value, "\r\n\x00\\") {
		return "", errors.New("通知跳转地址无效")
	}
	parts := strings.Split(strings.TrimPrefix(u.Path, "/"), "/")
	if len(parts) != 2 || !strings.HasPrefix(u.Path, "/") || parts[1] == "" || parts[1] == "." || parts[1] == ".." {
		return "", errors.New("通知跳转地址无效")
	}
	if parts[0] == "groups" && u.RawQuery == "" {
		return value, nil
	}
	query, err := url.ParseQuery(u.RawQuery)
	if err == nil && parts[0] == "chat" && len(query) == 1 && len(query["agent_id"]) == 1 && query.Get("agent_id") != "" {
		return value, nil
	}
	return "", errors.New("通知跳转地址无效")
}

func notificationPathFromURL(value, stateDir string) (string, error) {
	u, err := url.Parse(value)
	if err != nil || u.Scheme != notificationScheme(stateDir) || u.Host != "open" || u.Path != "" || u.User != nil || u.Fragment != "" {
		return "", errors.New("通知来源无效")
	}
	query, err := url.ParseQuery(u.RawQuery)
	if err != nil || len(query) != 1 || len(query["path"]) != 1 {
		return "", errors.New("通知跳转地址无效")
	}
	return validNotificationPath(query.Get("path"))
}

func (s *ShellService) openNotificationFromArgs(args []string) {
	for index, arg := range args {
		value := ""
		if arg == "--notification" && index+1 < len(args) {
			value = args[index+1]
		} else if strings.HasPrefix(arg, "--notification=") {
			value = strings.TrimPrefix(arg, "--notification=")
		}
		if value == "" {
			continue
		}
		path, err := notificationPathFromURL(value, s.config.StateDir)
		if err != nil || s.window == nil {
			return
		}
		encoded, _ := json.Marshal(path)
		s.window.ExecJS("window.history.pushState({}, '', " + string(encoded) + ");window.dispatchEvent(new PopStateEvent('popstate'));")
		return
	}
}
