package main

import (
	"bytes"
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"net/http/httputil"
	"path"
	"strings"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
)

//go:embed all:assets
var embedded embed.FS

const bridgePath = "/astrorder-desktop-bridge.js"

// On Windows, Wails applies WebviewWindowOptions.JS only to inline-HTML
// windows, so a URL window never runs it. The bridge is therefore served as a
// module and injected ahead of the React entry: module scripts run in document
// order, so window.astrorderDesktop exists before main.tsx reads it.
const bridgeTemplate = `import * as svc from '/bindings/astrorder.dev/desktop/desktopservice.js';
import { installFilePaths, resolvePreviewImages } from '/desktop-files.js';
window.__ASTRORDER_BACKEND_HOST__ = %s;
const getPathForFile = installFilePaths(window);

window.astrorderDesktop = Object.freeze({
  getPathForFile,
  openPreview: async (payload) => svc.OpenPreview({ images: await resolvePreviewImages(payload.images, window), index: payload.index ?? 0 }),
  status: () => svc.Status(),
  run: (target, action) => svc.Run(String(target), String(action)),
  getStartupSettings: () => svc.GetStartupSettings(),
  setStartupEnabled: (enabled) => svc.SetStartupEnabled(Boolean(enabled)),
  saveDownload: (url, filename) => svc.SaveDownload(String(url), String(filename)),
  setClipboardFiles: (paths) => svc.SetClipboardFiles(Array.isArray(paths) ? paths.map(String) : []),
  notify: (opts) => svc.Notify(String(opts?.title ?? ''), String(opts?.body ?? '')),
  minimize: () => svc.Minimize(),
  maximize: () => svc.Maximize(),
  close: () => svc.Close(),
  isMaximized: () => svc.IsMaximized(),
  openDirectoryDialog: (defaultPath) => svc.OpenDirectoryDialog(String(defaultPath ?? '')),
  openFileDialog: (defaultPath) => svc.OpenFileDialog(String(defaultPath ?? '')),
  openExternal: (url) => svc.OpenExternal(String(url)),
  showStatistics: (url, bounds, refresh) => svc.ShowStatistics(String(url), { ...bounds, scale: window.devicePixelRatio }, Boolean(refresh)),
  hideStatistics: () => svc.HideStatistics(),
});
document.documentElement.classList.add('is-desktop-app');
// No-op unless the shell was started with --smoke-report.
void svc.ReportReady();
`

func bridgeScript(cfg Config) []byte {
	host := ""
	if cfg.Backend != nil {
		host = cfg.Backend.Host
	}
	encoded, _ := json.Marshal(host)
	return []byte(fmt.Sprintf(bridgeTemplate, encoded))
}

func injectBridge(index []byte) ([]byte, error) {
	at := bytes.Index(index, []byte(`<script type="module"`))
	if at < 0 {
		return nil, fmt.Errorf("React index.html has no module entry for the desktop bridge")
	}
	tag := []byte(`<script type="module" src="` + bridgePath + `"></script>`)
	out := make([]byte, 0, len(index)+len(tag))
	out = append(out, index[:at]...)
	out = append(out, tag...)
	return append(out, index[at:]...), nil
}

func assetHandler(cfg Config) (http.Handler, error) {
	files, err := fs.Sub(embedded, "assets")
	if err != nil {
		return nil, err
	}
	appFiles, err := fs.Sub(files, "app")
	if err != nil {
		return nil, err
	}
	rawIndex, err := fs.ReadFile(appFiles, "index.html")
	if err != nil {
		return nil, err
	}
	index, err := injectBridge(rawIndex)
	if err != nil {
		return nil, err
	}
	bridge := bridgeScript(cfg)
	built := time.Now()
	var proxy *httputil.ReverseProxy
	if cfg.Backend != nil {
		proxy = httputil.NewSingleHostReverseProxy(cfg.Backend)
		proxy.ErrorHandler = func(w http.ResponseWriter, _ *http.Request, _ error) {
			http.Error(w, "isolated App Server is unavailable", http.StatusBadGateway)
		}
	}
	static := http.FileServer(http.FS(files))
	runtime := application.BundledAssetFileServer(files)
	app := http.FileServer(http.FS(appFiles))
	serveIndex := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		http.ServeContent(w, r, "index.html", built, bytes.NewReader(index))
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/wails/runtime.js":
			runtime.ServeHTTP(w, r)
			return
		case r.URL.Path == bridgePath:
			w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			w.Header().Set("Cache-Control", "no-store")
			_, _ = w.Write(bridge)
			return
		case strings.HasPrefix(r.URL.Path, "/api/") || strings.HasPrefix(r.URL.Path, "/ws/") || r.URL.Path == "/health" || strings.HasPrefix(r.URL.Path, "/attachments/") || strings.HasPrefix(r.URL.Path, "/static/"):
			if proxy == nil {
				http.Error(w, "no isolated backend configured; use --backend", http.StatusServiceUnavailable)
				return
			}
			// An opaque WebView2 stream can have a body without a length header.
			// ReverseProxy treats length zero as no body, so preserve it as an
			// unknown-length stream and let the transport encode it chunked.
			if r.Body != nil && r.Body != http.NoBody && r.ContentLength == 0 && r.Header.Get("Content-Length") == "" {
				r.ContentLength = -1
			}
			proxy.ServeHTTP(w, r)
			return
		case r.URL.Path == "/desktop-files.js" || r.URL.Path == "/image-preview.html":
			static.ServeHTTP(w, r)
			return
		case strings.HasPrefix(r.URL.Path, "/bindings/"):
			static.ServeHTTP(w, r)
			return
		}
		name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if name == "" || name == "index.html" {
			serveIndex(w, r)
			return
		}
		if _, err := fs.Stat(appFiles, name); err != nil {
			if path.Ext(name) != "" {
				http.NotFound(w, r)
				return
			}
			serveIndex(w, r)
			return
		}
		app.ServeHTTP(w, r)
	}), nil
}
