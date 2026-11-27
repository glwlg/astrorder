//go:build !windows

package desktopcontrol

import (
	"context"
	"fmt"
	"runtime"
)

type unsupportedDriver struct {
	platform string
}

func newPlatformDriver(_ func(context.Context) Config) Driver {
	return &unsupportedDriver{platform: runtime.GOOS}
}

func (d *unsupportedDriver) Platform() string    { return d.platform }
func (d *unsupportedDriver) BackendName() string { return "unimplemented" }

func (d *unsupportedDriver) Info(_ context.Context) DriverInfo {
	return DriverInfo{
		Platform:  d.platform,
		Backend:   d.BackendName(),
		Supported: false,
		Running:   false,
		Detail:    fmt.Sprintf("当前宿主机系统（%s）暂未实现桌面控制驱动；Windows 宿主机内置支持 Windows-MCP，macOS 驱动接口已预留", d.platform),
	}
}

func (d *unsupportedDriver) Invoke(_ context.Context, _ string, _ map[string]any) (*CallResult, error) {
	return nil, fmt.Errorf("%w: host OS %q has no desktop control driver yet (Windows uses windows-mcp; macOS slot reserved)", ErrUnsupportedPlatform, d.platform)
}

func (d *unsupportedDriver) TestConnection(ctx context.Context, _ *Config) (DriverInfo, string, error) {
	info := d.Info(ctx)
	return info, info.Detail, fmt.Errorf("%w: %s", ErrUnsupportedPlatform, d.platform)
}

func (d *unsupportedDriver) Close(_ context.Context) error { return nil }
