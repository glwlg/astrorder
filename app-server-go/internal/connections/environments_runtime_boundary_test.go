package connections_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	pb "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
)

func TestAcceptanceAnotherConnectionOwnershipCannotReviveReleasedControl(t *testing.T) {
	connID := "saved"
	otherConn := "other"
	owned := true
	notOwned := false
	codexSSH := pb.RuntimeKind_RUNTIME_KIND_CODEX_SSH
	server := &fakeDaemonService{}
	server.statusResponse.Store(&pb.GetStatusResponse{
		DaemonId: "daemon-test-123",
		Runtimes: []*pb.RuntimeStatus{{Runtime: pb.RuntimeKind_RUNTIME_KIND_CODEX_SSH, Registered: true, OwnedSessions: 1}},
		// The saved control was released: its durable journal row explicitly
		// reports runtime_owned=false. Only the other connection's session is
		// live-owned, so the global count of 1 must not revive the saved
		// control.
		Sessions: []*pb.SessionSnapshot{
			{Session: &pb.SessionRef{SessionId: "probe-codex-saved", ConnectionId: &connID}, Status: pb.SessionStatus_SESSION_STATUS_IDLE, RuntimeOwned: &notOwned},
			{Session: &pb.SessionRef{SessionId: "probe-codex-other", ConnectionId: &otherConn}, Status: pb.SessionStatus_SESSION_STATUS_IDLE, RuntimeOwned: &owned, Runtime: &codexSSH},
		},
	})
	handler, st, _ := receiptFixture(t, server)
	if _, err := st.UpsertAgent(context.Background(), map[string]any{"id": "ssh-codex-saved", "kind": "codex", "status": "ready", "connection_id": "saved", "name": "Saved Codex"}); err != nil {
		t.Fatal(err)
	}
	if err := st.SetAgentConnectionChoice(context.Background(), "saved:codex", true); err != nil {
		t.Fatal(err)
	}
	for _, agent := range environmentAgents(t, listedRemoteEnvironment(t, handler)) {
		if agent["kind"] == "codex" && agent["state"] == "connected" {
			t.Fatalf("another connection revived a released journal control: %#v", agent)
		}
	}
}

// The live-ownership verdict is exact per control session: a missing verdict
// (old daemon), an explicit false, a mismatched runtime kind, or a mismatched
// connection identity must never be upgraded to connected by the global
// ownership count.
func TestAcceptanceMissingOrMismatchedLiveOwnershipIsNotConnected(t *testing.T) {
	connID := "saved"
	otherConn := "other"
	owned := true
	notOwned := false
	codexSSH := pb.RuntimeKind_RUNTIME_KIND_CODEX_SSH
	grokSSH := pb.RuntimeKind_RUNTIME_KIND_GROK_SSH
	cases := []struct {
		name    string
		session *pb.SessionSnapshot
	}{
		{name: "missing verdict", session: &pb.SessionSnapshot{Session: &pb.SessionRef{SessionId: "probe-codex-saved", ConnectionId: &connID}, Status: pb.SessionStatus_SESSION_STATUS_IDLE}},
		{name: "explicit false", session: &pb.SessionSnapshot{Session: &pb.SessionRef{SessionId: "probe-codex-saved", ConnectionId: &connID}, Status: pb.SessionStatus_SESSION_STATUS_IDLE, RuntimeOwned: &notOwned}},
		{name: "mismatched runtime kind", session: &pb.SessionSnapshot{Session: &pb.SessionRef{SessionId: "probe-codex-saved", ConnectionId: &connID}, Status: pb.SessionStatus_SESSION_STATUS_IDLE, RuntimeOwned: &owned, Runtime: &grokSSH}},
		{name: "mismatched connection identity", session: &pb.SessionSnapshot{Session: &pb.SessionRef{SessionId: "probe-codex-saved", ConnectionId: &otherConn}, Status: pb.SessionStatus_SESSION_STATUS_IDLE, RuntimeOwned: &owned, Runtime: &codexSSH}},
		{name: "missing connection identity", session: &pb.SessionSnapshot{Session: &pb.SessionRef{SessionId: "probe-codex-saved"}, Status: pb.SessionStatus_SESSION_STATUS_IDLE, RuntimeOwned: &owned, Runtime: &codexSSH}},
		{name: "unknown status enum", session: &pb.SessionSnapshot{Session: &pb.SessionRef{SessionId: "probe-codex-saved", ConnectionId: &connID}, Status: pb.SessionStatus(999), RuntimeOwned: &owned, Runtime: &codexSSH}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := &fakeDaemonService{}
			server.statusResponse.Store(&pb.GetStatusResponse{
				DaemonId: "daemon-test-123",
				Runtimes: []*pb.RuntimeStatus{{Runtime: pb.RuntimeKind_RUNTIME_KIND_CODEX_SSH, Registered: true, OwnedSessions: 1}},
				Sessions: []*pb.SessionSnapshot{tc.session},
			})
			handler, st, _ := receiptFixture(t, server)
			if _, err := st.UpsertAgent(context.Background(), map[string]any{"id": "ssh-codex-saved", "kind": "codex", "status": "ready", "connection_id": "saved", "name": "Saved Codex"}); err != nil {
				t.Fatal(err)
			}
			if err := st.SetAgentConnectionChoice(context.Background(), "saved:codex", true); err != nil {
				t.Fatal(err)
			}
			for _, agent := range environmentAgents(t, listedRemoteEnvironment(t, handler)) {
				if agent["kind"] == "codex" && agent["state"] == "connected" {
					t.Fatalf("unconfirmed ownership reported connected: %#v", agent)
				}
			}
		})
	}
}

