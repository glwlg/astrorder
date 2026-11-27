//go:build windows

package toolchain

import (
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows/registry"
)

func readSystemRegistryPaths() []string {
	var out []string
	// User PATH takes precedence over Machine PATH when both are present, matching
	// Windows shell behavior for user-installed CLI tools while retaining system entries.
	if userPath := readRegistryPathValue(registry.CURRENT_USER, `Environment`, "Path"); userPath != "" {
		out = append(out, filepath.SplitList(userPath)...)
	}
	if machinePath := readRegistryPathValue(registry.LOCAL_MACHINE, `SYSTEM\CurrentControlSet\Control\Session Manager\Environment`, "Path"); machinePath != "" {
		out = append(out, filepath.SplitList(machinePath)...)
	}
	return out
}

func readRegistryPathValue(root registry.Key, subkey, name string) string {
	key, err := registry.OpenKey(root, subkey, registry.QUERY_VALUE)
	if err != nil {
		return ""
	}
	defer key.Close()
	val, _, err := key.GetStringValue(name)
	if err != nil || strings.TrimSpace(val) == "" {
		return ""
	}
	if expanded, expErr := registry.ExpandString(val); expErr == nil && strings.TrimSpace(expanded) != "" {
		return expanded
	}
	return val
}
