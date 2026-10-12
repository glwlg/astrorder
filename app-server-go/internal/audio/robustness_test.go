package audio

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"astrorder.dev/app-server/internal/store"
)

type failingStore struct {
	*store.Store
}

func (f *failingStore) SetPreference(ctx context.Context, key string, value any, overwrite bool) error {
	return errors.New("simulated store write failure")
}

func TestStoreWriteFailureDoesNotPretendSuccess(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer st.Close()

	authorizer := testAuthenticator()
	svc, err := NewService(Options{
		Store:       st,
		Authorizer:  authorizer,
		ResourceDir: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}

	// Close store to force error on SetPreference
	st.Close()

	// Enable should fail and not pretend success
	err = svc.plugin.Enable(false, true)
	if err == nil {
		t.Fatal("expected store write failure, got nil")
	}

	// PUT endpoint should return 500 when persist fails
	req := httptest.NewRequest(http.MethodPut, "/api/v1/audio/local-plugin", strings.NewReader(`{"enabled":true}`))
	req.Header.Set("Authorization", "Bearer test-secret")
	rec := httptest.NewRecorder()
	svc.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 on store failure, got %d", rec.Code)
	}
}

type roundTripperMock struct {
	roundTrip func(*http.Request) (*http.Response, error)
}

func (m roundTripperMock) RoundTrip(r *http.Request) (*http.Response, error) {
	return m.roundTrip(r)
}

func TestCloseDuringBlockedDownloadHaltsWithoutLateProcess(t *testing.T) {
	resDir := t.TempDir()

	downloadStarted := make(chan struct{})
	var closeOnce sync.Once

	mockTransport := roundTripperMock{
		roundTrip: func(r *http.Request) (*http.Response, error) {
			closeOnce.Do(func() {
				close(downloadStarted)
			})
			// Block until request context is cancelled
			<-r.Context().Done()
			return nil, r.Context().Err()
		},
	}
	mockClient := &http.Client{Transport: mockTransport}

	var processCreated atomic.Int32
	fakeFactory := func(ctx context.Context, name string, args ...string) (*exec.Cmd, error) {
		processCreated.Add(1)
		cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=TestHelperProcess")
		cmd.Env = append(os.Environ(), "GO_WANT_HELPER_PROCESS=1")
		return cmd, nil
	}

	appCtx, cancelApp := context.WithCancel(context.Background())
	defer cancelApp()

	svc, err := NewService(Options{
		Context:        appCtx,
		Authorizer:     testAuthenticator(),
		ResourceDir:    resDir,
		HTTPClient:     mockClient,
		ProcessFactory: fakeFactory,
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}

	// Start enabling, which triggers download
	err = svc.plugin.Enable(false, false)
	if err != nil {
		t.Fatalf("Enable: %v", err)
	}

	// Wait until download starts
	select {
	case <-downloadStarted:
	case <-time.After(3 * time.Second):
		t.Fatal("download did not start")
	}

	// Close service while download is blocked
	closeCtx, closeCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer closeCancel()

	err = svc.Close(closeCtx)
	if err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Ensure process was NEVER started
	if created := processCreated.Load(); created != 0 {
		t.Fatalf("expected 0 processes created after Close during download, got %d", created)
	}

	// Status should show disabled
	status := svc.plugin.Status()
	if status["state"] != "disabled" {
		t.Fatalf("expected state=disabled, got %v", status["state"])
	}
}

func TestStartupRestoreDoesNotTriggerImplicitDownload(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer st.Close()

	// Persist enabled=true in DB
	if err := st.SetPreference(context.Background(), EnabledKey, true, true); err != nil {
		t.Fatalf("SetPreference: %v", err)
	}

	resDir := t.TempDir() // Empty resources

	svc, err := NewService(Options{
		Store:       st,
		Authorizer:  testAuthenticator(),
		ResourceDir: resDir,
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}

	status := svc.plugin.Status()
	// Should not have downloaded, resources remain unavailable, state=error visible
	if status["available"].(bool) {
		t.Fatal("expected available=false")
	}
	if status["state"] != "error" {
		t.Fatalf("expected state=error, got %v", status["state"])
	}
	if status["error"] == nil || !strings.Contains(status["error"].(string), "未下载") {
		t.Fatalf("expected error explaining missing resources, got %v", status["error"])
	}
}

func TestAppLifetimeContextCancelsAndRejects(t *testing.T) {
	appCtx, cancelApp := context.WithCancel(context.Background())
	cancelApp() // already cancelled

	svc, err := NewService(Options{
		Context:     appCtx,
		Authorizer:  testAuthenticator(),
		ResourceDir: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}

	err = svc.plugin.Enable(false, false)
	if err == nil {
		t.Fatal("expected Enable to reject under cancelled appCtx, got nil")
	}
}
