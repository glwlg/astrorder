package process

import (
	"os"
	"runtime"
	"strings"
)

// NativeEnvironment retains current provider settings but removes all App and
// daemon control credentials before launching model-owned child processes.
func NativeEnvironment(extra ...[]string) []string {
	env := append(systemEnvironment(), os.Environ()...)
	for _, values := range extra {
		env = append(env, values...)
	}
	out := make([]string, 0, len(env))
	indexes := map[string]int{}
	for _, entry := range env {
		name, _, valid := strings.Cut(entry, "=")
		if !valid || name == "" {
			continue
		}
		switch strings.ToUpper(name) {
		case "ASTRORDER_SESSION_DAEMON_SECRET", "ASTRORDER_SESSION_DAEMON_CONNECTOR_SECRET", "ASTRORDER_BROWSER_SECRET", "ASTRORDER_CONNECTOR_SECRET", "ASTRORDER_SECRET":
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
	return expandEnvironment(out)
}
