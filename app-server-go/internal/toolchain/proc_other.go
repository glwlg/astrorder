//go:build !windows

package toolchain

import (
	"errors"
	"os/exec"
	"runtime"
)

func configureHiddenCmd(*exec.Cmd) {}

func defaultOpenSystemBrowser(rawURL string) error {
	cleanURL, err := validateBrowserURL(rawURL)
	if err != nil {
		return err
	}
	opener := "xdg-open"
	if runtime.GOOS == "darwin" {
		opener = "open"
	}
	bin, err := exec.LookPath(opener)
	if err != nil {
		return errors.New("未找到系统浏览器启动器：" + opener)
	}
	cmd := exec.Command(bin, cleanURL)
	return cmd.Start()
}
