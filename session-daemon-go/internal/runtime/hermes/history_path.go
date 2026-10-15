package hermes

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

func (a *Adapter) historyDBPath() string {
	// Remote native ids must never be looked up in this machine's profile.
	if a.sshStarter != nil {
		return ""
	}
	if a.config.StateDBPath != "" {
		return a.config.StateDBPath
	}
	env := func(key string) string {
		value := os.Getenv(key)
		for _, entry := range a.config.Environment {
			name, v, ok := strings.Cut(entry, "=")
			if ok && strings.EqualFold(name, key) {
				value = v
			}
		}
		return strings.TrimSpace(value)
	}
	home := env("HERMES_HOME")
	if home == "" {
		userHome, _ := os.UserHomeDir()
		suffix := env("HERMES_DATA_DIR_SUFFIX")
		if runtime.GOOS == "windows" {
			base := env("LOCALAPPDATA")
			if base == "" {
				base = filepath.Join(userHome, "AppData", "Local")
			}
			home = filepath.Join(base, "hermes"+suffix)
		} else {
			home = filepath.Join(userHome, ".hermes"+suffix)
		}
	}
	profile := a.config.ProfileName
	if profile != "" && profile != "default" {
		if strings.ContainsAny(profile, "/\\") || profile == ".." || filepath.IsAbs(profile) {
			return ""
		}
		home = filepath.Join(home, "profiles", profile)
	}
	path := filepath.Join(home, "state.db")
	if info, err := os.Stat(path); err == nil && !info.IsDir() {
		return path
	}
	return ""
}
