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

type probeRecorder struct {
	mu       sync.Mutex
	calls    []string
	versions map[string]string
	failures map[string]error
}

func (p *probeRecorder) GetVersion(_ context.Context, kind, connID string, _ map[string]any) (string, bool, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	key := kind + "|" + connID
	p.calls = append(p.calls, key)
	if err, ok := p.failures[key]; ok {
		return "", false, err
	}
	version := p.versions[key]
	if version == "" {
		version = "9.9.9"
	}
	return version, true, nil
}

func (p *probeRecorder) callList() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.calls...)
}

type fakeOSProber struct {
	mu     sync.Mutex
	calls  []string
	result string
	err    error
}

func (f *fakeOSProber) ProbeOS(_ context.Context, connID string, _ map[string]any) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, connID)
	return f.result, f.err
}

func (f *fakeOSProber) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

func environmentsFixture(t *testing.T, server *fakeDaemonService, versioner connections.VersionExecutor, osProber connections.OSProber) (http.Handler, *store.Store) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "environments.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if _, err := st.SaveSSHConnection(context.Background(), map[string]any{
		"id": "saved", "host": "private-fixture.invalid", "display_name": "Fixture Host", "state": "connected", "detail": "fixture connected",
	}); err != nil {
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
	handler, err := connections.NewHandler(connections.Options{Store: st, Client: client, VersionExecutor: versioner, OSProber: osProber,
		RemoteExecutable: &recordingExecutableResolver{results: map[string]string{"codex": "/opt/fixture/codex", "grok": "/opt/fixture/grok"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return handler, st
}

func decodeEnvironmentItem(t *testing.T, body []byte) map[string]any {
	t.Helper()
	var item map[string]any
	if err := json.Unmarshal(body, &item); err != nil {
		t.Fatalf("decode environment item: %v body=%s", err, body)
	}
	return item
}

func environmentAgents(t *testing.T, item map[string]any) []map[string]any {
	t.Helper()
	raw, ok := item["agents"].([]any)
	if !ok {
		t.Fatalf("environment item has no agents list: %#v", item)
	}
	agents := make([]map[string]any, 0, len(raw))
	for _, entry := range raw {
		agent, ok := entry.(map[string]any)
		if !ok {
			t.Fatalf("agent entry is not an object: %#v", entry)
		}
		agents = append(agents, agent)
	}
	return agents
}

func TestDiscoverEnvironmentProbesRealAvailabilityAndCachesForListing(t *testing.T) {
	connID := "saved"
	owned := true
	sshHermes := pb.RuntimeKind_RUNTIME_KIND_SSH_HERMES
	server := &fakeDaemonService{}
	// The connected remote Hermes claim is confirmed by an explicit live
	// ownership verdict from the daemon for this connection's control session.
	server.statusResponse.Store(&pb.GetStatusResponse{
		DaemonId: "daemon-test-123",
		Runtimes: []*pb.RuntimeStatus{{Runtime: pb.RuntimeKind_RUNTIME_KIND_SSH_HERMES, Registered: true, OwnedSessions: 1}},
		Sessions: []*pb.SessionSnapshot{{Session: &pb.SessionRef{SessionId: "probe-hermes-saved", ConnectionId: &connID}, Status: pb.SessionStatus_SESSION_STATUS_IDLE, RuntimeOwned: &owned, Runtime: &sshHermes}},
	})
	recorder := &probeRecorder{failures: map[string]error{"codex|saved": errors.New("codex not installed on remote")}}
	osProber := &fakeOSProber{result: "Linux"}
	handler, _ := environmentsFixture(t, server, recorder, osProber)

	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/v1/environments/saved/discover", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("discover: HTTP=%d body=%s", w.Code, w.Body.String())
	}
	item := decodeEnvironmentItem(t, w.Body.Bytes())
	if item["id"] != "saved" || item["method"] != "ssh" || item["discovered"] != true || item["os"] != "Linux" || item["name"] != "Fixture Host" {
		t.Fatalf("discover did not return the environment item contract: %#v", item)
	}
	want := []string{"hermes|saved", "codex|saved", "grok|saved"}
	if calls := recorder.callList(); strings.Join(calls, ",") != strings.Join(want, ",") {
		t.Fatalf("discover did not probe each kind exactly once: %v", calls)
	}
	agents := environmentAgents(t, item)
	if len(agents) != 4 {
		t.Fatalf("expected four agent entries, got %#v", agents)
	}
	if agents[0]["kind"] != "hermes" || agents[0]["available"] != true || agents[0]["state"] != "connected" {
		t.Fatalf("hermes probe success lost: %#v", agents[0])
	}
	if agents[1]["kind"] != "codex" || agents[1]["available"] != false {
		t.Fatalf("failed codex probe fabricated availability: %#v", agents[1])
	}
	if agents[0]["executable"] != nil {
		t.Fatalf("discover fabricated an executable path: %#v", agents[0])
	}

	w = httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/environments", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("list: HTTP=%d body=%s", w.Code, w.Body.String())
	}
	if got := recorder.callList(); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("GET triggered external probes: %v", got)
	}
	if osProber.callCount() != 1 {
		t.Fatalf("GET triggered OS probes: %d", osProber.callCount())
	}
	var list struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Items) != 2 || list.Items[1]["id"] != "saved" || list.Items[1]["discovered"] != true || list.Items[1]["os"] != "Linux" {
		t.Fatalf("listing lost cached discovery: %#v", list.Items)
	}
	listed := environmentAgents(t, list.Items[1])
	if listed[0]["state"] != "connected" || listed[0]["available"] != true || listed[1]["available"] != false || listed[1]["state"] != "disconnected" {
		t.Fatalf("listing lost discovered availability: %#v", listed)
	}
}

