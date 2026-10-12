package connections

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"
)

var (
	sshHostPattern         = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9.-]{0,251}[A-Za-z0-9])?$`)
	sshNamePattern         = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_.-]{0,63}$`)
	sshAliasPattern        = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$`)
	windowsAbsolutePattern = regexp.MustCompile(`^[A-Za-z]:[/\\]`)
)

func sshText(raw map[string]any, key string, maximum int) (string, error) {
	value := raw[key]
	if value == nil {
		return "", nil
	}
	text, ok := value.(string)
	if !ok {
		return "", fmt.Errorf("%s must be text", key)
	}
	for _, r := range text {
		if r < 32 || r == 127 {
			return "", fmt.Errorf("%s contains a control character", key)
		}
	}
	text = strings.TrimSpace(text)
	if utf8.RuneCountInString(text) > maximum {
		return "", fmt.Errorf("%s exceeds its length limit", key)
	}
	return text, nil
}

func sshPort(value any) (uint32, error) {
	if value == nil {
		return 22, nil
	}
	var number int64
	var err error
	switch port := value.(type) {
	case int:
		number = int64(port)
	case int64:
		number = port
	case float64:
		if port < 1 || port > 65535 || port != float64(int64(port)) {
			return 0, fmt.Errorf("SSH port must be an integer between 1 and 65535")
		}
		number = int64(port)
	case json.Number:
		number, err = port.Int64()
	case string:
		number, err = strconv.ParseInt(port, 10, 64)
	default:
		err = fmt.Errorf("invalid port type")
	}
	if err != nil || number < 1 || number > 65535 {
		return 0, fmt.Errorf("SSH port must be an integer between 1 and 65535")
	}
	return uint32(number), nil
}

func validateSSHSettings(raw map[string]any) (map[string]any, error) {
	settings := make(map[string]any)
	for key, limit := range map[string]int{"display_name": 256, "profile_name": 128, "host": 253, "ssh_config_alias": 128, "user": 64, "identity_file": 1024, "hermes_path": 1024, "workspace": 1024} {
		text, err := sshText(raw, key, limit)
		if err != nil {
			return nil, err
		}
		settings[key] = text
	}
	host := settings["host"].(string)
	alias := settings["ssh_config_alias"].(string)
	if host == "" && alias == "" {
		return nil, fmt.Errorf("SSH host or config alias is required")
	}
	if host != "" && (!sshHostPattern.MatchString(host) || strings.Contains(host, "..")) {
		return nil, fmt.Errorf("SSH host must be a hostname or IPv4 address")
	}
	if alias != "" && !sshAliasPattern.MatchString(alias) {
		return nil, fmt.Errorf("invalid SSH config alias")
	}
	if settings["profile_name"] == "" {
		settings["profile_name"] = "default"
	}
	if !sshAliasPattern.MatchString(settings["profile_name"].(string)) {
		return nil, fmt.Errorf("invalid Hermes profile name")
	}
	if user := settings["user"].(string); user != "" && !sshNamePattern.MatchString(user) {
		return nil, fmt.Errorf("invalid SSH username")
	}
	for _, key := range []string{"identity_file", "hermes_path", "workspace"} {
		text := settings[key].(string)
		if text != "" && !strings.HasPrefix(text, "/") && !strings.HasPrefix(text, `\\`) && !windowsAbsolutePattern.MatchString(text) {
			return nil, fmt.Errorf("%s must be an absolute path", key)
		}
	}
	port, err := sshPort(raw["port"])
	if err != nil {
		return nil, err
	}
	settings["port"] = int(port)
	for key, value := range settings {
		if value == "" {
			settings[key] = nil
		}
	}
	return settings, nil
}
