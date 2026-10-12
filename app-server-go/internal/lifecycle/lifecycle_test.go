package lifecycle

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"sync/atomic"
	"testing"
	"time"
)

func TestIndependentProcessFlagsExcludeDetachedProcess(t *testing.T) {
	flags := IndependentProcessFlags()
	if flags&0x08000000 == 0 || flags&0x01000000 == 0 || flags&0x00000200 == 0 {
		t.Fatalf("required Windows flags missing: %#x", flags)
	}
	if flags&0x00000008 != 0 {
		t.Fatalf("DETACHED_PROCESS must never be used: %#x", flags)
	}
}

func TestAppLifecycleUsesExactIdentityHealthAndAuthenticatedExit(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("native Windows process identity test")
	}
	address := unusedLoopbackAddress(t)
	stateDir := t.TempDir()
	secret := "isolated-lifecycle-secret"
	options := AppOptions{
		ExecutablePath: os.Args[0],
		Arguments:      []string{"-test.run=^TestLifecycleHelperProcess$"},
		WorkingDir:     stateDir,
		Environment: append(os.Environ(),
			"ASTRORDER_LIFECYCLE_HELPER=1",
			"ASTRORDER_LIFECYCLE_ADDRESS="+address,
			"ASTRORDER_LIFECYCLE_SECRET="+secret,
		),
		StateDir:        stateDir,
		Address:         address,
		BrowserSecret:   secret,
		LogPath:         filepath.Join(stateDir, "app.log"),
		StartupTimeout:  10 * time.Second,
		ShutdownTimeout: 10 * time.Second,
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	started, err := StartApp(ctx, options)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, _ = StopApp(context.Background(), options)
	}()
	if started.State != StateRunning || !started.IdentityVerified || !started.Healthy || !started.PortOwned || started.Process.PID <= 0 || started.Process.CreationTime == 0 {
		t.Fatalf("invalid running status: %#v", started)
	}
	if !samePath(started.Process.ExecutablePath, os.Args[0]) {
		t.Fatalf("native executable identity = %q", started.Process.ExecutablePath)
	}

	again, err := StartApp(ctx, options)
	if err != nil {
		t.Fatal(err)
	}
	if again.Process != started.Process {
		t.Fatalf("duplicate start changed process identity: first=%#v second=%#v", started.Process, again.Process)
	}

	wrong := options
	wrong.BrowserSecret = "wrong-secret"
	if _, err := StopApp(ctx, wrong); err == nil {
		t.Fatal("unauthenticated App shutdown succeeded")
	}
	stillRunning, err := AppStatus(ctx, options)
	if err != nil || stillRunning.State != StateRunning || stillRunning.Process != started.Process {
		t.Fatalf("failed shutdown disturbed owned App: status=%#v err=%v", stillRunning, err)
	}

	stopped, err := StopApp(ctx, options)
	if err != nil {
		t.Fatal(err)
	}
	if stopped.State != StateStopped || stopped.Healthy || stopped.PortOwned {
		t.Fatalf("stop did not wait for exit and port release: %#v", stopped)
	}
	if _, err := InspectProcess(started.Process.PID); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("owned process still exists or identity lookup was ambiguous: %v", err)
	}
	port := mustPort(t, address)
	pids, err := ListeningPIDs(port)
	if err != nil || len(pids) != 0 {
		t.Fatalf("listener survived stop: pids=%v err=%v", pids, err)
	}
}

func TestValidateAppOptionsRejectsBroadStateOrExternalLogTargets(t *testing.T) {
	base := t.TempDir()
	volumeRoot := filepath.Clean(filepath.VolumeName(base) + string(filepath.Separator))
	address := unusedLoopbackAddress(t)
	if _, err := validateAppOptions(AppOptions{
		ExecutablePath: os.Args[0], StateDir: volumeRoot, Address: address,
	}); err == nil {
		t.Fatal("volume root was accepted as App lifecycle state directory")
	}
	stateDir := filepath.Join(base, "state")
	if _, err := validateAppOptions(AppOptions{
		ExecutablePath: os.Args[0], StateDir: stateDir, Address: address, LogPath: filepath.Join(base, "outside.log"),
	}); err == nil {
		t.Fatal("App log outside lifecycle state directory was accepted")
	}
}

