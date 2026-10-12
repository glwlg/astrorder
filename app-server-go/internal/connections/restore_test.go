package connections_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"astrorder.dev/app-server/internal/connections"
	"astrorder.dev/app-server/internal/daemonclient"
	"astrorder.dev/app-server/internal/store"
	pb "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
)

type recordingResolver struct {
	results map[string]string
	err     error
}

func (r *recordingResolver) Resolve(_ context.Context, kind string, _ map[string]any) (string, error) {
	if r.err != nil {
		return "", r.err
	}
	if path, ok := r.results[kind]; ok {
		return path, nil
	}
	return "", errors.New("not resolved: " + kind)
}

func setupRestoreFixture(t *testing.T, daemon *fakeDaemonService, resolver connections.RemoteExecutableResolver) (*connections.Handler, *store.Store) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "restore-test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	daemon.token = "restore-credential"
	addr, closeServer := startFakeDaemon(t, daemon)
	t.Cleanup(closeServer)

	client, err := daemonclient.Dial(context.Background(), daemonclient.Config{
		Target:         addr,
		Credential:     daemon.token,
		RequestTimeout: 2 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })

	h, err := connections.NewHandler(connections.Options{
		Store:            st,
		Client:           client,
		RemoteExecutable: resolver,
		VersionExecutor:  &fakeVersionExecutor{},
	})
	if err != nil {
		t.Fatal(err)
	}
	return h, st
}

func TestRestoreConnectsLocalCodexAndHermes(t *testing.T) {
	daemon := &fakeDaemonService{}
	h, _ := setupRestoreFixture(t, daemon, nil)

	if err := h.Restore(context.Background()); err != nil {
		t.Fatalf("Restore failed: %v", err)
	}

	daemon.attachRequestsMu.Lock()
	reqs := append([]*pb.AttachSessionRequest(nil), daemon.attachRequests...)
	daemon.attachRequestsMu.Unlock()

	foundCodex, foundHermes := false, false
	for _, req := range reqs {
		if req.Session != nil {
			if req.Session.SessionId == "probe-codex-local" && req.Target != nil && req.Target.Runtime == pb.RuntimeKind_RUNTIME_KIND_CODEX {
				foundCodex = true
			}
			if req.Session.SessionId == "probe-hermes-local" && req.Target != nil && req.Target.Runtime == pb.RuntimeKind_RUNTIME_KIND_HERMES {
				foundHermes = true
			}
		}
	}
	if !foundCodex {
		t.Errorf("Restore did not attach local codex; requests: %v", reqs)
	}
	if !foundHermes {
		t.Errorf("Restore did not attach local hermes; requests: %v", reqs)
	}
}

func TestRestoreSkipsDisabledLocalCodex(t *testing.T) {
	daemon := &fakeDaemonService{}
	h, st := setupRestoreFixture(t, daemon, nil)

	// Explicitly disable local codex
	if err := st.SetAgentConnectionChoice(context.Background(), "local:codex", false); err != nil {
		t.Fatal(err)
	}

	if err := h.Restore(context.Background()); err != nil {
		t.Fatalf("Restore failed: %v", err)
	}

	daemon.attachRequestsMu.Lock()
	reqs := append([]*pb.AttachSessionRequest(nil), daemon.attachRequests...)
	daemon.attachRequestsMu.Unlock()

	for _, req := range reqs {
		if req.Session != nil && req.Session.SessionId == "probe-codex-local" {
			t.Errorf("Restore attached disabled local codex: %v", req)
		}
	}
}

func TestRestoreRestoresEnabledRemoteConnections(t *testing.T) {
	daemon := &fakeDaemonService{}
	resolver := &recordingResolver{
		results: map[string]string{
			"codex": "/home/luwei/.vite-plus/bin/codex",
		},
	}
	h, st := setupRestoreFixture(t, daemon, resolver)

	// Create an SSH connection
	conn, err := st.SaveSSHConnection(context.Background(), map[string]any{
		"id":           "remote-debian",
		"host":         "192.168.1.50",
		"display_name": "Debian Server",
		"user":         "luwei",
		"workspace":    "/home/luwei/workspace",
		"state":        "configured",
	})
	if err != nil {
		t.Fatal(err)
	}

	connID := conn["id"].(string)
	// Enable remote codex choice
	if err := st.SetAgentConnectionChoice(context.Background(), connID+":codex", true); err != nil {
		t.Fatal(err)
	}
	// Explicitly disable grok choice
	if err := st.SetAgentConnectionChoice(context.Background(), connID+":grok", false); err != nil {
		t.Fatal(err)
	}

	if err := h.Restore(context.Background()); err != nil {
		t.Fatalf("Restore failed: %v", err)
	}

	daemon.attachRequestsMu.Lock()
	reqs := append([]*pb.AttachSessionRequest(nil), daemon.attachRequests...)
	daemon.attachRequestsMu.Unlock()

	foundRemoteCodex := false
	for _, req := range reqs {
		if req.Session != nil && req.Session.SessionId == "probe-codex-"+connID {
			foundRemoteCodex = true
			if req.Target == nil || req.Target.Runtime != pb.RuntimeKind_RUNTIME_KIND_CODEX_SSH {
				t.Errorf("expected RuntimeKind CODEX_SSH, got %v", req.Target)
			}
			if req.Target.Ssh == nil || req.Target.Ssh.GetCodexExecutable() != "/home/luwei/.vite-plus/bin/codex" {
				t.Errorf("expected codex executable /home/luwei/.vite-plus/bin/codex, got %v", req.Target.Ssh)
			}
		}
		if req.Session != nil && req.Session.SessionId == "probe-grok-"+connID {
			t.Errorf("Restore attached disabled remote grok: %v", req)
		}
	}
	if !foundRemoteCodex {
		t.Errorf("Restore did not attach enabled remote codex; requests: %v", reqs)
	}
}

