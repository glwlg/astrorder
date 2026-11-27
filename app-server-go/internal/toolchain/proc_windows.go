//go:build windows

package toolchain

import (
	"os/exec"
	"syscall"
)

const createNoWindow = 0x08000000

func configureHiddenCmd(cmd *exec.Cmd) {
	if cmd == nil {
		return
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: createNoWindow,
	}
}

func defaultOpenSystemBrowser(rawURL string) error {
	cleanURL, err := validateBrowserURL(rawURL)
	if err != nil {
		return err
	}
	cmd := exec.Command("rundll32.exe", "url.dll,FileProtocolHandler", cleanURL)
	configureHiddenCmd(cmd)
	return cmd.Start()
}
