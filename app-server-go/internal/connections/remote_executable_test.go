package connections_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"astrorder.dev/app-server/internal/connections"
	"astrorder.dev/app-server/internal/daemonclient"
	"astrorder.dev/app-server/internal/store"
	pb "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
)

// init dispatches the helper-process mode before TestMain runs: the spawned
// child (the test binary invoked as an OpenSSH fixture) exits inside the
// helper, while the normal test process returns immediately because the
// trigger environment is unset.
func init() {
	remoteExecutableHelperProcess()
}

// remoteExecutableHelperProcess makes the test binary act as an OpenSSH argv
// fixture for the native resolver: it captures the full argv and emits
// scripted metadata output. It is not a real remote login.
func remoteExecutableHelperProcess() {
	file := os.Getenv("ASTRORDER_SSH_EXECUTABLE_ARGV")
	if file == "" || len(os.Args) < 2 || os.Args[1] != "-o" {
		return
	}
	raw, _ := json.Marshal(os.Args[1:])
	if err := os.WriteFile(file, raw, 0600); err != nil {
		os.Exit(2)
	}
	switch os.Getenv("ASTRORDER_SSH_EXECUTABLE_MODE") {
	case "multiline":
		os.Stdout.WriteString("/opt/native/codex\n/opt/native/grok\n")
	case "relative":
		os.Stdout.WriteString("bin/codex\n")
	case "oversize":
		os.Stdout.WriteString(strings.Repeat("x", 1<<20))
	case "nul":
		os.Stdout.Write([]byte("/opt/native/codex\x00evil\n"))
	case "fail":
		os.Exit(1)
	case "hang":
		time.Sleep(30 * time.Second)
	default:
		os.Stdout.WriteString("/opt/native/codex\n")
	}
	os.Exit(0)
}

type recordingExecutableResolver struct {
	mu      sync.Mutex
	calls   []string
	results map[string]string
	err     error
}

func (r *recordingExecutableResolver) Resolve(_ context.Context, kind string, _ map[string]any) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, kind)
	if r.err != nil {
		return "", r.err
	}
	if path, ok := r.results[kind]; ok {
		return path, nil
	}
	return "", errors.New("fixture has no result for " + kind)
}

func (r *recordingExecutableResolver) callList() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.calls...)
}

func remoteExecutableFixture(t *testing.T, server *fakeDaemonService, resolver connections.RemoteExecutableResolver) (http.Handler, *store.Store) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "remote-executable.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	_, err = st.SaveSSHConnection(context.Background(), map[string]any{
		"id": "saved", "host": "private-fixture.invalid", "port": 2222, "user": "fixture",
		"profile_name": "dev", "display_name": "Fixture Host", "workspace": "/srv/work",
		"state": "disconnected", "detail": "fixture disconnected",
	})
	if err != nil {
		t.Fatal(err)
	}
	server.token = "private-fixture-credential"
	address, closeServer := startFakeDaemon(t, server)
	t.Cleanup(closeServer)
	client, err := daemonclient.Dial(context.Background(), daemonclient.Config{Target: address, Credential: server.token, RequestTimeout: 2 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	handler, err := connections.NewHandler(connections.Options{Store: st, Client: client, RemoteExecutable: resolver})
	if err != nil {
		t.Fatal(err)
	}
	return handler, st
}

// The remote codex connect must carry the resolved executable plus the exact
// control identity into the formal AttachSession request, without inventing a
// hidden session or mutating the stored row.
func TestRemoteCodexConnectResolvesExecutableIntoAttachTarget(t *testing.T) {
	resolver := &recordingExecutableResolver{results: map[string]string{"codex": "/opt/native/codex"}}
	server := &fakeDaemonService{}
	handler, st := remoteExecutableFixture(t, server, resolver)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/v1/environments/saved/agents/codex/connect", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("codex connect: HTTP=%d body=%s", w.Code, w.Body.String())
	}
	if calls := resolver.callList(); len(calls) != 1 || calls[0] != "codex" {
		t.Fatalf("resolver calls = %v, want [codex]", calls)
	}
	if server.attachHit.Load() != 1 {
		t.Fatalf("attach ran %d times", server.attachHit.Load())
	}
	req := server.attachRequest.Load()
	if req == nil || req.Target.GetRuntime() != pb.RuntimeKind_RUNTIME_KIND_CODEX_SSH || req.Target.GetConnectionId() != "saved" {
		t.Fatalf("attach target identity changed: %v", req.GetTarget())
	}
	if req.Target.Ssh.GetCodexExecutable() != "/opt/native/codex" {
		t.Fatalf("resolved executable missing from attach target: %v", req.Target.Ssh)
	}
	if req.Target.Ssh.GetWorkspace() != "/srv/work" || req.Target.Ssh.GetProfileName() != "dev" {
		t.Fatalf("saved settings were lost: %v", req.Target.Ssh)
	}
	if req.Session.GetSessionId() != "probe-codex-saved" || req.Session.GetConnectionId() != "saved" || !req.RuntimeControl {
		t.Fatalf("control identity changed: %v", req.Session)
	}
	stored, err := st.GetSSHConnection(context.Background(), "saved")
	if err != nil || stored == nil || stored["state"] != "disconnected" {
		t.Fatalf("connect mutated the stored SSH row: %v err=%v", stored, err)
	}
}