func TestDiscoverEnvironmentDoesNotFabricateOSWhenProbeFails(t *testing.T) {
	server := &fakeDaemonService{}
	recorder := &probeRecorder{}
	osProber := &fakeOSProber{err: errors.New("ssh probe failed")}
	handler, _ := environmentsFixture(t, server, recorder, osProber)

	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/v1/environments/saved/discover", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("discover: HTTP=%d body=%s", w.Code, w.Body.String())
	}
	item := decodeEnvironmentItem(t, w.Body.Bytes())
	if item["os"] != nil {
		t.Fatalf("failed OS probe fabricated an os value: %#v", item["os"])
	}
	if item["discovered"] != true {
		t.Fatalf("version probes succeeded but item is not discovered: %#v", item)
	}
}

func TestDiscoverUnknownEnvironmentReturnsNotFound(t *testing.T) {
	server := &fakeDaemonService{}
	handler, _ := environmentsFixture(t, server, &probeRecorder{}, &fakeOSProber{result: "Linux"})
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/v1/environments/missing/discover", nil))
	if w.Code != http.StatusNotFound {
		t.Fatalf("discover unknown: HTTP=%d body=%s", w.Code, w.Body.String())
	}
}

func TestDiscoverLocalReturnsSameEnvironmentItemStructure(t *testing.T) {
	server := &fakeDaemonService{}
	server.statusResponse.Store(&pb.GetStatusResponse{DaemonId: "daemon-test-123", Runtimes: []*pb.RuntimeStatus{{Runtime: pb.RuntimeKind_RUNTIME_KIND_HERMES, Registered: true}}})
	recorder := &probeRecorder{}
	osProber := &fakeOSProber{result: "ignored"}
	handler, _ := environmentsFixture(t, server, recorder, osProber)

	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/v1/environments/local/discover", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("local discover: HTTP=%d body=%s", w.Code, w.Body.String())
	}
	item := decodeEnvironmentItem(t, w.Body.Bytes())
	if item["id"] != "local" || item["method"] != "local" || item["discovered"] != true {
		t.Fatalf("local discover lost the environment item contract: %#v", item)
	}
	agents := environmentAgents(t, item)
	if len(agents) != 4 || agents[0]["kind"] != "hermes" || agents[1]["kind"] != "codex" || agents[2]["kind"] != "grok" {
		t.Fatalf("local discover did not return the Agent catalog: %#v", agents)
	}
	want := []string{"hermes|local", "codex|local", "grok|local", "antigravity|local"}
	if calls := recorder.callList(); strings.Join(calls, ",") != strings.Join(want, ",") {
		t.Fatalf("local discover did not probe each local CLI exactly once: %v", calls)
	}
	if osProber.callCount() != 0 {
		t.Fatalf("local discover probed a remote OS: %d", osProber.callCount())
	}
}

