package connections_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	pb "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
)

func listedRemoteEnvironment(t *testing.T, handler http.Handler) map[string]any {
	t.Helper()
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/environments", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("list: HTTP=%d body=%s", w.Code, w.Body.String())
	}
	body := decodeEnvironmentItem(t, w.Body.Bytes())
	for _, raw := range body["items"].([]any) {
		item := raw.(map[string]any)
		if item["id"] == "saved" {
			return item
		}
	}
	t.Fatal("saved environment missing")
	return nil
}

func TestAcceptanceFailedRediscoveryDoesNotRetainSuccessfulAvailability(t *testing.T) {
	recorder := &probeRecorder{}
	osProber := &fakeOSProber{result: "Linux"}
	handler, st := environmentsFixture(t, &fakeDaemonService{}, recorder, osProber)
	if _, err := st.UpdateSSHConnectionState(context.Background(), "saved", "configured", "not attached", nil); err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/v1/environments/saved/discover", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("initial discover: %d %s", w.Code, w.Body.String())
	}
	recorder.mu.Lock()
	recorder.failures = map[string]error{"hermes|saved": errors.New("unreachable"), "codex|saved": errors.New("unreachable"), "grok|saved": errors.New("unreachable")}
	recorder.mu.Unlock()
	osProber.mu.Lock()
	osProber.err = errors.New("unreachable")
	osProber.mu.Unlock()
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/v1/environments/saved/discover", nil))
	if w.Code != http.StatusBadGateway {
		t.Fatalf("failed rediscovery: %d %s", w.Code, w.Body.String())
	}
	item := listedRemoteEnvironment(t, handler)
	if item["discovered"] != false || item["os"] != nil {
		t.Fatalf("failed rediscovery retained successful discovery: %#v", item)
	}
	for _, agent := range environmentAgents(t, item) {
		if agent["available"] == true {
			t.Fatalf("failed rediscovery retained stale availability: %#v", agent)
		}
	}
}

func TestAcceptanceConnectedRemoteCodexUsesItsOwnDaemonControl(t *testing.T) {
	server := &fakeDaemonService{}
	connID := "saved"
	owned := true
	codexSSH := pb.RuntimeKind_RUNTIME_KIND_CODEX_SSH
	server.statusResponse.Store(&pb.GetStatusResponse{
		DaemonId: "daemon-test-123",
		Runtimes: []*pb.RuntimeStatus{{Runtime: pb.RuntimeKind_RUNTIME_KIND_CODEX_SSH, Registered: true, OwnedSessions: 1}},
		Sessions: []*pb.SessionSnapshot{{Session: &pb.SessionRef{SessionId: "probe-codex-saved", ConnectionId: &connID}, Status: pb.SessionStatus_SESSION_STATUS_IDLE, RuntimeOwned: &owned, Runtime: &codexSSH}},
	})
	handler, st := environmentsFixture(t, server, &probeRecorder{}, &fakeOSProber{result: "Linux"})
	if _, err := st.UpsertAgent(context.Background(), map[string]any{"id": "ssh-codex-saved", "kind": "codex", "status": "ready", "connection_id": "saved", "name": "Remote Codex"}); err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/v1/environments/saved/agents/codex/connect", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("connect: %d %s", w.Code, w.Body.String())
	}
	item := listedRemoteEnvironment(t, handler)
	found := false
	for _, agent := range environmentAgents(t, item) {
		if agent["kind"] == "codex" {
			found = true
			if agent["state"] != "connected" || agent["agent_id"] != "ssh-codex-saved" {
				t.Fatalf("confirmed Codex runtime lost its environment connection state: %#v", agent)
			}
		}
		if agent["kind"] == "grok" && agent["state"] == "connected" {
			t.Fatalf("Grok inherited Codex connection state: %#v", agent)
		}
	}
	if !found {
		t.Fatal("Codex missing")
	}
}

