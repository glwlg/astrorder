package lifecycle

import "os/exec"

// ConfigureIndependentProcess preserves the same job breakaway contract used by kernels.
func ConfigureIndependentProcess(command *exec.Cmd) error {
	return configureIndependentProcess(command)
}