func TestDiscoveryCacheIsolatesChangedSSHTarget(t *testing.T) {
	server := &fakeDaemonService{}
	recorder := &probeRecorder{}
	osProber := &fakeOSProber{result: "Linux"}
	handler, st := environmentsFixture(t, server, recorder, osProber)

	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/v1/environments/saved/discover", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("discover: HTTP=%d body=%s", w.Code, w.Body.String())
	}

	// Editing the SSH target must not reuse the previous probe result.
	if _, err := st.SaveSSHConnection(context.Background(), map[string]any{
		"id": "saved", "host": "changed-fixture.invalid", "display_name": "Fixture Host", "state": "disconnected",
	}); err != nil {
		t.Fatal(err)
	}
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/environments", nil))
	var list struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if list.Items[1]["discovered"] != false || list.Items[1]["os"] != nil {
		t.Fatalf("changed SSH target reused the stale discovery result: %#v", list.Items[1])
	}
	for _, agent := range environmentAgents(t, list.Items[1]) {
		if agent["available"] == true {
			t.Fatalf("changed SSH target reused stale availability: %#v", agent)
		}
	}
}

func TestDiscoverUnreachableRemoteReportsFailureWithoutCaching(t *testing.T) {
	server := &fakeDaemonService{}
	recorder := &probeRecorder{failures: map[string]error{
		"hermes|saved": errors.New("ssh unreachable"),
		"codex|saved":  errors.New("ssh unreachable"),
		"grok|saved":   errors.New("ssh unreachable"),
	}}
	osProber := &fakeOSProber{err: errors.New("ssh unreachable")}
	handler, _ := environmentsFixture(t, server, recorder, osProber)

	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/v1/environments/saved/discover", nil))
	if w.Code != http.StatusBadGateway {
		t.Fatalf("unreachable discover must not report success: HTTP=%d body=%s", w.Code, w.Body.String())
	}

	// A failed discovery must not be cached as success; the listing stays unknown.
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/environments", nil))
	var list struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if list.Items[1]["discovered"] != false || list.Items[1]["os"] != nil {
		t.Fatalf("failed discovery leaked into the listing: %#v", list.Items[1])
	}
}

func TestUndiscoveredRemoteStaysUnknownInListing(t *testing.T) {
	server := &fakeDaemonService{}
	recorder := &probeRecorder{}
	osProber := &fakeOSProber{result: "Linux"}
	handler, st := environmentsFixture(t, server, recorder, osProber)
	if _, err := st.UpdateSSHConnectionState(context.Background(), "saved", "configured", "saved, not connected", nil); err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/environments", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("list: HTTP=%d body=%s", w.Code, w.Body.String())
	}
	if len(recorder.callList()) != 0 || osProber.callCount() != 0 {
		t.Fatalf("GET triggered external probes before discovery: %v", recorder.callList())
	}
	var list struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Items) != 2 {
		t.Fatalf("expected local + saved items: %#v", list.Items)
	}
	remote := list.Items[1]
	if remote["discovered"] != false || remote["os"] != nil {
		t.Fatalf("undiscovered remote claims discovery: %#v", remote)
	}
	for _, agent := range environmentAgents(t, remote) {
		if agent["available"] != false || agent["state"] == "connected" {
			t.Fatalf("undiscovered, unconnected remote agent claims availability: %#v", agent)
		}
	}
}

