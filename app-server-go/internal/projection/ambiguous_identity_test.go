package projection_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"astrorder.dev/app-server/internal/daemonclient"
	"astrorder.dev/app-server/internal/projection"
	"astrorder.dev/app-server/internal/store"
	sessiondv1 "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
)

func TestAmbiguousSessionIdentityDegradesGracefullyWithoutPanic(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	tmpDir := t.TempDir()
	st, err := store.Open(filepath.Join(tmpDir, "ambiguous_test.sqlite3"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	daemonID := "daemon-ambig-1"

	// Pre-seed agent-1 and agent-2 with owned sessions having the same session ID "shared-sid"
	_, err = st.UpsertAgent(ctx, map[string]any{
		"id":            "agent-1",
		"kind":          "grok",
		"name":          "Grok Agent",
		"status":        "connected",
		"capabilities":  []string{"chat"},
		"control_state": "owned",
	})
	if err != nil {
		t.Fatalf("upsert agent-1: %v", err)
	}
	_, err = st.UpsertAgent(ctx, map[string]any{
		"id":            "agent-2",
		"kind":          "hermes",
		"name":          "Hermes Agent",
		"status":        "connected",
		"capabilities":  []string{"chat"},
		"control_state": "owned",
	})
	if err != nil {
		t.Fatalf("upsert agent-2: %v", err)
	}

	_, err = st.UpsertSession(ctx, map[string]any{
		"id":            "shared-sid",
		"agent_id":      "agent-1",
		"title":         "Grok Session",
		"status":        "running",
		"control_state": "owned",
		"updated_at":    "2026-10-09T00:00:00Z",
	})
	if err != nil {
		t.Fatalf("upsert session 1: %v", err)
	}
	_, err = st.UpsertSession(ctx, map[string]any{
		"id":            "shared-sid",
		"agent_id":      "agent-2",
		"title":         "Hermes Session",
		"status":        "running",
		"control_state": "owned",
		"updated_at":    "2026-10-09T00:00:00Z",
	})
	if err != nil {
		t.Fatalf("upsert session 2: %v", err)
	}

	// Frame without explicit agent_id: previously caused ambiguous session identity error.
	// Now should degrade gracefully by picking the first owned session.
	frames := []*sessiondv1.EventStreamFrame{
		{
			Frame: &sessiondv1.EventStreamFrame_Snapshot{
				Snapshot: &sessiondv1.StateSnapshot{
					DaemonId: daemonID,
					Sessions: []*sessiondv1.SessionSnapshot{},
				},
			},
		},
		{
			Frame: &sessiondv1.EventStreamFrame_LiveReady{
				LiveReady: &sessiondv1.LiveReady{
					DaemonId:   daemonID,
					Watermarks: []*sessiondv1.SessionWatermark{},
				},
			},
		},
		// Frame with Grok turn completed but NO agent_id in native payload
		{
			Frame: &sessiondv1.EventStreamFrame_Live{
				Live: &sessiondv1.EventEnvelope{
					DaemonId: daemonID,
					Session: &sessiondv1.SessionRef{
						SessionId: "shared-sid",
					},
					SeqId:     1,
					EventType: "grok.notification",
					Status:    sessiondv1.SessionStatus_SESSION_STATUS_IDLE,
					NativePayload: makeNativePayload(map[string]any{
						"session_id": "shared-sid",
						"frame": map[string]any{
							"params": map[string]any{
								"update": map[string]any{
									"sessionUpdate": "turn_completed",
								},
							},
						},
					}),
				},
			},
		},
	}

	fake := &fakeEventsServer{
		daemonID: daemonID,
		frames:   frames,
	}
	addr, stopServer := startFakeEventsServer(t, fake)
	defer stopServer()

	client, err := daemonclient.Dial(ctx, daemonclient.Config{
		Target:         addr,
		Credential:     "cred",
		RequestTimeout: 2 * time.Second,
	})
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer client.Close()

	pub := &testPublisher{}
	proj, err := projection.New(client, st, pub)
	if err != nil {
		t.Fatalf("new projection: %v", err)
	}

	readyCh := make(chan struct{})
	runCtx, runCancel := context.WithCancel(ctx)
	defer runCancel()

	go func() {
		_ = proj.RunReady(runCtx, readyCh)
	}()

	select {
	case <-readyCh:
	case <-time.After(3 * time.Second):
		t.Fatal("projection timed out waiting for ready")
	}

	// Wait for frame to be processed
	time.Sleep(200 * time.Millisecond)

	// Verify that projection did not fail/halt: one of the sessions should have been updated to idle
	s1, _ := st.GetSession(ctx, "agent-1", "shared-sid")
	s2, _ := st.GetSession(ctx, "agent-2", "shared-sid")
	if s1 == nil || s2 == nil {
		t.Fatal("sessions not found")
	}
	// One of them must have been transitioned to idle by the fallback resolution
	if s1["status"] != "idle" && s2["status"] != "idle" {
		t.Fatalf("expected at least one session to be idle: s1=%v, s2=%v", s1["status"], s2["status"])
	}
}
