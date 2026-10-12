package connections_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"astrorder.dev/app-server/internal/connections"
	"astrorder.dev/app-server/internal/daemonclient"
	"astrorder.dev/app-server/internal/store"
	pb "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func receiptFixture(t *testing.T, server *fakeDaemonService) (http.Handler, *store.Store, *dummyPublisher) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "receipts.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	_, err = st.SaveSSHConnection(context.Background(), map[string]any{"id": "saved", "host": "private-fixture.invalid", "state": "connected"})
	if err != nil {
		t.Fatal(err)
	}
	server.token = "private-fixture-credential"
	address, closeServer := startFakeDaemon(t, server)
	t.Cleanup(closeServer)
	client, err := daemonclient.Dial(context.Background(), daemonclient.Config{Target: address, Credential: server.token, RequestTimeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	publisher := &dummyPublisher{}
	handler, err := connections.NewHandler(connections.Options{Store: st, Client: client, Publisher: publisher,
		RemoteExecutable: &recordingExecutableResolver{results: map[string]string{"codex": "/opt/fixture/codex", "grok": "/opt/fixture/grok"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return handler, st, publisher
}

func TestUnconfirmedDisconnectCannotReconnectOrChangeStoredState(t *testing.T) {
	for _, outcome := range []struct {
		name     string
		response *pb.DisconnectRuntimeResponse
		err      error
	}{
		{name: "transport refusal", err: status.Error(codes.FailedPrecondition, "active session")},
		{name: "empty receipt", response: &pb.DisconnectRuntimeResponse{}},
		{name: "not disconnected", response: &pb.DisconnectRuntimeResponse{DaemonId: "daemon-test-123", OperationState: pb.OperationState_OPERATION_STATE_COMPLETED}},
		{name: "pending", response: &pb.DisconnectRuntimeResponse{DaemonId: "daemon-test-123", Disconnected: true, OperationState: pb.OperationState_OPERATION_STATE_PENDING}},
		{name: "uncertain", response: &pb.DisconnectRuntimeResponse{DaemonId: "daemon-test-123", Disconnected: true, OperationState: pb.OperationState_OPERATION_STATE_UNCERTAIN}},
		{name: "wrong daemon", response: &pb.DisconnectRuntimeResponse{DaemonId: "other-epoch", Disconnected: true, OperationState: pb.OperationState_OPERATION_STATE_COMPLETED}},
		{name: "business refusal", response: &pb.DisconnectRuntimeResponse{DaemonId: "daemon-test-123", Disconnected: true, OperationState: pb.OperationState_OPERATION_STATE_COMPLETED, Error: &pb.BusinessError{Message: "native disconnect refused"}}},
	} {
		t.Run(outcome.name, func(t *testing.T) {
			for _, route := range []struct{ method, path string }{
				{http.MethodPost, "/api/v1/environments/local/agents/hermes/restart"},
				{http.MethodPost, "/api/v1/environments/local/agents/hermes/disconnect"},
				{http.MethodPost, "/api/v1/connections/local/disconnect"},
				{http.MethodPost, "/api/v1/connections/codex/disconnect"},
				{http.MethodPost, "/api/v1/connections/ssh/saved/disconnect"},
				{http.MethodDelete, "/api/v1/connections/ssh/saved"},
			} {
				t.Run(route.path, func(t *testing.T) {
					server := &fakeDaemonService{disconnectResponse: outcome.response, disconnectError: outcome.err}
					handler, st, publisher := receiptFixture(t, server)
					w := httptest.NewRecorder()
					handler.ServeHTTP(w, httptest.NewRequest(route.method, route.path, nil))
					if w.Code != http.StatusConflict || server.attachHit.Load() != 0 || server.disconnectHit.Load() != 1 {
						t.Errorf("refused disconnect changed control flow: HTTP=%d disconnect=%d attach=%d body=%s", w.Code, server.disconnectHit.Load(), server.attachHit.Load(), w.Body.String())
					}
					stored, err := st.GetSSHConnection(context.Background(), "saved")
					if err != nil || stored == nil || stored["state"] != "connected" || len(publisher.events) != 0 {
						t.Errorf("unconfirmed disconnect changed persisted state or published success: row=%v events=%v error=%v", stored, publisher.events, err)
					}
				})
			}
		})
	}
}

func TestConfirmedRestartDisconnectsOnceThenAttaches(t *testing.T) {
	server := &fakeDaemonService{}
	handler, _, _ := receiptFixture(t, server)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/v1/environments/local/agents/hermes/restart", nil))
	if w.Code != http.StatusOK || server.disconnectHit.Load() != 1 || server.attachHit.Load() != 1 {
		t.Fatalf("confirmed restart: HTTP=%d disconnect=%d attach=%d body=%s", w.Code, server.disconnectHit.Load(), server.attachHit.Load(), w.Body.String())
	}
}

// Attach receipts must satisfy the same identity/epoch discipline as
// disconnects; only a fully confirmed outcome may mutate persisted state.
func TestAttachReceiptRequiresEpochIdentityAndConfirmedStatus(t *testing.T) {
	connID := "saved"
	expected := &pb.SessionRef{SessionId: "probe-hermes-saved", ConnectionId: &connID}
	cases := []struct {
		name      string
		receipt   *pb.SessionReceipt
		attachNil bool
		attachErr error
		wantOK    bool
	}{
		{name: "confirmed", receipt: &pb.SessionReceipt{DaemonId: "daemon-test-123", Session: expected, Status: pb.SessionStatus_SESSION_STATUS_IDLE, Attached: true}, wantOK: true},
		{name: "nil receipt", attachNil: true},
		{name: "not attached", receipt: &pb.SessionReceipt{DaemonId: "daemon-test-123", Session: expected, Status: pb.SessionStatus_SESSION_STATUS_IDLE}},
		{name: "wrong daemon epoch", receipt: &pb.SessionReceipt{DaemonId: "other-epoch", Session: expected, Status: pb.SessionStatus_SESSION_STATUS_IDLE, Attached: true}},
		{name: "wrong session identity", receipt: &pb.SessionReceipt{DaemonId: "daemon-test-123", Session: &pb.SessionRef{SessionId: "probe-codex-saved", ConnectionId: &connID}, Status: pb.SessionStatus_SESSION_STATUS_IDLE, Attached: true}},
		{name: "wrong connection identity", receipt: &pb.SessionReceipt{DaemonId: "daemon-test-123", Session: &pb.SessionRef{SessionId: "probe-hermes-saved", ConnectionId: func() *string { s := "other"; return &s }()}, Status: pb.SessionStatus_SESSION_STATUS_IDLE, Attached: true}},
		{name: "uncertain status", receipt: &pb.SessionReceipt{DaemonId: "daemon-test-123", Session: expected, Attached: true}},
		{name: "missing session", receipt: &pb.SessionReceipt{DaemonId: "daemon-test-123", Status: pb.SessionStatus_SESSION_STATUS_IDLE, Attached: true}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := &fakeDaemonService{attachResponse: tc.receipt, attachNilReceipt: tc.attachNil, attachError: tc.attachErr}
			handler, st, publisher := receiptFixture(t, server)
			if _, err := st.UpdateSSHConnectionState(context.Background(), "saved", "disconnected", "not attached", nil); err != nil {
				t.Fatal(err)
			}
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/v1/environments/saved/agents/hermes/connect", nil))
			if tc.wantOK {
				if w.Code != http.StatusOK {
					t.Fatalf("confirmed attach rejected: HTTP=%d body=%s", w.Code, w.Body.String())
				}
				if server.attachHit.Load() != 1 {
					t.Fatalf("confirmed attach ran %d times", server.attachHit.Load())
				}
				return
			}
			if w.Code != http.StatusBadGateway {
				t.Fatalf("unconfirmed attach accepted: HTTP=%d body=%s", w.Code, w.Body.String())
			}
			if server.attachHit.Load() != 1 {
				t.Fatalf("unconfirmed attach was retried: attachHit=%d", server.attachHit.Load())
			}
			if _, present, err := st.GetAgentConnectionChoice(context.Background(), "saved:hermes"); err != nil || present {
				t.Fatalf("unconfirmed attach persisted a choice: present=%v err=%v", present, err)
			}
			stored, err := st.GetSSHConnection(context.Background(), "saved")
			if err != nil || stored == nil || stored["state"] != "disconnected" {
				t.Fatalf("unconfirmed attach wrote state: %v err=%v", stored, err)
			}
			for _, event := range publisher.events {
				if event == "ssh_connection.connected" {
					t.Fatalf("unconfirmed attach published success: %v", publisher.events)
				}
			}
		})
	}
}