// Grok restart resolves before the protected disconnect and attaches with the
// resolved executable.
func TestRemoteGrokRestartResolvesBeforeAttach(t *testing.T) {
	resolver := &recordingExecutableResolver{results: map[string]string{"grok": "/opt/native/grok"}}
	server := &fakeDaemonService{}
	handler, _ := remoteExecutableFixture(t, server, resolver)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/v1/environments/saved/agents/grok/restart", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("grok restart: HTTP=%d body=%s", w.Code, w.Body.String())
	}
	if server.disconnectHit.Load() != 1 || server.attachHit.Load() != 1 {
		t.Fatalf("restart control flow: disconnect=%d attach=%d", server.disconnectHit.Load(), server.attachHit.Load())
	}
	req := server.attachRequest.Load()
	if req == nil || req.Target.GetRuntime() != pb.RuntimeKind_RUNTIME_KIND_GROK_SSH || req.Target.Ssh.GetGrokExecutable() != "/opt/native/grok" || !req.RuntimeControl {
		t.Fatalf("grok restart attach target changed: %v", req)
	}
	if calls := resolver.callList(); len(calls) != 1 || calls[0] != "grok" {
		t.Fatalf("resolver calls = %v, want [grok]", calls)
	}
}

// A failed resolution must not reach the daemon at all: no attach, no
// disconnect (restart), no choice, no stored state change, no retry.
func TestRemoteExecutableResolutionFailureBlocksAttachAndRestart(t *testing.T) {
	for _, action := range []string{"connect", "restart"} {
		t.Run(action, func(t *testing.T) {
			resolver := &recordingExecutableResolver{err: errors.New("ssh probe failed")}
			server := &fakeDaemonService{}
			handler, st := remoteExecutableFixture(t, server, resolver)
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/v1/environments/saved/agents/codex/"+action, nil))
			if w.Code != http.StatusBadGateway {
				t.Fatalf("resolution failure: HTTP=%d body=%s", w.Code, w.Body.String())
			}
			if server.attachHit.Load() != 0 || server.disconnectHit.Load() != 0 {
				t.Fatalf("failed resolution reached the daemon: attach=%d disconnect=%d", server.attachHit.Load(), server.disconnectHit.Load())
			}
			if _, present, err := st.GetAgentConnectionChoice(context.Background(), "saved:codex"); err != nil || present {
				t.Fatalf("failed resolution persisted a choice: present=%v err=%v", present, err)
			}
			stored, err := st.GetSSHConnection(context.Background(), "saved")
			if err != nil || stored == nil || stored["state"] != "disconnected" {
				t.Fatalf("failed resolution mutated the stored row: %v err=%v", stored, err)
			}
		})
	}
}

// Disconnect never needs the resolver.
func TestRemoteDisconnectDoesNotResolveExecutable(t *testing.T) {
	resolver := &recordingExecutableResolver{err: errors.New("must not be called")}
	server := &fakeDaemonService{}
	handler, _ := remoteExecutableFixture(t, server, resolver)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/v1/environments/saved/agents/codex/disconnect", nil))
	if w.Code != http.StatusOK || server.disconnectHit.Load() != 1 {
		t.Fatalf("disconnect: HTTP=%d disconnect=%d body=%s", w.Code, server.disconnectHit.Load(), w.Body.String())
	}
	if calls := resolver.callList(); len(calls) != 0 {
		t.Fatalf("disconnect invoked the resolver: %v", calls)
	}
}