func TestRestoreRestoresConnectedHermesWhenChoiceNotSet(t *testing.T) {
	daemon := &fakeDaemonService{}
	h, st := setupRestoreFixture(t, daemon, nil)

	conn, err := st.SaveSSHConnection(context.Background(), map[string]any{
		"id":           "remote-hermes-box",
		"host":         "192.168.1.60",
		"display_name": "Hermes Remote",
		"user":         "luwei",
		"workspace":    "/home/luwei/hermes",
		"state":        "connected",
	})
	if err != nil {
		t.Fatal(err)
	}
	connID := conn["id"].(string)

	if err := h.Restore(context.Background()); err != nil {
		t.Fatalf("Restore failed: %v", err)
	}

	daemon.attachRequestsMu.Lock()
	reqs := append([]*pb.AttachSessionRequest(nil), daemon.attachRequests...)
	daemon.attachRequestsMu.Unlock()

	found := false
	for _, req := range reqs {
		if req.Session != nil && req.Session.SessionId == "probe-hermes-"+connID {
			found = true
			if req.Target == nil || req.Target.Runtime != pb.RuntimeKind_RUNTIME_KIND_SSH_HERMES {
				t.Errorf("expected RuntimeKind SSH_HERMES, got %v", req.Target)
			}
		}
	}
	if !found {
		t.Errorf("Restore did not attach remote Hermes saved with state=connected")
	}
}

func TestAutoConnectRunsAsynchronously(t *testing.T) {
	daemon := &fakeDaemonService{}
	h, _ := setupRestoreFixture(t, daemon, nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	h.AutoConnect(ctx)

	// Poll briefly for the background attach to arrive
	deadline := time.Now().Add(3 * time.Second)
	attached := false
	for time.Now().Before(deadline) {
		daemon.attachRequestsMu.Lock()
		count := len(daemon.attachRequests)
		daemon.attachRequestsMu.Unlock()
		if count >= 2 {
			attached = true
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	if !attached {
		t.Fatal("AutoConnect did not complete in background within timeout")
	}
}

func TestEnvironmentsExecutableFieldPopulation(t *testing.T) {
	daemon := &fakeDaemonService{}
	resolver := &recordingResolver{
		results: map[string]string{
			"codex": "/home/luwei/.vite-plus/bin/codex",
		},
	}
	h, st := setupRestoreFixture(t, daemon, resolver)

	// 1. Saved connection with explicit hermes_path
	if _, err := st.SaveSSHConnection(context.Background(), map[string]any{
		"id":           "debian-box",
		"host":         "192.168.1.10",
		"display_name": "Debian Box",
		"user":         "luwei",
		"state":        "configured",
		"hermes_path":  "/opt/hermes/bin/hermes",
	}); err != nil {
		t.Fatal(err)
	}

	// First discover the environment so codex gets resolved
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/v1/environments/debian-box/discover", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("discover failed: %d %s", w.Code, w.Body.String())
	}

	var discoveredItem map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &discoveredItem); err != nil {
		t.Fatal(err)
	}

	agents := discoveredItem["agents"].([]any)
	for _, a := range agents {
		agent := a.(map[string]any)
		kind := agent["kind"].(string)
		switch kind {
		case "codex":
			// Probed executable
			if agent["executable"] != "/home/luwei/.vite-plus/bin/codex" {
				t.Errorf("expected codex executable /home/luwei/.vite-plus/bin/codex, got %v", agent["executable"])
			}
		case "hermes":
			// Explicit hermes_path
			if agent["executable"] != "/opt/hermes/bin/hermes" {
				t.Errorf("expected hermes executable /opt/hermes/bin/hermes, got %v", agent["executable"])
			}
		case "grok":
			// Unconfigured executable should be nil
			if agent["executable"] != nil {
				t.Errorf("expected grok executable nil, got %v", agent["executable"])
			}
		}
	}

	// Also verify listing preserves the executable
	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/environments", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("list environments failed: %d %s", w.Code, w.Body.String())
	}
	var listing struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &listing); err != nil {
		t.Fatal(err)
	}

	// Find local item and debian item
	for _, item := range listing.Items {
		itemAgents := item["agents"].([]any)
		if item["id"] == "local" {
			// Local items have "executable" key in each agent
			for _, a := range itemAgents {
				agent := a.(map[string]any)
				if _, ok := agent["executable"]; !ok {
					t.Errorf("local agent missing 'executable' key: %v", agent)
				}
			}
		}
		if item["id"] == "debian-box" {
			for _, a := range itemAgents {
				agent := a.(map[string]any)
				if agent["kind"] == "codex" && agent["executable"] != "/home/luwei/.vite-plus/bin/codex" {
					t.Errorf("listing codex missing executable: %v", agent)
				}
				if agent["kind"] == "hermes" && agent["executable"] != "/opt/hermes/bin/hermes" {
					t.Errorf("listing hermes missing executable: %v", agent)
				}
			}
		}
	}
}
