//go:build windows

package main

import (
	"fmt"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/w32"
)

func desktopWindowsOptions(userDataPath string) (application.WindowsOptions, error) {

	// Wails beta.28 sets only ICON_BIG. With no class icon, Explorer can
	// display the default document icon. Keep a shared small icon alive for
	// the process and bind it before forwarding each large-icon assignment.
	small, err := w32.CreateSmallHIconFromImage(trayIcon)
	if err != nil {
		return application.WindowsOptions{}, fmt.Errorf("create taskbar icon: %w", err)
	}
	return application.WindowsOptions{
		WebviewUserDataPath: userDataPath,
		WndProcInterceptor: func(hwnd uintptr, msg uint32, wParam, lParam uintptr) (uintptr, bool) {
			if msg == w32.WM_SETICON && wParam == w32.ICON_BIG && lParam != 0 {
				w32.SendMessage(hwnd, w32.WM_SETICON, w32.ICON_SMALL, small)
			}
			return 0, false
		},
	}, nil
}
