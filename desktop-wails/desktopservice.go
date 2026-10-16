package main

import (
	"context"
	"errors"
	"github.com/wailsapp/wails/v3/pkg/application"
)

// DesktopService exposes capabilities only to the main window. The internal
// ShellService remains usable by native tray callbacks without page authority.
type DesktopService struct{ shell *ShellService }

func requireWindow(ctx context.Context, expected *application.WebviewWindow) error {
	caller, ok := ctx.Value(application.WindowKey).(application.Window)
	if !ok || expected == nil || caller.ID() != expected.ID() {
		return errors.New("desktop capability is not available to this window")
	}
	return nil
}
func (d *DesktopService) Status(ctx context.Context) (map[string]any, error) {
	if err := requireWindow(ctx, d.shell.window); err != nil {
		return nil, err
	}
	return d.shell.Status()
}
func (d *DesktopService) Run(ctx context.Context, target, action string) (map[string]any, error) {
	if err := requireWindow(ctx, d.shell.window); err != nil {
		return nil, err
	}
	return d.shell.Run(target, action)
}
func (d *DesktopService) GetStartupSettings(ctx context.Context) (StartupSettings, error) {
	if err := requireWindow(ctx, d.shell.window); err != nil {
		return StartupSettings{}, err
	}
	return d.shell.GetStartupSettings()
}
func (d *DesktopService) SetStartupEnabled(ctx context.Context, enabled bool) (StartupSettings, error) {
	if err := requireWindow(ctx, d.shell.window); err != nil {
		return StartupSettings{}, err
	}
	return d.shell.SetStartupEnabled(enabled)
}
func (d *DesktopService) SetClipboardFiles(ctx context.Context, paths []string) (bool, error) {
	if err := requireWindow(ctx, d.shell.window); err != nil {
		return false, err
	}
	return d.shell.SetClipboardFiles(paths)
}
func (d *DesktopService) Notify(ctx context.Context, title, body string) (bool, error) {
	if err := requireWindow(ctx, d.shell.window); err != nil {
		return false, err
	}
	return d.shell.Notify(title, body), nil
}
func (d *DesktopService) Minimize(ctx context.Context) error {
	if err := requireWindow(ctx, d.shell.window); err != nil {
		return err
	}
	d.shell.Minimize()
	return nil
}
func (d *DesktopService) Maximize(ctx context.Context) (bool, error) {
	if err := requireWindow(ctx, d.shell.window); err != nil {
		return false, err
	}
	return d.shell.Maximize(), nil
}
func (d *DesktopService) Close(ctx context.Context) error {
	if err := requireWindow(ctx, d.shell.window); err != nil {
		return err
	}
	d.shell.Close()
	return nil
}
func (d *DesktopService) IsMaximized(ctx context.Context) (bool, error) {
	if err := requireWindow(ctx, d.shell.window); err != nil {
		return false, err
	}
	return d.shell.IsMaximized(), nil
}
func (d *DesktopService) OpenDirectoryDialog(ctx context.Context, path string) (string, error) {
	if err := requireWindow(ctx, d.shell.window); err != nil {
		return "", err
	}
	return d.shell.OpenDirectoryDialog(path)
}
func (d *DesktopService) OpenFileDialog(ctx context.Context, path string) (string, error) {
	if err := requireWindow(ctx, d.shell.window); err != nil {
		return "", err
	}
	return d.shell.OpenFileDialog(path)
}
func (d *DesktopService) OpenExternal(ctx context.Context, url string) error {
	if err := requireWindow(ctx, d.shell.window); err != nil {
		return err
	}
	return d.shell.OpenExternal(url)
}
func (d *DesktopService) ShowStatistics(ctx context.Context, address string, bounds StatisticsBounds, refresh bool) error {
	if err := requireWindow(ctx, d.shell.window); err != nil {
		return err
	}
	return d.shell.statistics.show(address, bounds, refresh)
}
func (d *DesktopService) HideStatistics(ctx context.Context) error {
	if err := requireWindow(ctx, d.shell.window); err != nil {
		return err
	}
	d.shell.statistics.hide()
	return nil
}
func (d *DesktopService) OpenPreview(ctx context.Context, payload PreviewPayload) error {
	if err := requireWindow(ctx, d.shell.window); err != nil {
		return err
	}
	return d.shell.preview.open(payload)
}
func (d *DesktopService) ReportReady(ctx context.Context) error {
	if err := requireWindow(ctx, d.shell.window); err != nil {
		return err
	}
	return d.shell.ReportReady()
}