func TestConcurrentStartAppUsesOneOwnedProcess(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("native Windows process identity test")
	}
	address := unusedLoopbackAddress(t)
	stateDir := t.TempDir()
	secret := "isolated-concurrent-start-secret"
	options := AppOptions{
		ExecutablePath: os.Args[0],
		Arguments:      []string{"-test.run=^TestLifecycleHelperProcess$"},
		WorkingDir:     stateDir,
		Environment: append(os.Environ(),
			"ASTRORDER_LIFECYCLE_HELPER=1",
			"ASTRORDER_LIFECYCLE_ADDRESS="+address,
			"ASTRORDER_LIFECYCLE_SECRET="+secret,
		),
		StateDir:        stateDir,
		Address:         address,
		BrowserSecret:   secret,
		LogPath:         filepath.Join(stateDir, "app.log"),
		StartupTimeout:  10 * time.Second,
		ShutdownTimeout: 10 * time.Second,
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	type outcome struct {
		status AppStatusResult
		err    error
	}
	outcomes := make(chan outcome, 2)
	for range 2 {
		go func() {
			status, err := StartApp(ctx, options)
			outcomes <- outcome{status: status, err: err}
		}()
	}
	first, second := <-outcomes, <-outcomes
	if first.err != nil || second.err != nil {
		t.Fatalf("concurrent starts failed: first=%v second=%v", first.err, second.err)
	}
	if first.status.State != StateRunning || first.status.Process != second.status.Process {
		t.Fatalf("concurrent starts created different identities: first=%#v second=%#v", first.status, second.status)
	}
	stopped, err := StopApp(ctx, options)
	if err != nil || stopped.State != StateStopped {
		t.Fatalf("stop after concurrent start: status=%#v err=%v", stopped, err)
	}
}

func TestAppStatusSeparatesHealthyResponseFromOwnedIdentity(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("native Windows process identity test")
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "ok", "service": "astrorder", "protocol_version": 1})
	})}
	go server.Serve(listener)
	defer server.Shutdown(context.Background())

	status, err := AppStatus(context.Background(), AppOptions{
		ExecutablePath: os.Args[0],
		StateDir:       t.TempDir(),
		Address:        listener.Addr().String(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if status.State != StateConflict || status.IdentityVerified || !status.Healthy {
		t.Fatalf("health was mistaken for process ownership: %#v", status)
	}
}

func TestStopAppWithoutOwnedMetadataRefusesHealthyForeignListener(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("native Windows process identity test")
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	shutdownCalls := 0
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/health" {
			_ = json.NewEncoder(w).Encode(map[string]any{"status": "ok", "service": "astrorder", "protocol_version": 1})
			return
		}
		shutdownCalls++
		w.WriteHeader(http.StatusOK)
	})}
	go server.Serve(listener)
	defer server.Shutdown(context.Background())

	_, err = StopApp(context.Background(), AppOptions{
		ExecutablePath: os.Args[0],
		StateDir:       filepath.Join(t.TempDir(), "missing-state"),
		Address:        listener.Addr().String(),
		BrowserSecret:  "must-not-be-sent",
	})
	if err == nil {
		t.Fatal("foreign listener without owned metadata was treated as stopped")
	}
	if shutdownCalls != 0 {
		t.Fatal("shutdown request reached a foreign listener")
	}
}

func TestIsolatedHTTPClientRefusesHealthAndShutdownRedirects(t *testing.T) {
	var healthRedirects atomic.Int32
	healthTarget := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		healthRedirects.Add(1)
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "ok", "service": "astrorder", "protocol_version": 1})
	}))
	defer healthTarget.Close()
	healthRedirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		http.Redirect(w, request, healthTarget.URL+"/health", http.StatusTemporaryRedirect)
	}))
	defer healthRedirector.Close()
	healthAddress := healthRedirector.Listener.Addr().String()
	healthOptions, err := validateAppOptions(AppOptions{
		ExecutablePath: os.Args[0],
		StateDir:       t.TempDir(),
		Address:        healthAddress,
	})
	if err != nil {
		t.Fatal(err)
	}
	if appHealthy(context.Background(), healthOptions) {
		t.Fatal("redirected health response was accepted")
	}
	if healthRedirects.Load() != 0 {
		t.Fatal("health client followed redirect")
	}

	secret := "isolated-redirect-secret"
	var shutdownRedirects atomic.Int32
	shutdownTarget := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		shutdownRedirects.Add(1)
		if request.Header.Get("Authorization") == "Bearer "+secret {
			t.Error("browser Authorization was forwarded to a different loopback port")
		}
		_ = json.NewEncoder(w).Encode(map[string]bool{"stopping": true})
	}))
	defer shutdownTarget.Close()
	shutdownRedirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		http.Redirect(w, request, shutdownTarget.URL+"/_desktop/shutdown", http.StatusTemporaryRedirect)
	}))
	defer shutdownRedirector.Close()
	shutdownOptions, err := validateAppOptions(AppOptions{
		ExecutablePath: os.Args[0],
		StateDir:       t.TempDir(),
		Address:        shutdownRedirector.Listener.Addr().String(),
		BrowserSecret:  secret,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := requestAppShutdown(context.Background(), shutdownOptions); err == nil {
		t.Fatal("redirected shutdown response was accepted")
	}
	if shutdownRedirects.Load() != 0 {
		t.Fatal("shutdown client followed and replayed redirect")
	}
}

func TestLifecycleHTTPRejectsTrailingJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/health":
			_, _ = w.Write([]byte(`{"status":"ok","service":"astrorder","protocol_version":1} {"extra":true}`))
		case "/_desktop/shutdown":
			_, _ = w.Write([]byte(`{"stopping":true} {"extra":true}`))
		}
	}))
	defer server.Close()
	options, err := validateAppOptions(AppOptions{
		ExecutablePath: os.Args[0],
		StateDir:       t.TempDir(),
		Address:        server.Listener.Addr().String(),
		BrowserSecret:  "isolated-trailing-body-secret",
	})
	if err != nil {
		t.Fatal(err)
	}
	if appHealthy(context.Background(), options) {
		t.Fatal("health accepted trailing JSON")
	}
	if err := requestAppShutdown(context.Background(), options); err == nil {
		t.Fatal("shutdown accepted trailing JSON")
	}
}