// A GetStatus answered by a foreign daemon epoch is unconfirmable: even a
// fully-owned live verdict inside that response must not confirm a stored
// connection for this epoch's client.
func TestAcceptanceForeignDaemonEpochCannotConfirmConnection(t *testing.T) {
	connID := "saved"
	owned := true
	sshHermes := pb.RuntimeKind_RUNTIME_KIND_SSH_HERMES
	server := &fakeDaemonService{}
	server.statusResponse.Store(&pb.GetStatusResponse{
		DaemonId: "other-epoch",
		Runtimes: []*pb.RuntimeStatus{{Runtime: pb.RuntimeKind_RUNTIME_KIND_SSH_HERMES, Registered: true, OwnedSessions: 1}},
		Sessions: []*pb.SessionSnapshot{{Session: &pb.SessionRef{SessionId: "probe-hermes-saved", ConnectionId: &connID}, Status: pb.SessionStatus_SESSION_STATUS_IDLE, RuntimeOwned: &owned, Runtime: &sshHermes}},
	})
	handler, _, _ := receiptFixture(t, server)
	for _, agent := range environmentAgents(t, listedRemoteEnvironment(t, handler)) {
		if agent["kind"] == "hermes" && agent["state"] == "connected" {
			t.Fatalf("foreign daemon epoch confirmed a connection: %#v", agent)
		}
	}
}

// The positive control for the exact-ownership contract: the same code path
// reports connected only when runtime_owned=true carries the expected typed
// runtime and this connection's identity.
func TestAcceptanceExactLiveOwnershipReportsConnected(t *testing.T) {
	connID := "saved"
	owned := true
	codexSSH := pb.RuntimeKind_RUNTIME_KIND_CODEX_SSH
	server := &fakeDaemonService{}
	server.statusResponse.Store(&pb.GetStatusResponse{
		DaemonId: "daemon-test-123",
		Runtimes: []*pb.RuntimeStatus{{Runtime: pb.RuntimeKind_RUNTIME_KIND_CODEX_SSH, Registered: true, OwnedSessions: 1}},
		Sessions: []*pb.SessionSnapshot{
			{Session: &pb.SessionRef{SessionId: "probe-codex-saved", ConnectionId: &connID, AgentId: func() *string { s := "ssh-codex-saved"; return &s }()}, Status: pb.SessionStatus_SESSION_STATUS_IDLE, RuntimeOwned: &owned, Runtime: &codexSSH},
		},
	})
	handler, st, _ := receiptFixture(t, server)
	if _, err := st.UpsertAgent(context.Background(), map[string]any{"id": "ssh-codex-saved", "kind": "codex", "status": "ready", "connection_id": "saved", "name": "Saved Codex"}); err != nil {
		t.Fatal(err)
	}
	if err := st.SetAgentConnectionChoice(context.Background(), "saved:codex", true); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, agent := range environmentAgents(t, listedRemoteEnvironment(t, handler)) {
		if agent["kind"] == "codex" {
			found = true
			if agent["state"] != "connected" || agent["available"] != true {
				t.Fatalf("exact live ownership was not reported connected: %#v", agent)
			}
		}
	}
	if !found {
		t.Fatal("codex agent missing")
	}
}

func TestAcceptanceErrorAttachCannotWriteConnectedOrEnabledChoice(t *testing.T) {
	connID := "saved"
	server := &fakeDaemonService{attachResponse: &pb.SessionReceipt{
		DaemonId: "daemon-test-123",
		Session:  &pb.SessionRef{SessionId: "probe-hermes-saved", ConnectionId: &connID},
		Status:   pb.SessionStatus_SESSION_STATUS_ERROR,
		Attached: true,
	}}
	handler, st, publisher := receiptFixture(t, server)
	if _, err := st.UpdateSSHConnectionState(context.Background(), "saved", "disconnected", "not attached", nil); err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/v1/environments/saved/agents/hermes/connect", nil))
	if w.Code != http.StatusBadGateway {
		t.Errorf("error attach was reported successful: HTTP=%d body=%s", w.Code, w.Body.String())
	}
	if _, present, err := st.GetAgentConnectionChoice(context.Background(), "saved:hermes"); err != nil || present {
		t.Errorf("error attach persisted an enabled choice: present=%v err=%v", present, err)
	}
	row, err := st.GetSSHConnection(context.Background(), "saved")
	if err != nil || row == nil || row["state"] == "connected" {
		t.Errorf("error attach wrote connected: row=%v err=%v", row, err)
	}
	for _, event := range publisher.events {
		if event == "ssh_connection.connected" {
			t.Errorf("error attach published connected: %v", publisher.events)
		}
	}
	if server.attachHit.Load() != 1 {
		t.Errorf("error attach was resent: %d", server.attachHit.Load())
	}
}
