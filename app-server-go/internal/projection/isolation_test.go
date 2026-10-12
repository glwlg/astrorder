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

func TestProjectionIsolationAcrossAgentsWithSameNativeSID(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	tmpDir := t.TempDir()
	st, err := store.Open(filepath.Join(tmpDir, "isolation_test.sqlite3"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	daemonID := "daemon-iso-1"

	// Pre-seed agent-1 and agent-2 with their respective sessions having the SAME session ID "sess-shared"
	_, err = st.UpsertAgent(ctx, map[string]any{
		"id":            "agent-1",
		"kind":          "hermes",
		"name":          "Agent One",
		"status":        "connected",
		"capabilities":  []string{"chat"},
		"control_state": "owned",
	})
	if err != nil {
		t.Fatalf("upsert agent-1: %v", err)
	}
	_, err = st.UpsertAgent(ctx, map[string]any{
		"id":            "agent-2",
		"kind":          "codex",
		"name":          "Agent Two",
		"status":        "connected",
		"capabilities":  []string{"chat"},
		"control_state": "owned",
	})
	if err != nil {
		t.Fatalf("upsert agent-2: %v", err)
	}

	_, err = st.UpsertSession(ctx, map[string]any{
		"id":            "sess-shared",
		"agent_id":      "agent-1",
		"title":         "Agent 1 Session",
		"status":        "running",
		"control_state": "owned",
		"updated_at":    "2026-10-09T00:00:00Z",
	})
	if err != nil {
		t.Fatalf("upsert session 1: %v", err)
	}

	_, err = st.UpsertSession(ctx, map[string]any{
		"id":            "sess-shared",
		"agent_id":      "agent-2",
		"title":         "Agent 2 Session",
		"status":        "waiting_approval",
		"control_state": "owned",
		"updated_at":    "2026-10-09T00:00:00Z",
	})
	if err != nil {
		t.Fatalf("upsert session 2: %v", err)
	}

	// Stream 1 updates agent-1's sess-shared via Hermes command complete
	// Stream 2 updates agent-2's sess-shared via Codex notification
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
		// Frame 1: Hermes command completion targeted specifically at agent-1
		{
			Frame: &sessiondv1.EventStreamFrame_Live{
				Live: &sessiondv1.EventEnvelope{
					DaemonId: daemonID,
					Session: &sessiondv1.SessionRef{
						SessionId: "stream-hermes",
					},
					SeqId:     1,
					EventType: "hermes.command_complete",
					Status:    sessiondv1.SessionStatus_SESSION_STATUS_IDLE,
					NativePayload: makeNativePayload(map[string]any{
						"agent_id":   "agent-1",
						"session_id": "sess-shared",
					}),
				},
			},
		},
		// Frame 2: Codex turn started targeted specifically at agent-2
		{
			Frame: &sessiondv1.EventStreamFrame_Live{
				Live: &sessiondv1.EventEnvelope{
					DaemonId: daemonID,
					Session: &sessiondv1.SessionRef{
						SessionId: "stream-codex",
					},
					SeqId:     1,
					EventType: "codex.notification",
					NativePayload: makeNativePayload(map[string]any{
						"agent_id": "agent-2",
						"frame": map[string]any{
							"method": "turn/started",
							"params": map[string]any{
								"threadId": "sess-shared",
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
		t.Fatal("timed out waiting for live ready")
	}

	// Allow live frames to project
	time.Sleep(300 * time.Millisecond)

	// Verify agent-1 session is now "idle"
	s1, err := st.GetSession(ctx, "agent-1", "sess-shared")
	if err != nil {
		t.Fatalf("get session 1: %v", err)
	}
	if s1["status"] != "idle" {
		t.Fatalf("expected agent-1 sess-shared status 'idle', got %v", s1["status"])
	}

	// Verify agent-2 session is now "running"
	s2, err := st.GetSession(ctx, "agent-2", "sess-shared")
	if err != nil {
		t.Fatalf("get session 2: %v", err)
	}
	if s2["status"] != "running" {
		t.Fatalf("expected agent-2 sess-shared status 'running', got %v", s2["status"])
	}
}

func TestProjectionApprovalWaitingPreservedOnCompletion(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	tmpDir := t.TempDir()
	st, err := store.Open(filepath.Join(tmpDir, "approval_test.sqlite3"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	daemonID := "daemon-appr-1"

	_, err = st.UpsertAgent(ctx, map[string]any{
		"id":            "agent-hermes",
		"kind":          "hermes",
		"name":          "Hermes Agent",
		"status":        "connected",
		"capabilities":  []string{"chat"},
		"control_state": "owned",
	})
	if err != nil {
		t.Fatalf("upsert agent: %v", err)
	}

	_, err = st.UpsertSession(ctx, map[string]any{
		"id":            "sess-appr",
		"agent_id":      "agent-hermes",
		"title":         "Approval Session",
		"status":        "waiting_approval",
		"control_state": "owned",
		"updated_at":    "2026-10-09T00:00:00Z",
	})
	if err != nil {
		t.Fatalf("upsert session: %v", err)
	}

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
		{
			Frame: &sessiondv1.EventStreamFrame_Live{
				Live: &sessiondv1.EventEnvelope{
					DaemonId: daemonID,
					Session: &sessiondv1.SessionRef{
						SessionId: "stream-hermes-appr",
					},
					SeqId:     1,
					EventType: "hermes.command_complete",
					Status:    sessiondv1.SessionStatus_SESSION_STATUS_WAITING_APPROVAL,
					NativePayload: makeNativePayload(map[string]any{
						"agent_id":   "agent-hermes",
						"session_id": "sess-appr",
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
		t.Fatal("timed out waiting for live ready")
	}

	time.Sleep(300 * time.Millisecond)

	s, err := st.GetSession(ctx, "agent-hermes", "sess-appr")
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	if s["status"] != "waiting_approval" {
		t.Fatalf("session status should remain 'waiting_approval', got %v", s["status"])
	}
}
