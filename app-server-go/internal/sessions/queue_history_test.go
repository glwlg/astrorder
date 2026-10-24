package sessions

import (
	pb "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
	"context"
	"testing"
)

func TestQueueDispatchAfterExplicitCompletedInput(t *testing.T) {
	for _, tc := range []struct {
		name, action, state string
		checkpoint          bool
		want                int
	}{
		{"old unknown before completed guidance", "send", "unknown", true, 0},
		{"old accepted before completed guidance", "enqueue", "accepted", true, 0},
		{"accepted stop with live idle", "stop", "accepted", false, 1},
		{"unknown input without completion fence", "send", "unknown", false, 0},
		{"accepted input without completion fence", "send", "accepted", false, 0},
		{"received input without completion fence", "send", "received", false, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			owned, aid, kind := true, "a", pb.RuntimeKind_RUNTIME_KIND_HERMES
			srv := &guidanceModelServer{snapshot: &pb.SessionSnapshot{Session: &pb.SessionRef{SessionId: "s", AgentId: &aid}, RuntimeOwned: &owned, Runtime: &kind, Status: pb.SessionStatus_SESSION_STATUS_IDLE}}
			h, st, _, cleanup := setupTestServer(t, &srv.mockDaemonServer)
			defer cleanup()
			addr, stop := startGuidanceStatusTarget(t, srv)
			defer stop()
			client := dialModelRestoreTarget(t, addr)
			defer client.Close()
			h.client.Daemon = client.Daemon
			h.client.Session = client.Session
			ctx := context.Background()
			if _, err := st.UpsertAgent(ctx, map[string]any{"id": "a", "kind": "hermes", "name": "fixture", "status": "ready"}); err != nil {
				t.Fatal(err)
			}
			if _, err := st.UpsertSession(ctx, map[string]any{"id": "s", "agent_id": "a", "status": "idle", "control_state": "owned", "updated_at": "2026-10-20T00:00:00Z"}); err != nil {
				t.Fatal(err)
			}
			create := func(id, action, state string) {
				t.Helper()
				if _, _, err := st.CreateCommand(ctx, map[string]any{"id": id, "agent_id": "a", "session_id": "s", "action": action, "text": "fixture"}, state); err != nil {
					t.Fatal(err)
				}
			}
			create("old", tc.action, tc.state)
			if tc.checkpoint {
				create("explicit-new-input", "send", "completed")
			}
			create("next", "enqueue", "queued")
			calls := 0
			srv.sendCb = func(r *pb.SendRequest) (*pb.CommandReceipt, error) {
				calls++
				if r.Meta.GetCommandId() != "next" {
					t.Error("replayed historical command")
				}
				if string(r.GetPolicy().GetNativeOptions().GetUtf8Json()) != `{"idle_only":true}` {
					t.Error("lost idle admission")
				}
				return &pb.CommandReceipt{OperationState: pb.OperationState_OPERATION_STATE_COMPLETED, Accepted: true, Outcome: &pb.CommandReceipt_Completed{Completed: &pb.CommandResult{Accepted: true}}}, nil
			}
			if err := h.DispatchQueue(ctx); err != nil {
				t.Fatal(err)
			}
			if calls != tc.want {
				t.Fatalf("live idle queue sent %d messages, want %d", calls, tc.want)
			}
			old, err := st.GetCommand(ctx, "a", "s", "old")
			if err != nil || old["state"] != tc.state {
				t.Fatalf("historical outcome was invented: %v %v", old, err)
			}
		})
	}
}
