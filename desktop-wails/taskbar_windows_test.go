//go:build windows

package main

import (
	"runtime"
	"syscall"
	"testing"
	"unsafe"

	"github.com/wailsapp/wails/v3/pkg/w32"
)

func TestTaskbarReceivesSmallIconWhenFrameworkSetsLargeIcon(t *testing.T) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	options, err := desktopWindowsOptions(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if options.WndProcInterceptor == nil {
		t.Fatal("taskbar small icon binding is missing")
	}
	instance := w32.GetModuleHandle("")
	class := w32.MustStringToUTF16Ptr("AstrorderTaskbarIconRegression")
	wc := w32.WNDCLASSEX{}
	wc.Size = uint32(unsafe.Sizeof(wc))
	wc.Instance = instance
	wc.ClassName = class
	wc.WndProc = syscall.NewCallback(func(hwnd w32.HWND, msg uint32, wp, lp uintptr) uintptr {
		if result, handled := options.WndProcInterceptor(hwnd, msg, wp, lp); handled {
			return result
		}
		return w32.DefWindowProc(hwnd, msg, wp, lp)
	})
	if w32.RegisterClassEx(&wc) == 0 {
		t.Fatal("register test window class")
	}
	hwnd := w32.CreateWindowEx(0, class, w32.MustStringToUTF16Ptr("Icon regression"), 0, 0, 0, 64, 64, 0, 0, instance, nil)
	if hwnd == 0 {
		t.Fatal("create test window")
	}
	defer w32.DestroyWindow(hwnd)
	large, err := w32.CreateLargeHIconFromImage(appIcon)
	if err != nil {
		t.Fatal(err)
	}
	defer w32.DestroyIcon(large)
	w32.SendMessage(hwnd, w32.WM_SETICON, w32.ICON_BIG, large)
	if got := w32.SendMessage(hwnd, w32.WM_GETICON, w32.ICON_BIG, 0); got != large {
		t.Fatalf("large icon: got %v want %v", got, large)
	}
	small := w32.SendMessage(hwnd, w32.WM_GETICON, w32.ICON_SMALL, 0)
	if small == 0 {
		t.Fatal("taskbar small icon is absent")
	}
	if got := w32.SendMessage(hwnd, w32.WM_GETICON, w32.ICON_SMALL2, 0); got != small {
		t.Fatalf("small fallback: got %v want %v", got, small)
	}
}