// A failed rediscovery of a still-connected environment clears stale probe
// results but must not demote the confirmed connection state itself. The
// confirmed state is a real daemon-owned Hermes session for this connection.
func TestAcceptanceFailedRediscoveryKeepsConfirmedConnectedState(t *testing.T) {
	recorder := &probeRecorder{}
	osProber := &fakeOSProber{result: "Linux"}
	connID := "saved"
	owned := true
	sshHermes := pb.RuntimeKind_RUNTIME_KIND_SSH_HERMES
	server := &fakeDaemonService{}
	server.statusResponse.Store(&pb.GetStatusResponse{
		DaemonId: "daemon-test-123",
		Runtimes: []*pb.RuntimeStatus{{Runtime: pb.RuntimeKind_RUNTIME_KIND_SSH_HERMES, Registered: true, OwnedSessions: 1}},
		Sessions: []*pb.SessionSnapshot{{Session: &pb.SessionRef{SessionId: "probe-hermes-saved", ConnectionId: &connID}, Status: pb.SessionStatus_SESSION_STATUS_IDLE, RuntimeOwned: &owned, Runtime: &sshHermes}},
	})
	handler, _ := environmentsFixture(t, server, recorder, osProber)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/v1/environments/saved/discover", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("initial discover: %d %s", w.Code, w.Body.String())
	}
	recorder.mu.Lock()
	recorder.failures = map[string]error{"hermes|saved": errors.New("unreachable"), "codex|saved": errors.New("unreachable"), "grok|saved": errors.New("unreachable")}
	recorder.mu.Unlock()
	osProber.mu.Lock()
	osProber.err = errors.New("unreachable")
	osProber.mu.Unlock()
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/v1/environments/saved/discover", nil))
	if w.Code != http.StatusBadGateway {
		t.Fatalf("failed rediscovery: %d %s", w.Code, w.Body.String())
	}
	item := listedRemoteEnvironment(t, handler)
	if item["discovered"] != false || item["os"] != nil {
		t.Fatalf("failed rediscovery retained successful discovery: %#v", item)
	}
	for _, agent := range environmentAgents(t, item) {
		switch agent["kind"] {
		case "hermes":
			if agent["state"] != "connected" || agent["available"] != true {
				t.Fatalf("confirmed Hermes connection was demoted by a failed probe: %#v", agent)
			}
		default:
			if agent["state"] == "connected" || agent["available"] == true {
				t.Fatalf("failed probe fabricated availability for %s: %#v", agent["kind"], agent)
			}
		}
	}
}
func TestAcceptanceUncertainAttachDoesNotWriteChoiceOrConnectedState(t *testing.T) {
	connID := "saved"
	server := &fakeDaemonService{attachResponse: &pb.SessionReceipt{
		DaemonId: "daemon-test-123",
		Session:  &pb.SessionRef{SessionId: "probe-hermes-saved", ConnectionId: &connID},
		Attached: true,
	}}
	handler, st, publisher := receiptFixture(t, server)
	if _, err := st.UpdateSSHConnectionState(context.Background(), "saved", "disconnected", "not attached", nil); err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/v1/environments/saved/agents/hermes/connect", nil))
	if w.Code != http.StatusBadGateway {
		t.Fatalf("uncertain attach reported success: HTTP=%d body=%s", w.Code, w.Body.String())
	}
	if server.attachHit.Load() != 1 {
		t.Fatalf("unconfirmed attach was retried: attachHit=%d", server.attachHit.Load())
	}
	if _, present, err := st.GetAgentConnectionChoice(context.Background(), "saved:hermes"); err != nil || present {
		t.Fatalf("uncertain attach persisted a choice: present=%v err=%v", present, err)
	}
	stored, err := st.GetSSHConnection(context.Background(), "saved")
	if err != nil || stored == nil || stored["state"] != "disconnected" {
		t.Fatalf("uncertain attach wrote a success state: %v err=%v", stored, err)
	}
	for _, event := range publisher.events {
		if event == "ssh_connection.connected" {
			t.Fatalf("uncertain attach published success: %v", publisher.events)
		}
	}
}

