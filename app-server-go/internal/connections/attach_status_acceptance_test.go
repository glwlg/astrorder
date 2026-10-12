package connections_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	pb "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
)

// TestAttachStatusRejectsErrorAndUnknownEnumAcrossRoutes covers every wiring
// that consumes h.attachReceipt: the environment agent connect and restart
// actions, the local Codex and local Hermes connect endpoints, and the
// per-connection SSH Hermes connect endpoint. An ERROR session status is a
// known failure and an unknown enum value is not a confirmed live attach;
// neither may write connected/enabled state, publish a success event, or be
// retried.
func TestAttachStatusRejectsErrorAndUnknownEnumAcrossRoutes(t *testing.T) {
	connID := "saved"
	routes := []struct {
		name      string
		path      string
		sessionID string
		connID    *string
		choiceKey string
		restart   bool
		rowGuard  bool
	}{
		{name: "environment connect", path: "/api/v1/environments/saved/agents/hermes/connect", sessionID: "probe-hermes-saved", connID: &connID, choiceKey: "saved:hermes", rowGuard: true},
		{name: "environment restart", path: "/api/v1/environments/saved/agents/hermes/restart", sessionID: "probe-hermes-saved", connID: &connID, choiceKey: "saved:hermes", restart: true, rowGuard: true},
		{name: "local codex connect", path: "/api/v1/connections/codex/connect", sessionID: "probe-codex-local", choiceKey: "local:codex"},
		{name: "local hermes connect", path: "/api/v1/connections/local/connect", sessionID: "probe-hermes-local", choiceKey: "local:hermes"},
		{name: "ssh hermes connect", path: "/api/v1/connections/ssh/saved/connect", sessionID: "probe-hermes-saved", connID: &connID, choiceKey: "saved:hermes", rowGuard: true},
	}
	outcomes := []struct {
		name   string
		status pb.SessionStatus
	}{
		{name: "error status", status: pb.SessionStatus_SESSION_STATUS_ERROR},
		{name: "unknown enum", status: pb.SessionStatus(999)},
	}
	for _, route := range routes {
		for _, outcome := range outcomes {
			t.Run(route.name+"/"+outcome.name, func(t *testing.T) {
				server := &fakeDaemonService{attachResponse: &pb.SessionReceipt{
					DaemonId: "daemon-test-123",
					Session:  &pb.SessionRef{SessionId: route.sessionID, ConnectionId: route.connID},
					Status:   outcome.status,
					Attached: true,
				}}
				handler, st, publisher := receiptFixture(t, server)
				if route.rowGuard {
					if _, err := st.UpdateSSHConnectionState(context.Background(), "saved", "disconnected", "reset for regression", nil); err != nil {
						t.Fatal(err)
					}
				}
				w := httptest.NewRecorder()
				handler.ServeHTTP(w, httptest.NewRequest(http.MethodPost, route.path, nil))
				if w.Code != http.StatusBadGateway {
					t.Errorf("attach with %s was not rejected: HTTP=%d body=%s", outcome.name, w.Code, w.Body.String())
				}
				if server.attachHit.Load() != 1 {
					t.Errorf("rejected attach was retried: attachHit=%d", server.attachHit.Load())
				}
				if route.restart && server.disconnectHit.Load() != 1 {
					t.Errorf("restart did not disconnect exactly once: disconnectHit=%d", server.disconnectHit.Load())
				}
				if route.rowGuard {
					row, err := st.GetSSHConnection(context.Background(), "saved")
					if err != nil {
						t.Fatal(err)
					}
					want := "disconnected"
					if route.restart {
						// The confirmed disconnect already moved the row to
						// "connecting"; the failed re-attach must not promote
						// it to connected nor restore the old claim.
						want = "connecting"
					}
					if row == nil || row["state"] != want {
						t.Errorf("rejected attach mutated the SSH row: want=%s row=%v", want, row)
					}
				}
				if _, present, err := st.GetAgentConnectionChoice(context.Background(), route.choiceKey); err != nil || present {
					t.Errorf("rejected attach persisted an enabled choice: key=%s present=%v err=%v", route.choiceKey, present, err)
				}
				for _, event := range publisher.events {
					if event == "ssh_connection.connected" {
						t.Errorf("rejected attach published connected: %v", publisher.events)
					}
				}
			})
		}
	}
}

// TestAttachStatusWhitelistAcceptsLiveStatuses keeps the compatible success
// path intact: a confirmed live session status (idle, running, or
// waiting_approval) still writes connected state, enables the choice, and
// publishes the success event.
func TestAttachStatusWhitelistAcceptsLiveStatuses(t *testing.T) {
	for _, status := range []pb.SessionStatus{
		pb.SessionStatus_SESSION_STATUS_IDLE,
		pb.SessionStatus_SESSION_STATUS_RUNNING,
		pb.SessionStatus_SESSION_STATUS_WAITING_APPROVAL,
	} {
		t.Run(status.String(), func(t *testing.T) {
			connID := "saved"
			server := &fakeDaemonService{attachResponse: &pb.SessionReceipt{
				DaemonId: "daemon-test-123",
				Session:  &pb.SessionRef{SessionId: "probe-hermes-saved", ConnectionId: &connID},
				Status:   status,
				Attached: true,
			}}
			handler, st, publisher := receiptFixture(t, server)
			if _, err := st.UpdateSSHConnectionState(context.Background(), "saved", "disconnected", "reset for regression", nil); err != nil {
				t.Fatal(err)
			}
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/v1/environments/saved/agents/hermes/connect", nil))
			if w.Code != http.StatusOK {
				t.Fatalf("confirmed live attach rejected: status=%s HTTP=%d body=%s", status, w.Code, w.Body.String())
			}
			row, err := st.GetSSHConnection(context.Background(), "saved")
			if err != nil || row == nil || row["state"] != "connected" {
				t.Fatalf("confirmed live attach did not persist connected: row=%v err=%v", row, err)
			}
			if enabled, present, err := st.GetAgentConnectionChoice(context.Background(), "saved:hermes"); err != nil || !present || !enabled {
				t.Fatalf("confirmed live attach did not enable the choice: enabled=%v present=%v err=%v", enabled, present, err)
			}
			found := false
			for _, event := range publisher.events {
				if event == "ssh_connection.connected" {
					found = true
				}
			}
			if !found {
				t.Fatalf("confirmed live attach did not publish connected: %v", publisher.events)
			}
		})
	}
}
