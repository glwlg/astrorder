package configuration

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"astrorder.dev/session-daemon/internal/protocol"
	core "astrorder.dev/session-daemon/internal/runtime"
)

type stubLiveAdapter struct {
	mu       sync.Mutex
	sessions map[string]string
	closed   bool
}

func (a *stubLiveAdapter) Create(_ context.Context, _ core.Request) (core.Result, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.sessions == nil {
		a.sessions = map[string]string{}
	}
	a.sessions["live-1"] = "running"
	return core.Result{SessionID: "live-1", Status: "running", Payload: map[string]any{"status": "running"}}, nil
}

func (a *stubLiveAdapter) Command(_ context.Context, req core.Request) (core.Result, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed {
		return core.Result{}, errors.New("adapter closed")
	}
	if req.Action == "session.close" {
		delete(a.sessions, req.SessionID)
		return core.Result{SessionID: req.SessionID, Status: "idle", Payload: map[string]any{"closed": true}}, nil
	}
	return core.Result{SessionID: req.SessionID, Status: "running", Payload: map[string]any{"accepted": true}}, nil
}

func (a *stubLiveAdapter) Snapshot() map[string]core.Result {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := map[string]core.Result{}
	for id, st := range a.sessions {
		out[id] = core.Result{SessionID: id, Status: st}
	}
	return out
}

func (a *stubLiveAdapter) Close() error {
	a.mu.Lock()
	a.closed = true
	a.mu.Unlock()
	return nil
}

func writeTestConfigFile(t *testing.T, path string, cfg Config) {
	t.Helper()
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(tmp, path); err != nil {
		t.Fatal(err)
	}
}

func boolPtr(v bool) *bool { return &v }