// A restart whose confirmed disconnect is followed by an uncertain attach must
// keep the confirmed truth: not connected, no success choice, no re-attach.
func TestAcceptanceRestartUncertainAttachKeepsConfirmedDisconnect(t *testing.T) {
	connID := "saved"
	server := &fakeDaemonService{attachResponse: &pb.SessionReceipt{
		DaemonId: "daemon-test-123",
		Session:  &pb.SessionRef{SessionId: "probe-hermes-saved", ConnectionId: &connID},
		Attached: true,
	}}
	handler, st, publisher := receiptFixture(t, server)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/v1/environments/saved/agents/hermes/restart", nil))
	if w.Code != http.StatusBadGateway {
		t.Fatalf("uncertain restart reported success: HTTP=%d body=%s", w.Code, w.Body.String())
	}
	if server.disconnectHit.Load() != 1 || server.attachHit.Load() != 1 {
		t.Fatalf("restart control flow changed: disconnect=%d attach=%d", server.disconnectHit.Load(), server.attachHit.Load())
	}
	stored, err := st.GetSSHConnection(context.Background(), "saved")
	if err != nil || stored == nil || stored["state"] != "connecting" {
		t.Fatalf("restart kept a stale connected state after a confirmed disconnect: %v err=%v", stored, err)
	}
	if _, present, err := st.GetAgentConnectionChoice(context.Background(), "saved:hermes"); err != nil || present {
		t.Fatalf("uncertain restart persisted a choice: present=%v err=%v", present, err)
	}
	for _, event := range publisher.events {
		if event == "ssh_connection.connected" {
			t.Fatalf("uncertain restart published success: %v", publisher.events)
		}
	}
}

// A released runtime may still be listed by the durable journal. Its session
// stream alone must not keep the environment connected once the daemon no
// longer owns a live runtime for the kind: the live-ownership verdict drops
// to false even though the durable row survives.
func TestAcceptanceReleasedRemoteCodexIsNotReportedConnected(t *testing.T) {
	connID := "saved"
	owned := true
	notOwned := false
	codexSSH := pb.RuntimeKind_RUNTIME_KIND_CODEX_SSH
	server := &fakeDaemonService{}
	server.statusResponse.Store(&pb.GetStatusResponse{
		DaemonId: "daemon-test-123",
		Runtimes: []*pb.RuntimeStatus{{Runtime: pb.RuntimeKind_RUNTIME_KIND_CODEX_SSH, Registered: true, OwnedSessions: 1}},
		Sessions: []*pb.SessionSnapshot{{Session: &pb.SessionRef{SessionId: "probe-codex-saved", ConnectionId: &connID}, Status: pb.SessionStatus_SESSION_STATUS_IDLE, RuntimeOwned: &owned, Runtime: &codexSSH}},
	})
	handler, st, _ := receiptFixture(t, server)
	if _, err := st.UpsertAgent(context.Background(), map[string]any{"id": "ssh-codex-saved", "kind": "codex", "status": "ready", "connection_id": "saved", "name": "Remote Codex"}); err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/v1/environments/saved/agents/codex/connect", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("connect: %d %s", w.Code, w.Body.String())
	}
	codexState := func() map[string]any {
		t.Helper()
		for _, agent := range environmentAgents(t, listedRemoteEnvironment(t, handler)) {
			if agent["kind"] == "codex" {
				return agent
			}
		}
		t.Fatal("codex missing")
		return nil
	}
	if agent := codexState(); agent["state"] != "connected" || agent["agent_id"] != "ssh-codex-saved" {
		t.Fatalf("confirmed Codex attach not projected: %#v", agent)
	}
	server.statusResponse.Store(&pb.GetStatusResponse{
		DaemonId: "daemon-test-123",
		Runtimes: []*pb.RuntimeStatus{{Runtime: pb.RuntimeKind_RUNTIME_KIND_CODEX_SSH, Registered: true, OwnedSessions: 0}},
		Sessions: []*pb.SessionSnapshot{{Session: &pb.SessionRef{SessionId: "probe-codex-saved", ConnectionId: &connID}, Status: pb.SessionStatus_SESSION_STATUS_IDLE, RuntimeOwned: &notOwned}},
	})
	if agent := codexState(); agent["state"] != "disconnected" {
		t.Fatalf("released journal session kept Codex connected: %#v", agent)
	}
}

