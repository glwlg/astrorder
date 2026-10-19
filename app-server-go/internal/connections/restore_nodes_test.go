package connections_test

import (
	pb "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
	"context"
	"testing"
)

func TestRestoreSelectedNodeKindsAndHonorsDisconnects(t *testing.T) {
	daemon := &fakeDaemonService{}
	h, st := setupRestoreFixture(t, daemon, &recordingResolver{results: map[string]string{"codex": "/opt/codex", "grok": "/opt/grok"}})
	if _, err := st.SaveSSHConnection(context.Background(), map[string]any{"id": "saved", "host": "fixture.invalid", "state": "disconnected"}); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"local:hermes", "local:codex", "local:grok", "saved:hermes", "saved:codex"} {
		if err := st.SetAgentConnectionChoice(context.Background(), key, true); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.SetAgentConnectionChoice(context.Background(), "saved:grok", false); err != nil {
		t.Fatal(err)
	}
	if err := h.Restore(context.Background()); err != nil {
		t.Fatal(err)
	}
	daemon.attachRequestsMu.Lock()
	defer daemon.attachRequestsMu.Unlock()
	found := map[string]bool{}
	for _, req := range daemon.attachRequests {
		found[req.Session.GetSessionId()] = true
		if !req.RuntimeControl {
			t.Fatalf("restored a conversation instead of a control channel: %v", req)
		}
	}
	for _, id := range []string{"probe-hermes-local", "probe-codex-local", "probe-grok-local", "probe-hermes-saved", "probe-codex-saved"} {
		if !found[id] {
			t.Errorf("missing %s", id)
		}
	}
	if found["probe-grok-saved"] {
		t.Fatal("restored explicitly disabled remote Grok")
	}
}

func TestRestoreDoesNotAttachAgainstUnconfirmedDaemon(t *testing.T) {
	daemon := &fakeDaemonService{}
	h, _ := setupRestoreFixture(t, daemon, nil)
	daemon.statusResponse.Store(&pb.GetStatusResponse{DaemonId: "foreign"})
	if err := h.Restore(context.Background()); err == nil {
		t.Fatal("accepted foreign daemon")
	}
	if daemon.attachHit.Load() != 0 {
		t.Fatal("attached before confirming daemon identity")
	}
}