// The native resolver executes one bounded OpenSSH command with the exact
// target flags and a static shell script that never embeds connection data.
func TestNativeRemoteExecutableResolverProbesWithBoundedSSHCommand(t *testing.T) {
	argvFile := filepath.Join(t.TempDir(), "executable-argv.json")
	t.Setenv("ASTRORDER_SSH_EXECUTABLE_ARGV", argvFile)
	resolver := &connections.NativeRemoteExecutableResolver{SSHBinary: os.Args[0]}
	conn := map[string]any{"settings": map[string]any{"host": "private-fixture.invalid", "port": 2222, "user": "fixture", "workspace": "/srv/work"}}
	for _, kind := range []string{"codex", "grok"} {
		t.Run(kind, func(t *testing.T) {
			path, err := resolver.Resolve(context.Background(), kind, conn)
			if err != nil {
				t.Fatalf("resolve %s: %v", kind, err)
			}
			if path != "/opt/native/codex" {
				t.Fatalf("resolved path = %q", path)
			}
			raw, err := os.ReadFile(argvFile)
			if err != nil {
				t.Fatal("ssh probe was not executed", err)
			}
			var argv []string
			if err := json.Unmarshal(raw, &argv); err != nil {
				t.Fatal(err)
			}
			joined := strings.Join(argv, " ")
			for _, want := range []string{"-o BatchMode=yes", "-o ConnectTimeout=4", "-p 2222", "-l fixture", "private-fixture.invalid", "command -v"} {
				if !strings.Contains(joined, want) {
					t.Fatalf("%s probe argv missing %q: %v", kind, want, argv)
				}
			}
			script := argv[len(argv)-1]
			if strings.Contains(script, "private-fixture.invalid") || strings.Contains(script, "/srv/work") || strings.Contains(script, "fixture") {
				t.Fatalf("%s probe leaked connection data into the shell source: %q", kind, script)
			}
			switch kind {
			case "codex":
				if !strings.Contains(script, "k=codex") || !strings.Contains(script, ".nvm/versions/node") {
					t.Fatalf("codex probe script lost its kind mapping: %q", script)
				}
			case "grok":
				if !strings.Contains(script, "k=grok") || !strings.Contains(script, ".grok/bin/grok") {
					t.Fatalf("grok probe script lost its kind mapping: %q", script)
				}
			}
		})
	}
}

// Illegal probe output never becomes an executable: multiline, relative,
// oversized, NUL-poisoned, or nonzero-exit output is an explicit error.
func TestNativeRemoteExecutableResolverRejectsInvalidProbeOutput(t *testing.T) {
	for _, mode := range []string{"multiline", "relative", "oversize", "nul", "fail"} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv("ASTRORDER_SSH_EXECUTABLE_ARGV", filepath.Join(t.TempDir(), "argv.json"))
			t.Setenv("ASTRORDER_SSH_EXECUTABLE_MODE", mode)
			resolver := &connections.NativeRemoteExecutableResolver{SSHBinary: os.Args[0]}
			_, err := resolver.Resolve(context.Background(), "codex", map[string]any{"settings": map[string]any{"host": "private-fixture.invalid"}})
			if err == nil {
				t.Fatalf("%s probe output was accepted", mode)
			}
		})
	}
}

// A cancelled context fails fast and a hung probe dies on its deadline; no
// probe process survives the call.
func TestNativeRemoteExecutableResolverHonorsContext(t *testing.T) {
	t.Setenv("ASTRORDER_SSH_EXECUTABLE_ARGV", filepath.Join(t.TempDir(), "argv.json"))
	conn := map[string]any{"settings": map[string]any{"host": "private-fixture.invalid"}}
	resolver := &connections.NativeRemoteExecutableResolver{SSHBinary: os.Args[0]}

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := resolver.Resolve(cancelled, "codex", conn); !errors.Is(err, context.Canceled) {
		t.Fatalf("pre-cancelled resolve error = %v", err)
	}

	t.Setenv("ASTRORDER_SSH_EXECUTABLE_MODE", "hang")
	deadline, stop := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer stop()
	start := time.Now()
	if _, err := resolver.Resolve(deadline, "codex", conn); err == nil {
		t.Fatal("hung probe did not fail")
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("context deadline not honored: %v", elapsed)
	}
}

// An explicit already-resolved absolute path is used directly without probing.
func TestNativeRemoteExecutableResolverUsesExplicitPathWithoutProbe(t *testing.T) {
	argvFile := filepath.Join(t.TempDir(), "executable-argv.json")
	t.Setenv("ASTRORDER_SSH_EXECUTABLE_ARGV", argvFile)
	resolver := &connections.NativeRemoteExecutableResolver{SSHBinary: os.Args[0]}
	path, err := resolver.Resolve(context.Background(), "codex", map[string]any{
		"settings": map[string]any{"host": "private-fixture.invalid", "codex_executable": "/opt/native/explicit"},
	})
	if err != nil || path != "/opt/native/explicit" {
		t.Fatalf("explicit path: path=%q err=%v", path, err)
	}
	if _, err := os.Stat(argvFile); !os.IsNotExist(err) {
		t.Fatalf("explicit path still spawned a probe: %v", err)
	}
}

// Only the codex/grok whitelist resolves; other kinds never spawn a probe.
func TestNativeRemoteExecutableResolverRejectsUnsupportedKind(t *testing.T) {
	argvFile := filepath.Join(t.TempDir(), "executable-argv.json")
	t.Setenv("ASTRORDER_SSH_EXECUTABLE_ARGV", argvFile)
	resolver := &connections.NativeRemoteExecutableResolver{SSHBinary: os.Args[0]}
	if _, err := resolver.Resolve(context.Background(), "hermes", map[string]any{"settings": map[string]any{"host": "private-fixture.invalid"}}); err == nil {
		t.Fatal("unsupported kind resolved")
	}
	if _, err := os.Stat(argvFile); !os.IsNotExist(err) {
		t.Fatalf("unsupported kind spawned a probe: %v", err)
	}
}
