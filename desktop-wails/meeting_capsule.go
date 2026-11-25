package main

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
)

const (
	meetingCapsuleWidth  = 420
	meetingCapsuleHeight = 82
)

type MeetingCapsuleState struct {
	Active   bool      `json:"active"`
	Paused   bool      `json:"paused"`
	Stopping bool      `json:"stopping"`
	Elapsed  string    `json:"elapsed"`
	Text     string    `json:"text"`
	Bars     []float64 `json:"bars,omitempty"`
}

type MeetingCapsuleService struct {
	shell      *ShellService
	mu         sync.Mutex
	window     *application.WebviewWindow
	state      MeetingCapsuleState
	positioned bool
}

func truncateRunes(s string, max int) string {
	trimmed := strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(s, "\r", " "), "\n", " "))
	if max <= 0 || utf8.RuneCountInString(trimmed) <= max {
		return trimmed
	}
	runes := []rune(trimmed)
	return string(runes[:max])
}

func sanitizeCapsuleState(in MeetingCapsuleState) MeetingCapsuleState {
	out := MeetingCapsuleState{
		Active:   in.Active,
		Paused:   in.Paused,
		Stopping: in.Stopping,
		Elapsed:  truncateRunes(in.Elapsed, 16),
		Text:     truncateRunes(in.Text, 180),
	}
	if len(in.Bars) > 0 {
		limit := len(in.Bars)
		if limit > 8 {
			limit = 8
		}
		bars := make([]float64, limit)
		for i := 0; i < limit; i++ {
			v := in.Bars[i]
			if v < 0.1 {
				v = 0.1
			} else if v > 1 {
				v = 1
			}
			bars[i] = v
		}
		out.Bars = bars
	}
	return out
}

func (c *MeetingCapsuleService) authorize(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return requireWindow(ctx, c.window)
}

func (c *MeetingCapsuleService) ensureWindowLocked() (*application.WebviewWindow, error) {
	if c.window != nil {
		return c.window, nil
	}
	if c.shell == nil || c.shell.app == nil {
		return nil, errors.New("desktop application not available")
	}
	win := c.shell.app.Window.NewWithOptions(application.WebviewWindowOptions{
		Name:             "meeting-capsule",
		Title:            "实时转写",
		Width:            meetingCapsuleWidth,
		Height:           meetingCapsuleHeight,
		URL:              "/meeting-capsule.html",
		Hidden:           true,
		Frameless:        true,
		AlwaysOnTop:      true,
		DisableResize:    true,
		BackgroundType:   application.BackgroundTypeTransparent,
		BackgroundColour: application.NewRGBA(0, 0, 0, 0),
		Windows: application.WindowsWindow{
			HiddenOnTaskbar:                   true,
			DisableFramelessWindowDecorations: true,
		},
	})
	win.RegisterHook(events.Common.WindowClosing, func(e *application.WindowEvent) {
		if c.shell.quitting == nil || !c.shell.quitting.Load() {
			e.Cancel()
			win.Hide()
		}
	})
	c.window = win
	return win, nil
}

func (c *MeetingCapsuleService) positionWindowLocked(win *application.WebviewWindow) {
	if c.positioned || win == nil {
		return
	}
	c.positioned = true
	var screen *application.Screen
	if c.shell != nil && c.shell.window != nil {
		if s, err := c.shell.window.GetScreen(); err == nil && s != nil {
			screen = s
		}
	}
	if screen == nil && c.shell != nil && c.shell.app != nil && c.shell.app.Screen != nil {
		screen = c.shell.app.Screen.GetPrimary()
	}
	if screen != nil && screen.WorkArea.Width > meetingCapsuleWidth+24 {
		x := screen.WorkArea.X + screen.WorkArea.Width - meetingCapsuleWidth - 28
		y := screen.WorkArea.Y + 24
		win.SetPosition(x, y)
		return
	}
	win.SetPosition(80, 32)
}

func (c *MeetingCapsuleService) pushStateLocked() {
	if c.window == nil {
		return
	}
	encoded, err := json.Marshal(c.state)
	if err != nil {
		return
	}
	c.window.ExecJS("window.__astrorderUpdateCapsule&&window.__astrorderUpdateCapsule(" + string(encoded) + ")")
}

func (c *MeetingCapsuleService) show(state MeetingCapsuleState) error {
	clean := sanitizeCapsuleState(state)
	clean.Active = true
	c.mu.Lock()
	defer c.mu.Unlock()
	c.state = clean
	win, err := c.ensureWindowLocked()
	if err != nil {
		return err
	}
	c.positionWindowLocked(win)
	win.SetAlwaysOnTop(true)
	win.Show()
	c.pushStateLocked()
	return nil
}

func (c *MeetingCapsuleService) update(state MeetingCapsuleState) error {
	clean := sanitizeCapsuleState(state)
	c.mu.Lock()
	defer c.mu.Unlock()
	c.state = clean
	if c.window != nil && c.window.IsVisible() {
		c.pushStateLocked()
	}
	return nil
}

func (c *MeetingCapsuleService) hide() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.state.Active = false
	if c.window != nil {
		c.window.Hide()
	}
}

func (c *MeetingCapsuleService) dispatchMainAction(action string) error {
	if c.shell == nil || c.shell.window == nil {
		return errors.New("main window not available")
	}
	encoded, err := json.Marshal(action)
	if err != nil {
		return err
	}
	c.shell.window.ExecJS("window.dispatchEvent(new CustomEvent('astrorder:meeting-capsule-action',{detail:{action:" + string(encoded) + "}}))")
	return nil
}

func (c *MeetingCapsuleService) State(ctx context.Context) (MeetingCapsuleState, error) {
	if err := c.authorize(ctx); err != nil {
		return MeetingCapsuleState{}, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.state, nil
}

func (c *MeetingCapsuleService) Stop(ctx context.Context) error {
	if err := c.authorize(ctx); err != nil {
		return err
	}
	return c.dispatchMainAction("stop")
}

func (c *MeetingCapsuleService) TogglePause(ctx context.Context) error {
	if err := c.authorize(ctx); err != nil {
		return err
	}
	return c.dispatchMainAction("toggle_pause")
}

func (c *MeetingCapsuleService) Expand(ctx context.Context) error {
	if err := c.authorize(ctx); err != nil {
		return err
	}
	if c.shell == nil {
		return errors.New("desktop shell not available")
	}
	return c.shell.FocusMain("/meetings")
}
