//go:build !windows

package lifecycle

func QueryStartupTask() string {
	return "not_supported"
}

func InstallStartupTask(ctlExecutable, stateDir, rootDir string) error {
	return ErrLifecycleUnsupported
}

func UninstallStartupTask() error {
	return ErrLifecycleUnsupported
}
