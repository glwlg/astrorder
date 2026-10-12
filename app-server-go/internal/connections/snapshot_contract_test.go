package connections_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	pb "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
)

func TestLocalConnectionActionsReturnSnapshotContract(t *testing.T) {
	server := &fakeDaemonService{}
	handler, _, _ := receiptFixture(t, server)
	for _, action := range []string{"connect", "disconnect"} {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/v1/connections/local/"+action, nil))
		var body map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		local, _ := body["local"].(map[string]any)
		ssh, _ := body["ssh"].(map[string]any)
		if w.Code != http.StatusOK || local["kind"] != "hermes" || ssh["items"] == nil {
			t.Fatalf("local action lost connection contract: %s %d %v", action, w.Code, body)
		}
	}
}

func TestCodexSnapshotDoesNotClaimRegisteredAdapterIsConnected(t *testing.T) {
	server := &fakeDaemonService{}
	handler, _, _ := receiptFixture(t, server)
	for _, route := range []struct{ method, path string }{
		{http.MethodGet, "/api/v1/connections/codex"},
		{http.MethodPost, "/api/v1/connections/codex/connect"},
		{http.MethodPost, "/api/v1/connections/codex/disconnect"},
	} {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest(route.method, route.path, nil))
		var body map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if w.Code != http.StatusOK || body["kind"] != "codex" || body["state"] != "disconnected" || body["available"] != true || body["session_count"] != float64(0) || body["auth_required"] != false {
			t.Fatalf("Codex action lost authoritative snapshot: %s %d %v", route.path, w.Code, body)
		}
	}
}

func TestLocalSnapshotRetainsNativeErrorInsteadOfReadyProjection(t *testing.T) {
	server := &fakeDaemonService{}
	server.statusResponse.Store(&pb.GetStatusResponse{DaemonId: "daemon-test-123", Runtimes: []*pb.RuntimeStatus{{Runtime: pb.RuntimeKind_RUNTIME_KIND_HERMES, Registered: true, OwnedSessions: 1}}, Sessions: []*pb.SessionSnapshot{{Session: &pb.SessionRef{SessionId: "probe-hermes-local"}, Status: pb.SessionStatus_SESSION_STATUS_ERROR}}})
	handler, st, _ := receiptFixture(t, server)
	_, err := st.UpsertAgent(context.Background(), map[string]any{"id": "local-hermes-default", "kind": "hermes", "name": "Local Hermes", "status": "ready", "profile_name": "default"})
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/connections", nil))
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	local := body["local"].(map[string]any)
	if local["state"] != "error" {
		t.Fatal("native error masked by stale ready projection", body)
	}
}
