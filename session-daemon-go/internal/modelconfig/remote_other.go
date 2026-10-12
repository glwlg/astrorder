//go:build !windows

package modelconfig

import (
	"os/exec"
)

func prepareCmdSysProcAttr(cmd *exec.Cmd) {
}
