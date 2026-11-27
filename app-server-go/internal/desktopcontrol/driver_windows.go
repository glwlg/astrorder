//go:build windows

package desktopcontrol

import "context"

func newPlatformDriver(readConfig func(context.Context) Config) Driver {
	return NewWindowsMCPDriver(readConfig)
}