// A registered adapter plus a stored ready projection and a previously
// confirmed choice is still not an attachment: without live daemon ownership
// and this connection's control session the kind stays disconnected.
func TestAcceptanceRegisteredAdapterWithoutOwnershipIsNotConnected(t *testing.T) {
	server := &fakeDaemonService{}
	server.statusResponse.Store(&pb.GetStatusResponse{
		DaemonId: "daemon-test-123",
		Runtimes: []*pb.RuntimeStatus{{Runtime: pb.RuntimeKind_RUNTIME_KIND_CODEX_SSH, Registered: true}},
	})
	handler, st, _ := receiptFixture(t, server)
	if _, err := st.UpsertAgent(context.Background(), map[string]any{"id": "ssh-codex-saved", "kind": "codex", "status": "ready", "connection_id": "saved", "name": "Remote Codex"}); err != nil {
		t.Fatal(err)
	}
	if err := st.SetAgentConnectionChoice(context.Background(), "saved:codex", true); err != nil {
		t.Fatal(err)
	}
	for _, agent := range environmentAgents(t, listedRemoteEnvironment(t, handler)) {
		if agent["kind"] == "codex" && agent["state"] == "connected" {
			t.Fatalf("registered adapter without live ownership reported connected: %#v", agent)
		}
	}
}

// A confirmed disconnect writes the app-side choice, and that confirmed
// disablement must gate stale daemon ownership even if the daemon status
// still explicitly reports live ownership.
func TestAcceptanceConfirmedDisconnectGatesStaleDaemonOwnership(t *testing.T) {
	connID := "saved"
	owned := true
	codexSSH := pb.RuntimeKind_RUNTIME_KIND_CODEX_SSH
	server := &fakeDaemonService{}
	server.statusResponse.Store(&pb.GetStatusResponse{
		DaemonId: "daemon-test-123",
		Runtimes: []*pb.RuntimeStatus{{Runtime: pb.RuntimeKind_RUNTIME_KIND_CODEX_SSH, Registered: true, OwnedSessions: 1}},
		Sessions: []*pb.SessionSnapshot{{Session: &pb.SessionRef{SessionId: "probe-codex-saved", ConnectionId: &connID}, Status: pb.SessionStatus_SESSION_STATUS_IDLE, RuntimeOwned: &owned, Runtime: &codexSSH}},
	})
	handler, st, _ := receiptFixture(t, server)
	if _, err := st.UpsertAgent(context.Background(), map[string]any{"id": "ssh-codex-saved", "kind": "codex", "status": "ready", "connection_id": "saved", "name": "Remote Codex"}); err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/v1/environments/saved/agents/codex/connect", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("connect: %d %s", w.Code, w.Body.String())
	}
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/v1/environments/saved/agents/codex/disconnect", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("disconnect: %d %s", w.Code, w.Body.String())
	}
	for _, agent := range environmentAgents(t, listedRemoteEnvironment(t, handler)) {
		if agent["kind"] == "codex" && agent["state"] == "connected" {
			t.Fatalf("stale daemon ownership overrode a confirmed disconnect: %#v", agent)
		}
	}
}

// A remote Hermes whose own control session reports an error must surface the
// error instead of keeping a stale connected claim from its stored row.
func TestAcceptanceRemoteHermesControlErrorIsExplicit(t *testing.T) {
	connID := "saved"
	server := &fakeDaemonService{}
	server.statusResponse.Store(&pb.GetStatusResponse{
		DaemonId: "daemon-test-123",
		Sessions: []*pb.SessionSnapshot{{Session: &pb.SessionRef{SessionId: "probe-hermes-saved", ConnectionId: &connID}, Status: pb.SessionStatus_SESSION_STATUS_ERROR}},
	})
	handler, _, _ := receiptFixture(t, server)
	for _, agent := range environmentAgents(t, listedRemoteEnvironment(t, handler)) {
		if agent["kind"] == "hermes" {
			if agent["state"] != "error" {
				t.Fatalf("remote Hermes control error was masked: %#v", agent)
			}
			return
		}
	}
	t.Fatal("hermes missing")
}
