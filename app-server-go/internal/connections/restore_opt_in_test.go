package connections_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	connections "astrorder.dev/app-server/internal/connections"
	pb "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
)

func TestExplicitDisconnectOverridesStaleLocalOwnership(t *testing.T) {
	daemon := &fakeDaemonService{}
	h, st := setupRestoreFixture(t, daemon, nil)
	daemon.statusResponse.Store(&pb.GetStatusResponse{DaemonId: "daemon-test-123", Runtimes: []*pb.RuntimeStatus{{Runtime: pb.RuntimeKind_RUNTIME_KIND_CODEX, Registered: true, OwnedSessions: 1}}})
	if err := st.SetAgentConnectionChoice(context.Background(), "local:codex", false); err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/connections/codex", nil))
	var result map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result["state"] != "disconnected" {
		t.Fatalf("disabled Agent looked connected: %s", w.Body.String())
	}
}

func TestRestoreDoesNotOptIntoUnusedAgents(t *testing.T) {
	daemon := &fakeDaemonService{}
	h, st := setupRestoreFixture(t, daemon, &recordingResolver{results: map[string]string{"codex": "/opt/codex", "grok": "/opt/grok"}})
	if _, err := st.SaveSSHConnection(context.Background(), map[string]any{"id": "saved", "host": "fixture.invalid", "state": "configured"}); err != nil {
		t.Fatal(err)
	}
	if err := h.Restore(context.Background()); err != nil {
		t.Fatal(err)
	}
	if daemon.attachHit.Load() != 0 {
		t.Fatalf("unselected agents automatically attached: %d", daemon.attachHit.Load())
	}
}

func TestExplicitDisconnectSurvivesRestoreUntilReconnect(t *testing.T) {
	for _, node := range []string{"local", "saved"} {
		for _, kind := range []string{"hermes", "codex", "grok", "antigravity"} {
			if node != "local" && kind == "antigravity" {
				continue
			}
			t.Run(node+"/"+kind, func(t *testing.T) {
				daemon := &fakeDaemonService{}
				h, st, _ := receiptFixture(t, daemon)
				if err := st.SetAgentConnectionChoice(context.Background(), node+":"+kind, true); err != nil {
					t.Fatal(err)
				}
				request := func(action string) {
					w := httptest.NewRecorder()
					h.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/v1/environments/"+node+"/agents/"+kind+"/"+action, nil))
					if w.Code != http.StatusOK {
						t.Fatalf("%s: %d %s", action, w.Code, w.Body.String())
					}
				}
				request("disconnect")
				for range 3 {
					if err := h.(*connections.Handler).Restore(context.Background()); err != nil {
						t.Fatal(err)
					}
				}
				daemon.attachRequestsMu.Lock()
				for _, r := range daemon.attachRequests {
					if r.Session.GetSessionId() == "probe-"+kind+"-"+node {
						t.Error("disabled Agent reattached")
					}
				}
				daemon.attachRequestsMu.Unlock()
				request("connect")
				enabled, present, err := st.GetAgentConnectionChoice(context.Background(), node+":"+kind)
				if err != nil || !present || !enabled {
					t.Fatalf("explicit reconnect did not enable restore: %v %v %v", enabled, present, err)
				}
			})
		}
	}
}

func TestRestoreRetainsPreviouslyConnectedAgentWithoutEnablingSiblings(t *testing.T) {
	daemon := &fakeDaemonService{}
	h, st := setupRestoreFixture(t, daemon, nil)
	if _, err := st.UpsertAgent(context.Background(), map[string]any{"id": "existing", "kind": "codex", "name": "Codex", "status": "ready"}); err != nil {
		t.Fatal(err)
	}
	if err := h.Restore(context.Background()); err != nil {
		t.Fatal(err)
	}
	if daemon.attachHit.Load() != 1 || daemon.attachRequest.Load().Target.GetRuntime() != pb.RuntimeKind_RUNTIME_KIND_CODEX {
		t.Fatalf("restored unrelated or no runtime: %d", daemon.attachHit.Load())
	}
	enabled, present, err := st.GetAgentConnectionChoice(context.Background(), "local:codex")
	if err != nil || !enabled || !present {
		t.Fatal("legacy confirmed connection was not remembered")
	}
}
