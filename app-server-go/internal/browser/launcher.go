package browser

import (
	"encoding/base64"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// SessionMarker creates the window.name marker for durable recovery.
// marker = "astrorder:" + urlsafe_base64(sessionKey).rstrip("=")
func SessionMarker(sessionKey string) string {
	encoded := base64.RawURLEncoding.EncodeToString([]byte(sessionKey))
	return sessionPrefix + encoded
}

// MarkerSessionKey parses the sessionKey from window.name.
func MarkerSessionKey(marker string) (string, bool) {
	if !strings.HasPrefix(marker, sessionPrefix) {
		return "", false
	}
	val := strings.TrimPrefix(marker, sessionPrefix)
	decoded, err := base64.RawURLEncoding.DecodeString(val)
	if err != nil {
		return "", false
	}
	return string(decoded), true
}

// FindEdgeExecutable locates Microsoft Edge on Windows/macOS/Linux.
func FindEdgeExecutable() (string, error) {
	if runtime.GOOS == "windows" {
		for _, envVar := range []string{"PROGRAMFILES(X86)", "PROGRAMFILES", "LOCALAPPDATA"} {
			root := os.Getenv(envVar)
			if root != "" {
				candidate := filepath.Join(root, "Microsoft", "Edge", "Application", "msedge.exe")
				if fi, err := os.Stat(candidate); err == nil && !fi.IsDir() {
					return candidate, nil
				}
			}
		}
		// Also check chrome as fallback if user has chrome
		for _, envVar := range []string{"PROGRAMFILES(X86)", "PROGRAMFILES", "LOCALAPPDATA"} {
			root := os.Getenv(envVar)
			if root != "" {
				candidate := filepath.Join(root, "Google", "Chrome", "Application", "chrome.exe")
				if fi, err := os.Stat(candidate); err == nil && !fi.IsDir() {
					return candidate, nil
				}
			}
		}
	} else if runtime.GOOS == "darwin" {
		candidates := []string{
			"/Applications/Microsoft Edge.app/Contents/MacOS/Microsoft Edge",
			"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
		}
		for _, c := range candidates {
			if fi, err := os.Stat(c); err == nil && !fi.IsDir() {
				return c, nil
			}
		}
	} else {
		// Linux
		for _, name := range []string{"microsoft-edge", "microsoft-edge-stable", "google-chrome", "google-chrome-stable", "chromium", "chromium-browser"} {
			if path, err := exec.LookPath(name); err == nil {
				return path, nil
			}
		}
	}
	return "", fmt.Errorf("未找到 Microsoft Edge 或 Chrome，无法启动可视浏览器")
}

// ExtensionPath locates tools/browser_extension.
func ExtensionPath() string {
	candidates := []string{
		"tools/browser_extension",
		"../tools/browser_extension",
		"../../tools/browser_extension",
		"../../../tools/browser_extension",
	}
	for _, c := range candidates {
		abs, err := filepath.Abs(c)
		if err == nil {
			if fi, err := os.Stat(abs); err == nil && fi.IsDir() {
				return abs
			}
		}
	}
	return "tools/browser_extension"
}

// LaunchBrowserProcess starts the visible Edge process with remote debugging.
// Only called on actual user request when CDP port is unreachable. NEVER in tests.
func LaunchBrowserProcess(port int) error {
	exe, err := FindEdgeExecutable()
	if err != nil {
		return err
	}

	localAppData := os.Getenv("LOCALAPPDATA")
	if localAppData == "" {
		localAppData = os.TempDir()
	}
	profile := filepath.Join(localAppData, "Astrorder", "browser-profile")
	_ = os.MkdirAll(profile, 0700)

	extPath := ExtensionPath()

	args := []string{
		fmt.Sprintf("--remote-debugging-port=%d", port),
		fmt.Sprintf("--user-data-dir=%s", profile),
		"--no-first-run",
		"--no-default-browser-check",
		"--new-window",
		"about:blank",
	}

	if fi, err := os.Stat(extPath); err == nil && fi.IsDir() {
		args = append(args,
			fmt.Sprintf("--disable-extensions-except=%s", extPath),
			fmt.Sprintf("--load-extension=%s", extPath),
		)
	}

	cmd := exec.Command(exe, args...)
	// Detach process
	hideWindow(cmd)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("launch edge process: %w", err)
	}
	return nil
}

// OpenSystemURL opens a URL in user's default desktop browser without CDP.
func OpenSystemURL(targetURL string) error {
	cleanURL := strings.TrimSpace(targetURL)
	if !strings.HasPrefix(cleanURL, "http://") && !strings.HasPrefix(cleanURL, "https://") {
		cleanURL = "http://" + cleanURL
	}

	switch runtime.GOOS {
	case "windows":
		cmd := exec.Command("cmd.exe", "/c", "start", "", cleanURL)
		hideWindow(cmd)
		return cmd.Start()
	case "darwin":
		return exec.Command("open", cleanURL).Start()
	default:
		return exec.Command("xdg-open", cleanURL).Start()
	}
}

// RevisionNow returns current monotonic/unix revision.
func RevisionNow() int64 {
	return time.Now().UnixNano()
}
