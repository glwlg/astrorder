package connections_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	pb "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
)

func TestEnvironmentSnapshotIncludesAgentStatesAndConnectionMethod(t *testing.T) {
	connID := "saved"
	owned := true
	sshHermes := pb.RuntimeKind_RUNTIME_KIND_SSH_HERMES
	server := &fakeDaemonService{}
	server.statusResponse.Store(&pb.GetStatusResponse{
		DaemonId: "daemon-test-123",
		Runtimes: []*pb.RuntimeStatus{{Runtime: pb.RuntimeKind_RUNTIME_KIND_HERMES, Registered: true, OwnedSessions: 1}, {Runtime: pb.RuntimeKind_RUNTIME_KIND_CODEX, Registered: true}, {Runtime: pb.RuntimeKind_RUNTIME_KIND_SSH_HERMES, Registered: true, OwnedSessions: 1}},
		// The remote Hermes row is confirmed connected only while the daemon
		// explicitly reports live ownership of this connection's own control
		// session, so the fixture carries the real owner verdict and identity.
		Sessions: []*pb.SessionSnapshot{{Session: &pb.SessionRef{SessionId: "probe-hermes-saved", ConnectionId: &connID}, Status: pb.SessionStatus_SESSION_STATUS_IDLE, RuntimeOwned: &owned, Runtime: &sshHermes}},
	})
	handler, st, _ := receiptFixture(t, server)
	_, err := st.UpsertAgent(context.Background(), map[string]any{"id": "local-hermes-default", "kind": "hermes", "name": "Local Hermes", "status": "ready"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = st.UpsertAgent(context.Background(), map[string]any{"id": "ssh-hermes-saved", "kind": "hermes", "name": "Remote Hermes", "status": "ready", "connection_id": "saved"})
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/environments", nil))
	var body struct {
		Items []struct {
			ID         string `json:"id"`
			Method     string `json:"method"`
			Discovered bool   `json:"discovered"`
			Agents     []struct {
				Kind      string `json:"kind"`
				State     string `json:"state"`
				Available bool   `json:"available"`
			} `json:"agents"`
		} `json:"items"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if w.Code != http.StatusOK || len(body.Items) != 2 || body.Items[0].Method != "local" || body.Items[1].Method != "ssh" || len(body.Items[0].Agents) != 3 || len(body.Items[1].Agents) != 3 {
		t.Fatalf("environment page cannot render snapshot: %d %s", w.Code, w.Body.String())
	}
	local := body.Items[0].Agents
	if local[0].Kind != "hermes" || local[0].State != "connected" || !local[0].Available || local[1].State != "disconnected" || local[2].Available {
		t.Fatal("local environment uses fabricated availability or connection state", local)
	}
	remote := body.Items[1].Agents
	if remote[0].Kind != "hermes" || remote[0].State != "connected" || !remote[0].Available || remote[1].Available {
		t.Fatal("remote kinds share one connected state", remote)
	}
}
