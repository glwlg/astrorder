package main

import (
	"errors"
	"math"
	"net/url"
	"strings"
	"sync"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
)

// StatisticsBounds uses CSS pixels within the main window's client area.
type StatisticsBounds struct {
	X      float64 `json:"x"`
	Y      float64 `json:"y"`
	Width  float64 `json:"width"`
	Height float64 `json:"height"`
	Scale  float64 `json:"scale"`
}

func (b StatisticsBounds) validate() error {
	for _, v := range []float64{b.X, b.Y, b.Width, b.Height, b.Scale} {
		if math.IsNaN(v) || math.IsInf(v, 0) || v < 0 || v > 100000 {
			return errors.New("invalid statistics view bounds")
		}
	}
	return nil
}
func validateStatisticsAddress(address string) error {
	u, err := url.Parse(address)
	if err != nil || strings.ContainsAny(address, "\r\n\x00") || u == nil || u.Hostname() == "" || u.User != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return errors.New("统计页地址必须是完整的 http 或 https 地址，且不能包含用户名或密码")
	}
	return nil
}

type StatisticsView struct {
	shell   *ShellService
	mu      sync.Mutex
	window  *application.WebviewWindow
	address string
}

func (s *StatisticsView) show(address string, bounds StatisticsBounds, refresh bool) error {
	if err := validateStatisticsAddress(address); err != nil {
		return err
	}
	if err := bounds.validate(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.window == nil {
		// This is a top-level web document, not an HTML iframe. The native child
		// HWND provides the visual embedding without changing SameSite semantics.
		s.window = s.shell.app.Window.NewWithOptions(application.WebviewWindowOptions{
			Name: "gateway-statistics", Title: "网关统计", URL: address,
			Width: 800, Height: 600, Hidden: true, Frameless: true, DisableResize: true,
			Windows: application.WindowsWindow{HiddenOnTaskbar: true, DisableFramelessWindowDecorations: true},
		})
		s.window.RegisterHook(events.Common.WindowClosing, func(e *application.WindowEvent) { e.Cancel(); go s.hide() })
	} else if refresh || address != s.address {
		s.window.SetURL(address)
	}
	s.address = address
	if bounds.Width < 1 || bounds.Height < 1 {
		s.window.Hide()
		return nil
	}
	var err error
	application.InvokeSync(func() { err = placeStatisticsWindow(s.window, s.shell.window, bounds) })
	if err != nil {
		s.window.Hide()
		return err
	}
	s.window.Show()
	return nil
}
func (s *StatisticsView) hide() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.window != nil {
		s.window.Hide()
	}
}
