//go:build windows

package main

import (
	"fmt"
	"math"
	"syscall"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/w32"
)

var (
	statisticsUser32       = syscall.NewLazyDLL("user32.dll")
	statisticsGdi32        = syscall.NewLazyDLL("gdi32.dll")
	statisticsSetParent    = statisticsUser32.NewProc("SetParent")
	statisticsGetParent    = statisticsUser32.NewProc("GetParent")
	statisticsSetWindowRgn = statisticsUser32.NewProc("SetWindowRgn")
	statisticsCreateRect   = statisticsGdi32.NewProc("CreateRectRgn")
	statisticsCreateRound  = statisticsGdi32.NewProc("CreateRoundRectRgn")
	statisticsCombineRgn   = statisticsGdi32.NewProc("CombineRgn")
	statisticsDeleteObject = statisticsGdi32.NewProc("DeleteObject")
)

const win32RgnDiff = 4

func applyStatisticsCutouts(hwnd uintptr, winX, winY, winW, winH int, scale float64, cutouts []StatisticsCutout, hadCutouts bool) bool {
	if winW <= 0 || winH <= 0 || len(cutouts) == 0 {
		if hadCutouts {
			statisticsSetWindowRgn.Call(hwnd, 0, 1)
		}
		return false
	}
	baseRgn, _, _ := statisticsCreateRect.Call(0, 0, uintptr(winW), uintptr(winH))
	if baseRgn == 0 {
		return hadCutouts
	}
	applied := false
	for _, c := range cutouts {
		if c.Width <= 0 || c.Height <= 0 {
			continue
		}
		// Keep unclamped child-local coordinates so overlays straddling the edge of
		// the embedded view keep straight edges at the seam instead of false corners;
		// CombineRgn(..., RGN_DIFF) clips the hole against baseRgn automatically.
		left := int(math.Floor(c.X*scale)) - winX
		top := int(math.Floor(c.Y*scale)) - winY
		right := int(math.Ceil((c.X+c.Width)*scale)) - winX
		bottom := int(math.Ceil((c.Y+c.Height)*scale)) - winY
		if right <= 0 || bottom <= 0 || left >= winW || top >= winH || right <= left || bottom <= top {
			continue
		}
		var hole uintptr
		if c.Radius > 0 {
			rad := int(math.Round(c.Radius * scale))
			maxRad := min((right-left)/2, (bottom-top)/2)
			if rad > maxRad {
				rad = maxRad
			}
			if rad > 0 {
				hole, _, _ = statisticsCreateRound.Call(
					uintptr(left),
					uintptr(top),
					uintptr(right+1),
					uintptr(bottom+1),
					uintptr(rad*2),
					uintptr(rad*2),
				)
			}
		}
		if hole == 0 {
			hole, _, _ = statisticsCreateRect.Call(uintptr(left), uintptr(top), uintptr(right), uintptr(bottom))
		}
		if hole == 0 {
			continue
		}
		statisticsCombineRgn.Call(baseRgn, baseRgn, hole, win32RgnDiff)
		statisticsDeleteObject.Call(hole)
		applied = true
	}
	if !applied {
		statisticsDeleteObject.Call(baseRgn)
		if hadCutouts {
			statisticsSetWindowRgn.Call(hwnd, 0, 1)
		}
		return false
	}
	ok, _, _ := statisticsSetWindowRgn.Call(hwnd, baseRgn, 1)
	if ok == 0 {
		statisticsDeleteObject.Call(baseRgn)
		return hadCutouts
	}
	return true
}

func placeStatisticsWindow(child, parent *application.WebviewWindow, b StatisticsBounds, hadCutouts bool) (bool, error) {
	hwnd, owner := uintptr(child.NativeWindow()), uintptr(parent.NativeWindow())
	if hwnd == 0 || owner == 0 {
		return hadCutouts, fmt.Errorf("嵌入页面视图尚未初始化")
	}
	reparented := false
	current, _, _ := statisticsGetParent.Call(hwnd)
	if current != owner {
		style := uint32(w32.GetWindowLong(hwnd, w32.GWL_STYLE))
		w32.SetWindowLong(hwnd, w32.GWL_STYLE, (style&^(w32.WS_POPUP|w32.WS_CAPTION|w32.WS_THICKFRAME))|w32.WS_CHILD)
		statisticsSetParent.Call(hwnd, owner)
		current, _, _ = statisticsGetParent.Call(hwnd)
		if current != owner {
			return hadCutouts, fmt.Errorf("无法将嵌入页面视图嵌入主窗口")
		}
		reparented = true
	}
	scale := b.Scale
	if scale == 0 {
		scale = float64(w32.GetDpiForWindow(owner)) / 96
	}
	rect := w32.GetClientRect(owner)
	if rect == nil {
		return hadCutouts, fmt.Errorf("无法读取主窗口尺寸")
	}
	x, y := int(math.Round(b.X*scale)), int(math.Round(b.Y*scale))
	width, height := int(math.Round(b.Width*scale)), int(math.Round(b.Height*scale))
	x, y = min(x, int(rect.Right)), min(y, int(rect.Bottom))
	width, height = min(width, int(rect.Right)-x), min(height, int(rect.Bottom)-y)
	flags := uint(w32.SWP_NOACTIVATE)
	if reparented {
		flags |= w32.SWP_FRAMECHANGED
	}
	if !w32.SetWindowPos(hwnd, w32.HWND_TOP, x, y, width, height, flags) {
		return hadCutouts, fmt.Errorf("无法调整嵌入页面视图尺寸")
	}
	hasCutouts := applyStatisticsCutouts(hwnd, x, y, width, height, scale, b.Cutouts, hadCutouts)
	return hasCutouts, nil
}
