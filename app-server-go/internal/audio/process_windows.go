//go:build windows

package audio

import (
	"os"
	"os/exec"
	"strings"
	"syscall"
)

const (
	createNoWindow = 0x08000000
)

func configurePlatformProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: createNoWindow,
	}
	cmd.Env = sanitizedEnv()
}

func sanitizedEnv() []string {
	var env []string
	for _, entry := range os.Environ() {
		// Strip sensitive credentials, tokens, secrets, and auth headers
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