func TestLifecycleHelperProcess(t *testing.T) {
	if os.Getenv("ASTRORDER_LIFECYCLE_HELPER") != "1" {
		return
	}
	address := os.Getenv("ASTRORDER_LIFECYCLE_ADDRESS")
	secret := os.Getenv("ASTRORDER_LIFECYCLE_SECRET")
	server := &http.Server{}
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok","service":"astrorder","protocol_version":1}`))
	})
	mux.HandleFunc("/_desktop/shutdown", func(w http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Bearer "+secret {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"stopping":true}`))
		go func() {
			time.Sleep(25 * time.Millisecond)
			_ = server.Shutdown(context.Background())
		}()
	})
	server.Handler = mux
	listener, err := net.Listen("tcp4", address)
	if err != nil {
		t.Fatal(err)
	}
	if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
		t.Fatal(err)
	}
}

func unusedLoopbackAddress(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	return address
}

func mustPort(t *testing.T, address string) int {
	t.Helper()
	_, raw, err := net.SplitHostPort(address)
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(raw)
	if err != nil {
		t.Fatal(err)
	}
	return port
}

func TestProtectedDaemonStopRequiresAuthoritativeSafetyOrConfirmation(t *testing.T) {
	cases := []struct {
		name  string
		state AuthorityState
		err   error
	}{
		{name: "running", state: AuthorityState{Complete: true, PendingKnown: true, Sessions: map[string]string{"s": "running"}}},
		{name: "waiting approval", state: AuthorityState{Complete: true, PendingKnown: true, Sessions: map[string]string{"s": "waiting_approval"}}},
		{name: "pending", state: AuthorityState{Complete: true, PendingKnown: true, PendingOperations: 1}},
		{name: "unknown status", state: AuthorityState{Complete: true, PendingKnown: true, Sessions: map[string]string{"s": "mystery"}}},
		{name: "incomplete", state: AuthorityState{Complete: false, PendingKnown: true}},
		{name: "pending unknown", state: AuthorityState{Complete: true, PendingKnown: false}},
		{name: "query failed", err: errors.New("typed gRPC unavailable")},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			authority := &authorityFixture{state: test.state, err: test.err}
			if err := StopDaemon(context.Background(), authority, false); err == nil {
				t.Fatal("unsafe unconfirmed daemon stop was accepted")
			}
			if authority.shutdownCalls != 0 {
				t.Fatal("unsafe unconfirmed stop reached shutdown")
			}
		})
	}

	idle := &authorityFixture{state: AuthorityState{Complete: true, PendingKnown: true, Sessions: map[string]string{"s": "idle"}}}
	if err := StopDaemon(context.Background(), idle, false); err != nil {
		t.Fatal(err)
	}
	if idle.shutdownCalls != 1 || idle.lastConfirmed {
		t.Fatalf("authoritative idle stop = calls %d confirmed %v", idle.shutdownCalls, idle.lastConfirmed)
	}

	unknown := &authorityFixture{err: errors.New("typed gRPC unavailable")}
	if err := StopDaemon(context.Background(), unknown, true); err != nil {
		t.Fatal(err)
	}
	if unknown.shutdownCalls != 1 || !unknown.lastConfirmed {
		t.Fatalf("explicit confirmation was not relayed: calls %d confirmed %v", unknown.shutdownCalls, unknown.lastConfirmed)
	}
}

type authorityFixture struct {
	state         AuthorityState
	err           error
	shutdownCalls int
	lastConfirmed bool
}

func (fixture *authorityFixture) State(context.Context) (AuthorityState, error) {
	return fixture.state, fixture.err
}

func (fixture *authorityFixture) Shutdown(_ context.Context, confirmed bool) error {
	fixture.shutdownCalls++
	fixture.lastConfirmed = confirmed
	return nil
}

func (fixture *authorityFixture) String() string {
	return fmt.Sprintf("authorityFixture(calls=%d)", fixture.shutdownCalls)
}
