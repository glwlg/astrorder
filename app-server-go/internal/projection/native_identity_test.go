package projection_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"astrorder.dev/app-server/internal/daemonclient"
	"astrorder.dev/app-server/internal/projection"
	"astrorder.dev/app-server/internal/store"
	sessiondv1 "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
)

func TestNativeIdentityPendingApprovalsAndTurnLifecycle(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	tmpDir := t.TempDir()
	st, err := store.Open(filepath.Join(tmpDir, "native_id_test.sqlite3"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	daemonID := "daemon-native-id"

	// Upsert Agent & Session
	_, err = st.UpsertAgent(ctx, map[string]any{
		"id":            "agent-codex",
		"kind":          "codex",
		"name":          "Codex Agent",
		"status":        "connected",
		"capabilities":  []string{"chat"},
		"control_state": "owned",
	})
	if err != nil {
		t.Fatalf("upsert agent: %v", err)
	}

	_, err = st.UpsertSession(ctx, map[string]any{
		"id":            "session-codex-1",
		"agent_id":      "agent-codex",
		"title":         "Codex Session",
		"status":        "idle",
		"control_state": "owned",
		"updated_at":    "2026-10-09T00:00:00Z",
	})
	if err != nil {
		t.Fatalf("upsert session: %v", err)
	}

	// Native Request ID as json.Number (e.g. big int 9007199254740993)
	nativeID := json.Number("9007199254740993")
	authoritativeApprovalID := "codex:9007199254740993"

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
		// 1. turn/started persists ActiveTurn
		{
			Frame: &sessiondv1.EventStreamFrame_Live{
				Live: &sessiondv1.EventEnvelope{
					DaemonId: daemonID,
					Session: &sessiondv1.SessionRef{
						SessionId: "stream-c1",
					},
					SeqId:     1,
					EventType: "codex.notification",
					NativePayload: makeNativePayload(map[string]any{
						"agent_id": "agent-codex",
						"frame": map[string]any{
							"method": "turn/started",
							"params": map[string]any{
								"threadId": "session-codex-1",
								"turnId":   "turn-xyz-100",
							},
						},
					}),
				},
			},
		},
		// 2. requestApproval with authoritative approval_id and native_id
		{
			Frame: &sessiondv1.EventStreamFrame_Live{
				Live: &sessiondv1.EventEnvelope{
					DaemonId: daemonID,
					Session: &sessiondv1.SessionRef{
						SessionId: "stream-c1",
					},
					SeqId:     2,
					EventType: "codex.notification",
					NativePayload: makeNativePayload(map[string]any{
						"agent_id":    "agent-codex",
						"approval_id": authoritativeApprovalID,
						"frame": map[string]any{
							"id":     nativeID,
							"method": "item/commandExecution/requestApproval",
							"params": map[string]any{
								"threadId": "session-codex-1",
								"command":  "echo test",
							},
						},
					}),
				},
			},
		},
		// 3. serverRequest/resolved referencing requestId, resolving previous approval
		{
			Frame: &sessiondv1.EventStreamFrame_Live{
				Live: &sessiondv1.EventEnvelope{
					DaemonId: daemonID,
					Session: &sessiondv1.SessionRef{
						SessionId: "stream-c1",
					},
					SeqId:     3,
					EventType: "codex.notification",
					NativePayload: makeNativePayload(map[string]any{
						"agent_id": "agent-codex",
						"frame": map[string]any{
							"method": "serverRequest/resolved",
							"params": map[string]any{
								"threadId":  "session-codex-1",
								"requestId": nativeID,
								"decision":  "accept",
							},
						},
					}),
				},
			},
		},
		// 4. turn/completed clearing ActiveTurn
		{
			Frame: &sessiondv1.EventStreamFrame_Live{
				Live: &sessiondv1.EventEnvelope{
					DaemonId: daemonID,
					Session: &sessiondv1.SessionRef{
						SessionId: "stream-c1",
					},
					SeqId:     4,
					EventType: "codex.notification",
					NativePayload: makeNativePayload(map[string]any{
						"agent_id": "agent-codex",
						"frame": map[string]any{
							"method": "turn/completed",
							"params": map[string]any{
								"threadId": "session-codex-1",
								"turn": map[string]any{
									"id":     "turn-xyz-100",
									"status": "completed",
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
		t.Fatal("timed out waiting for live ready")
	}

	time.Sleep(300 * time.Millisecond)

	// Check that turn completed cleared ActiveTurn
	activeTurn, err := st.ActiveTurn(ctx, "agent-codex", "session-codex-1")
	if err != nil && err.Error() != "" && activeTurn != "" {
		t.Fatalf("expected ActiveTurn to be empty after completion, got %q (err: %v)", activeTurn, err)
	}
	if activeTurn != "" {
		t.Fatalf("expected ActiveTurn to be empty, got %q", activeTurn)
	}

	// Verify approvals in PendingApprovals: resolved approval should no longer be pending
	pending, err := st.PendingApprovals(ctx)
	if err != nil {
		t.Fatalf("pending approvals: %v", err)
	}
	if len(pending) != 0 {
		t.Fatalf("expected 0 pending approvals after resolved, got %d", len(pending))
	}
}
