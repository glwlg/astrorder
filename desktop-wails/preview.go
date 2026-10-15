package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"image"
	_ "image/gif"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
)

type PreviewPayload struct {
	Images []string `json:"images"`
	Index  int      `json:"index"`
}
type PreviewService struct {
	shell   *ShellService
	mu      sync.Mutex
	window  *application.WebviewWindow
	payload PreviewPayload
}

func decodePreview(data string) (image.Image, error) {
	if len(data) > 32<<20 {
		return nil, errors.New("image is too large")
	}
	header, body, ok := strings.Cut(data, ",")
	if !ok || !strings.HasPrefix(header, "data:image/") || !strings.HasSuffix(header, ";base64") {
		return nil, errors.New("invalid image data")
	}
	raw, err := base64.StdEncoding.DecodeString(body)
	if err != nil {
		return nil, err
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	if cfg.Width <= 0 || cfg.Height <= 0 || int64(cfg.Width)*int64(cfg.Height) > 40_000_000 {
		return nil, errors.New("image dimensions are too large")
	}
	img, _, err := image.Decode(bytes.NewReader(raw))
	return img, err
}
func (p *PreviewService) open(payload PreviewPayload) error {
	if len(payload.Images) == 0 || len(payload.Images) > 32 {
		return errors.New("invalid image count")
	}
	total := 0
	for _, data := range payload.Images {
		total += len(data)
		if total > 64<<20 {
			return errors.New("preview is too large")
		}
		if _, err := decodePreview(data); err != nil {
			return err
		}
	}
	if payload.Index < 0 || payload.Index >= len(payload.Images) {
		payload.Index = 0
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.payload = payload
	if p.window == nil {
		p.window = p.shell.app.Window.NewWithOptions(application.WebviewWindowOptions{Name: "preview", Title: "图片预览", Width: 1100, Height: 760, URL: "/image-preview.html", Hidden: true, Frameless: true, DisableResize: true, BackgroundColour: application.NewRGB(12, 13, 16), Windows: application.WindowsWindow{HiddenOnTaskbar: true}})
		p.window.RegisterHook(events.Common.WindowClosing, func(e *application.WindowEvent) { e.Cancel(); go p.close() })
	}
	p.window.SetBounds(p.shell.window.Bounds())
	p.window.Show().Focus()
	p.window.ExecJS("window.dispatchEvent(new Event('preview-update'))")
	return nil
}
func (p *PreviewService) close() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.window != nil {
		p.window.Hide()
	}
	p.payload = PreviewPayload{}
	if p.shell.window != nil && p.shell.window.IsVisible() {
		p.shell.window.Focus()
	}
}
func (p *PreviewService) authorize(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return requireWindow(ctx, p.window)
}
func (p *PreviewService) Data(ctx context.Context) (PreviewPayload, error) {
	if err := p.authorize(ctx); err != nil {
		return PreviewPayload{}, err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.payload, nil
}
func (p *PreviewService) Close(ctx context.Context) error {
	if err := p.authorize(ctx); err != nil {
		return err
	}
	p.close()
	return nil
}
func (p *PreviewService) current(index int) (image.Image, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if index < 0 || index >= len(p.payload.Images) {
		return nil, errors.New("invalid image index")
	}
	return decodePreview(p.payload.Images[index])
}
func (p *PreviewService) Copy(ctx context.Context, index int) (bool, error) {
	if err := p.authorize(ctx); err != nil {
		return false, err
	}
	img, err := p.current(index)
	if err != nil {
		return false, err
	}
	return copyImageWindows(img, uintptr(p.window.NativeWindow()))
}
func (p *PreviewService) Save(ctx context.Context, index int) (bool, error) {
	if err := p.authorize(ctx); err != nil {
		return false, err
	}
	img, err := p.current(index)
	if err != nil {
		return false, err
	}
	name, err := p.shell.app.Dialog.SaveFile().AttachToWindow(p.window).SetMessage("保存图片").SetFilename(fmt.Sprintf("astrorder-image-%d.png", time.Now().UnixMilli())).AddFilter("PNG 图像", "*.png").AddFilter("JPEG 图像", "*.jpg;*.jpeg").PromptForSingleSelection()
	if err != nil || name == "" {
		return false, err
	}
	ext := strings.ToLower(filepath.Ext(name))
	if ext != ".png" && ext != ".jpg" && ext != ".jpeg" {
		return false, errors.New("请选择 PNG 或 JPEG 格式")
	}
	f, err := os.Create(name)
	if err != nil {
		return false, err
	}
	if ext == ".png" {
		err = png.Encode(f, img)
	} else {
		err = jpeg.Encode(f, img, &jpeg.Options{Quality: 90})
	}
	closeErr := f.Close()
	if err != nil {
		return false, err
	}
	return closeErr == nil, closeErr
}