func TestManagerHotReloadAddsUpdatesRemovesAndPreservesRunningSessions(t *testing.T) {
	stateDir := t.TempDir()
	workspace := t.TempDir()
	cfgPath := filepath.Join(stateDir, "session-daemon.json")

	writeTestConfigFile(t, cfgPath, Config{
		Runtimes: []Runtime{
			{Type: "pty", Allowed: []string{workspace}},
		},
		ModelConfig: &ModelConfig{Home: workspace},
	})

	d, err := protocol.Open("secret", filepath.Join(stateDir, "daemon.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()

	mgr := NewManager(d, ManagerOptions{
		ConfigPath:  cfgPath,
		StateDir:    stateDir,
		RefreshPath: func(...string) string { return "" },
		LookPath:    func(string) (string, error) { return "", execErrNotFound },
		UserHomeDir: func() (string, error) { return workspace, nil },
	})

	initial, err := mgr.ReloadInitial()
	if err != nil {
		t.Fatalf("ReloadInitial failed: %v", err)
	}
	if !reflect.DeepEqual(initial.AddedRuntimes, []string{"pty"}) {
		t.Fatalf("initial added = %v, want [pty]", initial.AddedRuntimes)
	}
	if d.HasRuntime("codex") || !d.HasRuntime("pty") || !d.HasRuntime("codex-ssh") {
		t.Fatalf("unexpected initial runtimes: %v", d.RegisteredRuntimes())
	}

	// Simulate an active running session on an existing runtime before reload.
	live := &stubLiveAdapter{}
	if _, err := d.SwapRuntime("codex", live, nil); err != nil {
		t.Fatal(err)
	}
	resp := d.ExecuteSessionControl(context.Background(), map[string]any{
		"action":     "session.create",
		"agent_type": "codex",
		"params":     map[string]any{},
	})
	if resp["action"] != "session.create.result" {
		t.Fatalf("failed to create stub live session: %+v", resp)
	}

	// Now write codex + grok into session-daemon.json and hot-reload.
	writeTestConfigFile(t, cfgPath, Config{
		Runtimes: []Runtime{
			{Type: "pty", Allowed: []string{workspace}},
			{Type: "codex", Executable: "/opt/codex-v1", Workspace: workspace, Allowed: []string{workspace}, AgentID: "local-codex"},
			{Type: "grok", Executable: "/opt/grok-v1", Workspace: workspace, Allowed: []string{workspace}, AgentID: "local-grok"},
		},
		ModelConfig: &ModelConfig{Home: workspace},
	})

	res1, err := mgr.Reload(context.Background(), protocol.RuntimeReloadOptions{ProbePath: boolPtr(false)})
	if err != nil {
		t.Fatalf("reload 1 failed: %v", err)
	}
	if !reflect.DeepEqual(res1.AddedRuntimes, []string{"codex", "grok"}) || !reflect.DeepEqual(res1.UnchangedRuntimes, []string{"pty"}) {
		t.Fatalf("unexpected reload 1 diff: %+v", res1)
	}
	if res1.PreservedSessions != 1 {
		t.Fatalf("preserved sessions = %d, want 1", res1.PreservedSessions)
	}

	// Existing running session on previous codex adapter MUST still be alive and responsive!
	live.mu.Lock()
	wasClosed := live.closed
	live.mu.Unlock()
	if wasClosed {
		t.Fatal("hot reload closed adapter with active running session")
	}
	sendResp := d.ExecuteSessionControl(context.Background(), map[string]any{
		"action":     "session.send",
		"session_id": "live-1",
	})
	if sendResp["action"] != "session.send.result" {
		t.Fatalf("active session disrupted by hot reload: %+v", sendResp)
	}

	// Update codex executable path and remove grok; pty stays unchanged.
	writeTestConfigFile(t, cfgPath, Config{
		Runtimes: []Runtime{
			{Type: "pty", Allowed: []string{workspace}},
			{Type: "codex", Executable: "/opt/codex-v2", Workspace: workspace, Allowed: []string{workspace}, AgentID: "local-codex"},
		},
		ModelConfig: &ModelConfig{Home: workspace},
	})
	res2, err := mgr.Reload(context.Background(), protocol.RuntimeReloadOptions{ProbePath: boolPtr(false)})
	if err != nil {
		t.Fatalf("reload 2 failed: %v", err)
	}
	if !reflect.DeepEqual(res2.UpdatedRuntimes, []string{"codex"}) || !reflect.DeepEqual(res2.RemovedRuntimes, []string{"grok"}) || !reflect.DeepEqual(res2.UnchangedRuntimes, []string{"pty"}) {
		t.Fatalf("unexpected reload 2 diff: %+v", res2)
	}
	if d.HasRuntime("grok") || !d.HasRuntime("codex") {
		t.Fatalf("unexpected registered runtimes after reload 2: %v", d.RegisteredRuntimes())
	}

	// Malformed JSON must fail cleanly without disturbing registered runtimes or active session.
	if err := os.WriteFile(cfgPath, []byte(`{"runtimes":[broken`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := mgr.Reload(context.Background(), protocol.RuntimeReloadOptions{ProbePath: boolPtr(false)}); err == nil {
		t.Fatal("malformed config accepted")
	}
	if !d.HasRuntime("codex") || !d.HasRuntime("pty") {
		t.Fatalf("failed reload cleared existing runtimes: %v", d.RegisteredRuntimes())
	}
	sendResp2 := d.ExecuteSessionControl(context.Background(), map[string]any{
		"action":     "session.send",
		"session_id": "live-1",
	})
	if sendResp2["action"] != "session.send.result" {
		t.Fatalf("active session broken after failed reload: %+v", sendResp2)
	}
}

var execErrNotFound = errors.New("executable file not found in $PATH")

func TestManagerReloadProbesPathAndManagedRuntimes(t *testing.T) {
	stateDir := t.TempDir()
	workspace := t.TempDir()
	nodeDir := filepath.Join(stateDir, "runtimes", "node")
	uvBinDir := filepath.Join(stateDir, "runtimes", "uv", "bin")
	extraDir := t.TempDir()
	for _, dir := range []string{nodeDir, uvBinDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	cfgPath := filepath.Join(stateDir, "session-daemon.json")
	writeTestConfigFile(t, cfgPath, Config{
		Runtimes: []Runtime{
			{Type: "pty", Allowed: []string{workspace}},
		},
	})

	d := protocol.New("secret")
	defer d.Close()

	var seenRefreshDirs []string
	codexInstalled := false
	mgr := NewManager(d, ManagerOptions{
		ConfigPath: cfgPath,
		StateDir:   stateDir,
		RefreshPath: func(dirs ...string) string {
			seenRefreshDirs = append([]string(nil), dirs...)
			return strings.Join(dirs, string(os.PathListSeparator))
		},
		LookPath: func(name string) (string, error) {
			if name == "codex" && codexInstalled {
				return filepath.Join(extraDir, "codex"), nil
			}
			return "", execErrNotFound
		},
		UserHomeDir: func() (string, error) { return workspace, nil },
	})

	if _, err := mgr.ReloadInitial(); err != nil {
		t.Fatal(err)
	}
	if d.HasRuntime("codex") {
		t.Fatal("codex unexpectedly registered before installation")
	}

	// Simulate user installing codex + portable node/uv, then triggering hot reload.
	codexInstalled = true
	res, err := mgr.Reload(context.Background(), protocol.RuntimeReloadOptions{
		ExtraPathDirs: []string{extraDir},
	})
	if err != nil {
		t.Fatalf("Reload with PATH probe failed: %v", err)
	}
	if !reflect.DeepEqual(res.AddedRuntimes, []string{"codex"}) {
		t.Fatalf("expected auto-discovered codex in added_runtimes, got: %+v", res)
	}
	if !d.HasRuntime("codex") {
		t.Fatal("auto-discovered codex not registered on daemon")
	}

	// Verify managed runtimes and extra dir were forwarded to PATH refresh.
	hasDir := func(target string) bool {
		for _, dir := range seenRefreshDirs {
			if filepath.Clean(dir) == filepath.Clean(target) {
				return true
			}
		}
		return false
	}
	for _, want := range []string{extraDir, nodeDir, uvBinDir} {
		if !hasDir(want) {
			t.Fatalf("refreshPath missing expected dir %q in %v", want, seenRefreshDirs)
		}
	}

	// A subsequent config-only reload (e.g. file watcher with ProbePath=false)
	// must NOT drop previously PATH-discovered runtimes that were not in session-daemon.json.
	res2, err := mgr.Reload(context.Background(), protocol.RuntimeReloadOptions{ProbePath: boolPtr(false)})
	if err != nil {
		t.Fatal(err)
	}
	if !d.HasRuntime("codex") || len(res2.RemovedRuntimes) != 0 {
		t.Fatalf("config-only reload unexpectedly removed PATH-discovered codex: %+v", res2)
	}
}

func TestManagerWatchConfigFileHotRefreshesAndIgnoresInvalidWrites(t *testing.T) {
	stateDir := t.TempDir()
	workspace := t.TempDir()
	cfgPath := filepath.Join(stateDir, "session-daemon.json")
	writeTestConfigFile(t, cfgPath, Config{
		Runtimes: []Runtime{
			{Type: "pty", Allowed: []string{workspace}},
		},
	})

	d := protocol.New("secret")
	defer d.Close()

	mgr := NewManager(d, ManagerOptions{
		ConfigPath:  cfgPath,
		StateDir:    stateDir,
		RefreshPath: func(...string) string { return "" },
		LookPath:    func(string) (string, error) { return "", execErrNotFound },
		UserHomeDir: func() (string, error) { return workspace, nil },
	})
	if _, err := mgr.ReloadInitial(); err != nil {
		t.Fatal(err)
	}

	watchCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go mgr.WatchConfigFile(watchCtx, 25*time.Millisecond)

	// Write transient malformed JSON first — must not remove pty.
	if err := os.WriteFile(cfgPath, []byte(`{invalid`), 0o600); err != nil {
		t.Fatal(err)
	}
	time.Sleep(80 * time.Millisecond)
	if !d.HasRuntime("pty") {
		t.Fatal("watcher dropped pty on malformed JSON")
	}

	// Now write valid config adding grok — watcher should pick it up automatically.
	writeTestConfigFile(t, cfgPath, Config{
		Runtimes: []Runtime{
			{Type: "pty", Allowed: []string{workspace}},
			{Type: "grok", Executable: "/opt/grok", Workspace: workspace, Allowed: []string{workspace}, AgentID: "local-grok"},
		},
	})

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if d.HasRuntime("grok") {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("WatchConfigFile did not register newly added grok runtime in time; registered=%v", d.RegisteredRuntimes())
}

func TestManagerConcurrentReloadsAreThreadSafe(t *testing.T) {
	stateDir := t.TempDir()
	workspace := t.TempDir()
	cfgPath := filepath.Join(stateDir, "session-daemon.json")
	writeTestConfigFile(t, cfgPath, Config{
		Runtimes: []Runtime{
			{Type: "pty", Allowed: []string{workspace}},
			{Type: "codex", Executable: "/opt/codex", Workspace: workspace, Allowed: []string{workspace}, AgentID: "local-codex"},
		},
	})

	d := protocol.New("secret")
	defer d.Close()

	mgr := NewManager(d, ManagerOptions{
		ConfigPath:  cfgPath,
		StateDir:    stateDir,
		RefreshPath: func(...string) string { return "" },
		LookPath:    func(string) (string, error) { return "", execErrNotFound },
		UserHomeDir: func() (string, error) { return workspace, nil },
	})
	if _, err := mgr.ReloadInitial(); err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 5; j++ {
				if _, err := mgr.Reload(context.Background(), protocol.RuntimeReloadOptions{ProbePath: boolPtr(false)}); err != nil {
					t.Errorf("concurrent reload failed: %v", err)
				}
			}
		}()
	}
	wg.Wait()
}
