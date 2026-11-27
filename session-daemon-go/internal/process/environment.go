package process

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// MergePath combines one or more PATH-formatted strings or directory paths
// using the platform path list separator, deduplicating entries while
// preserving first-seen priority order.
func MergePath(segments ...string) string {
	sep := string(os.PathListSeparator)
	seen := make(map[string]bool)
	out := make([]string, 0, 16)
	for _, seg := range segments {
		if seg == "" {
			continue
		}
		for _, raw := range strings.Split(seg, sep) {
			entry := strings.TrimSpace(raw)
			if runtime.GOOS == "windows" {
				entry = strings.Trim(entry, `"`)
				entry = strings.TrimSpace(entry)
			}
			if entry == "" || strings.ContainsRune(entry, 0) {
				continue
			}
			key := filepath.Clean(entry)
			if runtime.GOOS == "windows" {
				key = strings.ToLower(key)
			}
			if seen[key] {
				continue
			}
			seen[key] = true
			out = append(out, entry)
		}
	}
	return strings.Join(out, sep)
}

// RefreshProcessPath merges extraDirs (highest priority), the current process
// PATH, and (on Windows) the latest Machine/User Registry PATH values, updates
// the current process PATH environment variable, and returns the merged value.
func RefreshProcessPath(extraDirs ...string) string {
	segments := make([]string, 0, len(extraDirs)+3)
	segments = append(segments, extraDirs...)
	segments = append(segments, os.Getenv("PATH"))
	if sysPath := systemPath(); sysPath != "" {
		segments = append(segments, sysPath)
	}
	merged := MergePath(segments...)
	if merged != "" {
		_ = os.Setenv("PATH", merged)
	}
	return merged
}

// NativeEnvironment retains current provider settings but removes all App and
// daemon control credentials before launching model-owned child processes.
// PATH entries from explicit overrides, the current process, and (on Windows)
// fresh Machine/User registry keys are merged rather than clobbering one another.
func NativeEnvironment(extra ...[]string) []string {
	sysEnv := systemEnvironment()
	procEnv := os.Environ()

	out := make([]string, 0, len(sysEnv)+len(procEnv)+8)
	indexes := map[string]int{}
	isPathKey := func(name string) bool {
		if runtime.GOOS == "windows" {
			return strings.EqualFold(name, "PATH")
		}
		return name == "PATH"
	}

	var extraPaths []string
	var procPaths []string
	var sysPaths []string
	pathVarName := "PATH"

	addEntries := func(entries []string, bucket *[]string) {
		for _, entry := range entries {
			name, value, valid := strings.Cut(entry, "=")
			if !valid || name == "" {
				continue
			}
			switch strings.ToUpper(name) {
			case "ASTRORDER_SESSION_DAEMON_SECRET", "ASTRORDER_SESSION_DAEMON_CONNECTOR_SECRET", "ASTRORDER_SESSION_DAEMON_GRPC_CREDENTIAL", "ASTRORDER_BROWSER_SECRET", "ASTRORDER_CONNECTOR_SECRET", "ASTRORDER_SECRET":
				continue
			}
			if isPathKey(name) {
				pathVarName = name
				if value != "" {
					*bucket = append(*bucket, value)
				}
				continue
			}
			key := name
			if runtime.GOOS == "windows" {
				key = strings.ToUpper(key)
			}
			if index, exists := indexes[key]; exists {
				out[index] = entry
			} else {
				indexes[key] = len(out)
				out = append(out, entry)
			}
		}
	}

	addEntries(sysEnv, &sysPaths)
	addEntries(procEnv, &procPaths)
	for _, values := range extra {
		addEntries(values, &extraPaths)
	}

	// Build merged PATH with explicit extra overrides first, then current process
	// PATH (which may include managed portable runtimes), then fresh system/user
	// registry PATH entries (user before machine).
	pathSegments := make([]string, 0, len(extraPaths)+len(procPaths)+len(sysPaths))
	for i := len(extraPaths) - 1; i >= 0; i-- {
		pathSegments = append(pathSegments, extraPaths[i])
	}
	pathSegments = append(pathSegments, procPaths...)
	for i := len(sysPaths) - 1; i >= 0; i-- {
		pathSegments = append(pathSegments, sysPaths[i])
	}
	if mergedPath := MergePath(pathSegments...); mergedPath != "" {
		out = append(out, pathVarName+"="+mergedPath)
	}

	expanded := expandEnvironment(out)
	// Re-deduplicate PATH after Windows %VAR% expansion so expanded duplicates collapse.
	for i, entry := range expanded {
		name, value, valid := strings.Cut(entry, "=")
		if valid && isPathKey(name) {
			expanded[i] = name + "=" + MergePath(value)
			break
		}
	}
	return expanded
}