func TestEnvironmentAgentActionsReturnSnapshotAndPersistChoices(t *testing.T) {
	server := &fakeDaemonService{}
	handler, st := environmentsFixture(t, server, &probeRecorder{}, &fakeOSProber{result: "Linux"})
	if _, err := st.UpdateSSHConnectionState(context.Background(), "saved", "configured", "saved, not connected", nil); err != nil {
		t.Fatal(err)
	}

	for _, step := range []struct {
		action  string
		enabled bool
		state   string
	}{
		{"connect", true, "connected"},
		{"disconnect", false, "disconnected"},
		{"restart", true, "connected"},
	} {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/v1/environments/saved/agents/hermes/"+step.action, nil))
		if w.Code != http.StatusOK {
			t.Fatalf("%s: HTTP=%d body=%s", step.action, w.Code, w.Body.String())
		}
		var snapshot struct {
			Items []map[string]any `json:"items"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &snapshot); err != nil {
			t.Fatalf("%s response is not an environment snapshot: %v body=%s", step.action, err, w.Body.String())
		}
		if len(snapshot.Items) != 2 || snapshot.Items[0]["method"] != "local" || snapshot.Items[1]["id"] != "saved" {
			t.Fatalf("%s did not return the environment snapshot contract: %#v", step.action, snapshot.Items)
		}
		enabled, present, err := st.GetAgentConnectionChoice(context.Background(), "saved:hermes")
		if err != nil || !present || enabled != step.enabled {
			t.Fatalf("%s did not persist the connection choice: enabled=%v present=%v err=%v", step.action, enabled, present, err)
		}
		row, err := st.GetSSHConnection(context.Background(), "saved")
		if err != nil || row == nil || row["state"] != step.state {
			t.Fatalf("%s did not reflect the confirmed state in the environment snapshot: state=%v err=%v", step.action, row, err)
		}
	}
}

func TestUnconfirmedDisconnectDoesNotRecordChoice(t *testing.T) {
	server := &fakeDaemonService{disconnectResponse: &pb.DisconnectRuntimeResponse{}}
	handler, st := environmentsFixture(t, server, &probeRecorder{}, &fakeOSProber{result: "Linux"})
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/v1/environments/saved/agents/hermes/disconnect", nil))
	if w.Code != http.StatusConflict {
		t.Fatalf("unconfirmed disconnect: HTTP=%d body=%s", w.Code, w.Body.String())
	}
	_, present, err := st.GetAgentConnectionChoice(context.Background(), "saved:hermes")
	if err != nil || present {
		t.Fatalf("unconfirmed disconnect persisted a choice: present=%v err=%v", present, err)
	}
}

// The subprocess is the test binary acting as an OpenSSH argv fixture, not a
// real remote host.
func TestNativeVersionExecutorUsesStoredSSHInt64Port(t *testing.T) {
	argvFile := filepath.Join(t.TempDir(), "version-argv.json")
	t.Setenv("ASTRORDER_SSH_VERSION_ARGV", argvFile)
	executor := &connections.NativeVersionExecutor{SSHBinary: os.Args[0]}
	version, _, err := executor.GetVersion(context.Background(), "codex", "remote-fixture", map[string]any{
		"settings": map[string]any{"host": "example.invalid", "port": int64(2222), "user": "tester"},
	})
	if err != nil {
		t.Fatalf("GetVersion over ssh fixture: %v", err)
	}
	if version != "1.2.3" {
		t.Fatalf("unexpected parsed version: %q", version)
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
	for _, want := range []string{"-p 2222", "-l tester", "example.invalid k=codex;", `exec "$p" --version`, "BatchMode=yes"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("ssh probe argv missing %q: %v", want, argv)
		}
	}
}

// A discover run must execute the configured SSH binary for version and OS
// probes alike; the default executors must not silently fall back to a
// different ssh binary than the injected one.
func TestDiscoverUsesInjectedSSHBinaryForVersionAndOSProbes(t *testing.T) {
	versionArgvFile := filepath.Join(t.TempDir(), "version-argv.json")
	osArgvFile := filepath.Join(t.TempDir(), "os-argv.json")
	t.Setenv("ASTRORDER_SSH_VERSION_ARGV", versionArgvFile)
	t.Setenv("ASTRORDER_SSH_OS_ARGV", osArgvFile)
	st, err := store.Open(filepath.Join(t.TempDir(), "injected-ssh.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if _, err := st.SaveSSHConnection(context.Background(), map[string]any{"id": "saved", "host": "private-fixture.invalid", "display_name": "Fixture Host", "state": "configured"}); err != nil {
		t.Fatal(err)
	}
	handler, err := connections.NewHandler(connections.Options{Store: st, SSHBinary: os.Args[0]})
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/v1/environments/saved/discover", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("discover with injected ssh binary: HTTP=%d body=%s", w.Code, w.Body.String())
	}
	item := decodeEnvironmentItem(t, w.Body.Bytes())
	if item["os"] != "Linux" {
		t.Fatalf("OS probe did not run through the injected ssh binary: %#v", item["os"])
	}
	for _, agent := range environmentAgents(t, item) {
		if agent["kind"] == "antigravity" {
			if agent["state"] != "unsupported" || agent["available"] != false {
				t.Fatalf("unexpected remote Antigravity availability: %#v", agent)
			}
			continue
		}
		if agent["available"] != true {
			t.Fatalf("version probe did not run through the injected ssh binary: %#v", agent)
		}
	}
	raw, err := os.ReadFile(versionArgvFile)
	if err != nil {
		t.Fatal("injected ssh binary was not executed for version probes", err)
	}
	var argv []string
	if err := json.Unmarshal(raw, &argv); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(argv, " ")
	for _, want := range []string{"BatchMode=yes", "--version", "private-fixture.invalid"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("version probe argv missing %q: %v", want, argv)
		}
	}
	raw, err = os.ReadFile(osArgvFile)
	if err != nil {
		t.Fatal("injected ssh binary was not executed for the OS probe", err)
	}
	if err := json.Unmarshal(raw, &argv); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(argv, " "), "uname -s") {
		t.Fatalf("OS probe argv missing uname: %v", argv)
	}
}
