//go:build windows

package main

import (
	"fmt"
	"math"
	"syscall"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/w32"
)

var statisticsSetParent = syscall.NewLazyDLL("user32.dll").NewProc("SetParent")
var statisticsGetParent = syscall.NewLazyDLL("user32.dll").NewProc("GetParent")

func placeStatisticsWindow(child, parent *application.WebviewWindow, b StatisticsBounds) error {
	hwnd, owner := uintptr(child.NativeWindow()), uintptr(parent.NativeWindow())
	if hwnd == 0 || owner == 0 {
		return fmt.Errorf("网关统计视图尚未初始化")
	}
	current, _, _ := statisticsGetParent.Call(hwnd)
	if current != owner {
		style := uint32(w32.GetWindowLong(hwnd, w32.GWL_STYLE))
		w32.SetWindowLong(hwnd, w32.GWL_STYLE, (style&^(w32.WS_POPUP|w32.WS_CAPTION|w32.WS_THICKFRAME))|w32.WS_CHILD)
		statisticsSetParent.Call(hwnd, owner)
		current, _, _ = statisticsGetParent.Call(hwnd)
		if current != owner {
			return fmt.Errorf("无法将网关统计视图嵌入主窗口")
		}
	}
	scale := b.Scale
	if scale == 0 {
		scale = float64(w32.GetDpiForWindow(owner)) / 96
	}
	rect := w32.GetClientRect(owner)
	if rect == nil {
		return fmt.Errorf("无法读取主窗口尺寸")
	}
	x, y := int(math.Round(b.X*scale)), int(math.Round(b.Y*scale))
	width, height := int(math.Round(b.Width*scale)), int(math.Round(b.Height*scale))
	x, y = min(x, int(rect.Right)), min(y, int(rect.Bottom))
	width, height = min(width, int(rect.Right)-x), min(height, int(rect.Bottom)-y)
	if !w32.SetWindowPos(hwnd, w32.HWND_TOP, x, y, width, height, w32.SWP_NOACTIVATE|w32.SWP_FRAMECHANGED) {
		return fmt.Errorf("无法调整网关统计视图尺寸")
	}
	return nil
}
