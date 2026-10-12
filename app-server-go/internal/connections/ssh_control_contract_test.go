package connections_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	pb "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
)

func TestSSHConnectionControlPreservesSavedHermesTarget(t *testing.T) {
	server := &fakeDaemonService{}
	handler, st, _ := receiptFixture(t, server)
	_, err := st.SaveSSHConnection(context.Background(), map[string]any{"id": "saved", "host": "host.invalid", "port": 2222, "profile_name": "dev", "display_name": "Private host", "hermes_path": "/opt/hermes", "workspace": "/srv/work"})
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/api/v1/connections/ssh/saved/connect", "/api/v1/connections/ssh/connect", "/api/v1/environments/saved/agents/hermes/connect"} {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest(http.MethodPost, path, nil))
		req := server.attachRequest.Load()
		if w.Code != http.StatusOK || req == nil {
			t.Fatalf("attach failed: %s status=%d body=%s", path, w.Code, w.Body.String())
		}
		target := req.Target
		if target.GetRuntime() != pb.RuntimeKind_RUNTIME_KIND_SSH_HERMES || target.GetConnectionId() != "saved" || target.Ssh.GetPort() != 2222 || target.Ssh.GetProfileName() != "dev" || target.Ssh.GetDisplayName() != "Private host" || target.Ssh.GetHermesPath() != "/opt/hermes" || target.Ssh.GetWorkspace() != "/srv/work" {
			t.Fatalf("saved Hermes target changed at gRPC: %s %v", path, target)
		}
		if req.Session.GetSessionId() != "probe-hermes-saved" || !req.RuntimeControl {
			t.Fatalf("connect created a new control identity: %s %v", path, req)
		}
	}
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/v1/connections/ssh/saved/disconnect", nil))
	if w.Code != http.StatusOK || server.disconnectRequest.Load().Target.GetRuntime() != pb.RuntimeKind_RUNTIME_KIND_SSH_HERMES {
		t.Fatalf("SSH disconnect targeted another runtime: %d %v", w.Code, server.disconnectRequest.Load())
	}
	before := server.attachHit.Load()
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/v1/environments/missing/agents/hermes/connect", nil))
	if w.Code != http.StatusNotFound || server.attachHit.Load() != before {
		t.Fatal("unknown SSH environment dispatched native work", w.Code)
	}
}

func TestConnectionsSnapshotUsesFrontendContract(t *testing.T) {
	server := &fakeDaemonService{}
	handler, _, _ := receiptFixture(t, server)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/connections", nil))
	var snapshot map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &snapshot); err != nil {
		t.Fatal(err)
	}
	local, _ := snapshot["local"].(map[string]any)
	ssh, _ := snapshot["ssh"].(map[string]any)
	items, _ := ssh["items"].([]any)
	if w.Code != http.StatusOK || local["kind"] != "hermes" || local["state"] != "offline" || local["available"] != false || len(items) != 1 || ssh["state"] != "connected" || ssh["settings"] == nil {
		t.Fatalf("incompatible or fabricated connections snapshot: %v", snapshot)
	}
	for _, key := range []string{"version", "agent_id", "session_id", "detail"} {
		if _, exists := local[key]; !exists {
			t.Fatal("local connection omitted contract field", key)
		}
	}
}
