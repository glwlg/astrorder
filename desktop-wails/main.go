package main

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/json"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
)

//go:embed assets/icon.png
var appIcon []byte

//go:embed assets/tray.ico
var trayIcon []byte

//go:embed assets/tray-dark.ico
var trayDarkIcon []byte

func main() {
	backend := flag.String("backend", "", "isolated App origin, e.g. http://127.0.0.1:30102")
	state := flag.String("state-dir", "", "isolated development state directory")
	check := flag.Bool("check", false, "validate configuration and embedded React assets without opening a window")
	smoke := flag.String("smoke-report", "", "write a GUI startup report and exit; development verification only")
	packaged := flag.Bool("packaged", defaultPackaged == "true", "run as packaged desktop app")
	flag.Parse()

	if *packaged && *smoke != "" {
		log.Fatal("smoke verification requires a development binary without --packaged")
	}
	cfg, err := loadConfig(*backend, *state, *packaged)
	if err != nil {
		log.Fatal(err)
	}
	if _, err := fs.Stat(embedded, "assets/app/index.html"); err != nil {
		log.Fatal("React assets missing: run node scripts/desktop-wails.mjs build")
	}

	ctl, ctlErr := NewCtlClient(cfg.RootDir, cfg.StateDir)

	if *check {
		_ = json.NewEncoder(os.Stdout).Encode(ShellInfo{
			Framework:         "Wails v3.0.0-beta.28",
			BackendConfigured: cfg.Backend != nil,
			StateDir:          cfg.StateDir,
			KernelManagement:  ctlErr == nil,
		})
		return
	}

	startupErr := preparePackaged(&cfg, ctl)
	handler, err := assetHandler(cfg)
	if err != nil {
		log.Fatal(err)
	}

	if err := os.MkdirAll(filepath.Join(cfg.StateDir, "webview2"), 0700); err != nil {
		log.Fatal(err)
	}

	service := &ShellService{
		config:      cfg,
		ctl:         ctl,
		smokeReport: *smoke,
		isPackaged:  *packaged,
	}

	service.preview = &PreviewService{shell: service}
	service.statistics = &StatisticsView{shell: service}
	var quitting atomic.Bool
	windowsOptions, err := desktopWindowsOptions(filepath.Join(cfg.StateDir, "webview2"))
	if err != nil {
		log.Fatal(err)
	}

	app := application.New(application.Options{
		Name:        "Astrorder",
		Description: "星序桌面客户端",
		Icon:        appIcon,
		Services:    []application.Service{application.NewService(&DesktopService{shell: service}), application.NewService(service.preview)},
		Assets:      application.AssetOptions{Handler: handler, DisableLogging: true},
		Windows:     windowsOptions,
		SingleInstance: &application.SingleInstanceOptions{
			UniqueID: fmt.Sprintf("com.astrorder.desktop.%x", sha256.Sum256([]byte(strings.ToLower(cfg.StateDir)))),
			OnSecondInstanceLaunch: func(data application.SecondInstanceData) {
				if service.window != nil {
					service.window.Restore()
					service.window.Show().Focus()
				}
			},
		},
	})

	service.app = app
	service.quit = func() {
		quitting.Store(true)
		app.Quit()
	}

	// The smoke run loads the real React entry; the injected desktop bridge
	// calls ReportReady once the generated bindings resolve.
	startURL := "/"

	service.window = app.Window.NewWithOptions(application.WebviewWindowOptions{
		Name:           "main",
		Title:          "星序",
		Width:          1440,
		Height:         900,
		MinWidth:       960,
		MinHeight:      640,
		URL:            startURL,
		EnableFileDrop: true,
	})

	service.window.RegisterHook(events.Common.WindowClosing, func(e *application.WindowEvent) {
		if !quitting.Load() {
			service.Close()
			e.Cancel()
		}
	})

	systemTray := app.SystemTray.New()
	systemTray.SetIcon(trayIcon)
	systemTray.SetDarkModeIcon(trayDarkIcon)
	systemTray.SetTooltip("星序 · 正在查询内核状态")

	showMainWindow := func() {
		if service.window != nil {
			service.window.Restore()
			service.window.Show().Focus()
		}
	}

	service.installTray(systemTray, showMainWindow)

	if cfg.Packaged && *smoke == "" {
		app.Event.OnApplicationEvent(events.Common.ApplicationStarted, func(_ *application.ApplicationEvent) {
			go func() {
				if startupErr != nil {
					service.showError("星序初始化失败", startupErr)
					return
				}
				ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
				defer cancel()
				service.opMu.Lock()
				err := startKernels(ctx, ctl.Run)
				service.opMu.Unlock()
				service.refreshTray()
				if err != nil {
					service.showError("星序启动失败", err)
					return
				}
				service.window.SetURL("/")
			}()
		})
	}
	if *smoke != "" {
		app.Event.OnApplicationEvent(events.Common.ApplicationStarted, func(_ *application.ApplicationEvent) {
			time.AfterFunc(20*time.Second, app.Quit)
		})
	}

	if err := app.Run(); err != nil {
		log.Fatal(err)
	}
}
