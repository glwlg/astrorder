//go:build !windows

package audio

import (
	"os"
	"os/exec"
	"strings"
)

func configurePlatformProcess(cmd *exec.Cmd) {
	cmd.Env = sanitizedEnv()
}

func sanitizedEnv() []string {
	var env []string
	for _, entry := range os.Environ() {
		upper := strings.ToUpper(entry)
		key, _, _ := strings.Cut(upper, "=")
		if strings.Contains(key, "SECRET") ||
			strings.Contains(key, "CREDENTIAL") ||
			strings.Contains(key, "TOKEN") ||
			strings.Contains(key, "PASSWORD") ||
			strings.Contains(key, "AUTH") ||
			strings.HasPrefix(key, "ASTRORDER_") {
			continue
		}
		env = append(env, entry)
	}
	return env
}
