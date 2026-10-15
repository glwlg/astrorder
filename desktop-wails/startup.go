package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

// Set only by the installer build; a development binary has no production defaults.
var defaultPackaged = "false"

func preparePackaged(cfg *Config, ctl *CtlClient) error {
	if !cfg.Packaged {
		return nil
	}
	if ctl == nil {
		return fmt.Errorf("astrorderctl executable is missing")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	_, err := ctl.invoke(ctx, "provision", "--root", cfg.RootDir, "--state-dir", cfg.StateDir, "--data-dir", filepath.Join(cfg.StateDir, "data"), "--app-executable", filepath.Join(cfg.RootDir, "bin", "astrorder-app.exe"), "--desktop-origin")
	if err != nil {
		return err
	}
	// Decode only the public port field; never return or log the envelope.
	raw, err := os.ReadFile(filepath.Join(cfg.StateDir, "production.json"))
	if err != nil {
		return err
	}
	var settings struct {
		Environment struct {
			Port json.RawMessage `json:"ASTRORDER_PORT"`
		} `json:"environment"`
	}
	if err = json.Unmarshal(raw, &settings); err != nil {
		return fmt.Errorf("invalid public desktop settings")
	}
	portText := string(settings.Environment.Port)
	if len(portText) > 0 && portText[0] == '"' {
		if err := json.Unmarshal(settings.Environment.Port, &portText); err != nil {
			return fmt.Errorf("invalid App port in desktop settings")
		}
	}
	port, err := strconv.Atoi(portText)
	if err != nil || port < 1 || port > 65535 {
		return fmt.Errorf("invalid App port in desktop settings")
	}
	origin, _ := url.Parse("http://127.0.0.1:" + strconv.Itoa(port))
	if cfg.Backend != nil && cfg.Backend.Host != origin.Host {
		return fmt.Errorf("explicit backend does not match installed App settings")
	}
	cfg.Backend = origin
	return nil
}

func startKernels(ctx context.Context, run func(context.Context, string, string, bool) (map[string]any, error)) error {
	for _, target := range []string{"daemon", "app"} {
		result, err := run(ctx, target, "start", false)
		if err != nil {
			return fmt.Errorf("%s start: %w", target, err)
		}
		if result["ok"] != true || result["state"] != "running" {
			return fmt.Errorf("%s did not reach running state", target)
		}
	}
	return nil
}
func (s *ShellService) showError(title string, err error) {
	if err == nil {
		return
	}
	s.app.Dialog.Error().SetTitle(title).SetMessage(err.Error()).AttachToWindow(s.window).Show()
}
func (s *ShellService) exitClient() {
	if !s.opMu.TryLock() {
		s.showError("无法退出星序", fmt.Errorf("另一个服务操作正在执行"))
		return
	}
	defer s.opMu.Unlock()
	if s.ctl != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		result, err := s.ctl.Run(ctx, "app", "stop", false)
		cancel()
		if err != nil {
			s.showError("无法停止大内核", err)
			return
		}
		if result["ok"] != true {
			s.showError("无法停止大内核", fmt.Errorf("大内核停止未成功"))
			return
		}
	}
	if s.preview != nil {
		s.preview.close()
	}
	s.quit()
}
