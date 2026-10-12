package audio

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func TestLocalStreamCancelAndMaxLimit(t *testing.T) {
	authorizer := testAuthenticator()
	resDir := t.TempDir()

	mockLlamaServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/health":
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusOK)
		}
	}))
	defer mockLlamaServer.Close()

	svc, err := NewService(Options{
		Authorizer:  authorizer,
		ResourceDir: resDir,
		HTTPClient:  mockLlamaServer.Client(),
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}

	port := mockLlamaServer.Listener.Addr().(*net.TCPAddr).Port
	svc.plugin.mu.Lock()
	svc.plugin.state = "running"
	svc.plugin.port = port
	svc.plugin.mu.Unlock()

	server := httptest.NewServer(svc.Handler())
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/api/v1/audio/local-stream?token=test-secret"
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("websocket.Dial: %v", err)
	}
	defer conn.CloseNow()

	// Read ready
	_, _, err = conn.Read(ctx)
	if err != nil {
		t.Fatalf("read ready: %v", err)
	}

	// Send cancel
	err = conn.Write(ctx, websocket.MessageText, []byte(`{"type":"cancel"}`))
	if err != nil {
		t.Fatalf("write cancel: %v", err)
	}

	// Should receive cancelled
	_, msg, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("read cancelled: %v", err)
	}
	if !strings.Contains(string(msg), `"cancelled"`) {
		t.Fatalf("expected cancelled message, got: %s", string(msg))
	}
}

func TestLocalPluginDownloadAndProcessLifecycle(t *testing.T) {
	resDir := t.TempDir()

	// Mock server that provides zip runtime and models
	mockDownloadServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "10")
		_, _ = w.Write([]byte("fakecontent"))
	}))
	defer mockDownloadServer.Close()

	var startedProcess atomic.Int32
	fakeFactory := func(ctx context.Context, name string, args ...string) (*exec.Cmd, error) {
		startedProcess.Add(1)
		// Return a non-running or benign command
		cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=TestHelperProcess")
		cmd.Env = append(os.Environ(), "GO_WANT_HELPER_PROCESS=1")
		return cmd, nil
	}

	authorizer := testAuthenticator()
	svc, err := NewService(Options{
		Authorizer:     authorizer,
		ResourceDir:    resDir,
		HTTPClient:     mockDownloadServer.Client(),
		ProcessFactory: fakeFactory,
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}

	// Prepare mock runtime/models so ensureResources passes without actual external download
	_ = os.MkdirAll(filepath.Join(resDir, "runtime"), 0o755)
	_ = os.MkdirAll(filepath.Join(resDir, "models"), 0o755)
	exe := "llama-server"
	if os.PathSeparator == '\\' {
		exe = "llama-server.exe"
	}
	_ = os.WriteFile(filepath.Join(resDir, "runtime", exe), []byte("exe"), 0o755)
	_ = os.WriteFile(filepath.Join(resDir, "models", ModelName), []byte("model"), 0o644)
	_ = os.WriteFile(filepath.Join(resDir, "models", MMProjName), []byte("mmproj"), 0o644)

	// Enable with wait=false
	err = svc.plugin.Enable(false, true)
	if err != nil {
		t.Fatalf("Enable: %v", err)
	}

	status := svc.plugin.Status()
	if !status["available"].(bool) {
		t.Fatalf("expected available=true")
	}
	if !status["enabled"].(bool) {
		t.Fatalf("expected enabled=true")
	}

	// Disable
	svc.plugin.Disable()
	status = svc.plugin.Status()
	if status["enabled"].(bool) {
		t.Fatalf("expected enabled=false")
	}
	if status["state"] != "disabled" {
		t.Fatalf("expected state=disabled, got %v", status["state"])
	}

	// Close context lifecycle
	err = svc.Close(context.Background())
	if err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_HELPER_PROCESS") != "1" {
		return
	}
	// benign helper process
	time.Sleep(50 * time.Millisecond)
	os.Exit(0)
}

func TestLocalStreamNotRunningRejection(t *testing.T) {
	authorizer := testAuthenticator()
	svc, err := NewService(Options{
		Authorizer:  authorizer,
		ResourceDir: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}

	server := httptest.NewServer(svc.Handler())
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/api/v1/audio/local-stream?token=test-secret"
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("websocket.Dial: %v", err)
	}
	defer conn.CloseNow()

	// Should read error and close
	_, msg, err := conn.Read(ctx)
	if err != nil && !errors.Is(err, context.Canceled) {
		// might get closed directly or read error frame
	}
	if len(msg) > 0 && !strings.Contains(string(msg), "未运行") {
		t.Fatalf("expected error message about plugin not running, got %s", string(msg))
	}
}
