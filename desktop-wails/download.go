package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"time"
)

// Only the existing authenticated file route is downloadable. Page content
// cannot choose an arbitrary network target or receive the browser credential.
func saveBackendFile(ctx context.Context, origin, token, address, destination string) error {
	ref, err := url.Parse(address)
	if err != nil || ref.IsAbs() || ref.Host != "" || ref.Path != "/api/v1/files/raw" || ref.Fragment != "" {
		return errors.New("invalid file download URL")
	}
	base, err := url.Parse(origin)
	if err != nil || base.Scheme != "http" || base.Host == "" {
		return errors.New("download backend unavailable")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, base.ResolveReference(ref).String(), nil)
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Origin", base.String())
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("文件下载失败 (%d)", response.StatusCode)
	}
	file, err := os.CreateTemp(filepath.Dir(destination), ".astrorder-download-*")
	if err != nil {
		return err
	}
	name := file.Name()
	defer os.Remove(name)
	_, err = io.Copy(file, response.Body)
	closeErr := file.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(name, destination)
}

func (d *DesktopService) SaveDownload(ctx context.Context, address, filename string) (bool, error) {
	if err := requireWindow(ctx, d.shell.window); err != nil {
		return false, err
	}
	s := d.shell
	if s.app == nil || s.app.Dialog == nil || s.config.Backend == nil || s.ctl == nil {
		return false, errors.New("文件保存服务不可用")
	}
	filename = filepath.Base(filename)
	if filename == "." || filename == "" {
		filename = "download"
	}
	destination, err := s.app.Dialog.SaveFile().AttachToWindow(s.window).SetMessage("保存文件").SetFilename(filename).PromptForSingleSelection()
	if err != nil || destination == "" {
		return false, err
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Minute)
	defer cancel()
	token, err := s.ctl.Login(ctx)
	if err != nil {
		return false, err
	}
	if err := saveBackendFile(ctx, s.config.Backend.String(), token, address, destination); err != nil {
		return false, err
	}
	return true, nil
}
