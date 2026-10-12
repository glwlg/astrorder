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

func TestProjectionReplayBarrierMismatchFails(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	tmpDir := t.TempDir()
	st, err := store.Open(filepath.Join(tmpDir, "barrier_mismatch.sqlite3"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	daemonID := "daemon-epoch-barrier"

	// LiveReady claims watermark through 5, but snapshot only had 2
	frames := []*sessiondv1.EventStreamFrame{
		{
			Frame: &sessiondv1.EventStreamFrame_Snapshot{
				Snapshot: &sessiondv1.StateSnapshot{
					DaemonId: daemonID,
					Sessions: []*sessiondv1.SessionSnapshot{
						{
							Session:            &sessiondv1.SessionRef{SessionId: "session-1"},
							ReplayThroughSeqId: 2,
						},
					},
				},
			},
		},
		{
			Frame: &sessiondv1.EventStreamFrame_LiveReady{
				LiveReady: &sessiondv1.LiveReady{
					DaemonId: daemonID,
					Watermarks: []*sessiondv1.SessionWatermark{
						{
							Session:            &sessiondv1.SessionRef{SessionId: "session-1"},
							ReplayThroughSeqId: 5, // Mismatch!
						},
					},
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

	err = proj.Run(ctx)
	if err == nil || !projection.IsFatalProjectionError(err) {
		t.Fatalf("expected fatal watermark mismatch error, got: %v", err)
	}
}

func TestProjectionGrokLifecycleAndTasks(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	tmpDir := t.TempDir()
	st, err := store.Open(filepath.Join(tmpDir, "grok_test.sqlite3"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	daemonID := "daemon-epoch-grok"

	// Initial session row
	_, err = st.UpsertSession(ctx, map[string]any{
		"id":         "grok-sess",
		"agent_id":   "grok-agent",
		"title":      "Grok Test",
		"status":     "running",
		"updated_at": "2026-10-09T12:00:00Z",
	})
	if err != nil {
		t.Fatalf("upsert initial session: %v", err)
	}

	// Inflight command
	_, _, err = st.CreateCommand(ctx, map[string]any{
		"id":          "grok-cmd-1",
		"agent_id":    "grok-agent",
		"session_id":  "grok-sess",
		"action":      "chat",
		"text":        "hello",
		"attachments": []any{},
	}, "running")
	if err != nil {
		t.Fatalf("create command: %v", err)
	}

	frames := []*sessiondv1.EventStreamFrame{
		{
			Frame: &sessiondv1.EventStreamFrame_Snapshot{
				Snapshot: &sessiondv1.StateSnapshot{
					DaemonId: daemonID,
					Sessions: []*sessiondv1.SessionSnapshot{
						{
							Session:            &sessiondv1.SessionRef{SessionId: "grok-sess"},
							ReplayThroughSeqId: 0,
						},
					},
				},
			},
		},
		{
			Frame: &sessiondv1.EventStreamFrame_LiveReady{
				LiveReady: &sessiondv1.LiveReady{
					DaemonId: daemonID,
					Watermarks: []*sessiondv1.SessionWatermark{
						{
							Session:            &sessiondv1.SessionRef{SessionId: "grok-sess"},
							ReplayThroughSeqId: 0,
						},
					},
				},
			},
		},
		{
			Frame: &sessiondv1.EventStreamFrame_Live{
				Live: &sessiondv1.EventEnvelope{
					DaemonId:  daemonID,
					Session:   &sessiondv1.SessionRef{SessionId: "grok-sess", AgentId: strPtr("grok-agent")},
					SeqId:     1,
					EventType: "grok.completed",
					NativePayload: makeNativePayload(map[string]any{
						"agent_id":   "grok-agent",
						"command_id": "grok-cmd-1",
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

	readyCh := make(chan struct{}, 1)
	runCtx, runCancel := context.WithCancel(ctx)
	go func() {
		_ = proj.RunReady(runCtx, readyCh)
	}()

	select {
	case <-readyCh:
	case <-time.After(2 * time.Second):
		t.Fatal("RunReady did not signal ready")
	}

	// Verify command transition to completed
	for i := 0; i < 50; i++ {
		cmd, _ := st.GetCommand(ctx, "grok-agent", "grok-sess", "grok-cmd-1")
		if cmd != nil && cmd["state"] == "completed" {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	cmd, err := st.GetCommand(ctx, "grok-agent", "grok-sess", "grok-cmd-1")
	if err != nil || cmd == nil || cmd["state"] != "completed" {
		t.Fatalf("command state not updated to completed: %+v (err: %v)", cmd, err)
	}

	sess, err := st.GetSession(ctx, "grok-agent", "grok-sess")
	if err != nil || sess == nil || sess["status"] != "idle" {
		t.Fatalf("session status not updated to idle: %+v (err: %v)", sess, err)
	}

	runCancel()
}
