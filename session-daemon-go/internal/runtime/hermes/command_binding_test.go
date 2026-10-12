package hermes

import (
	core "astrorder.dev/session-daemon/internal/runtime"
	"context"
	"os"
	"testing"
	"time"
)

func TestHermesCompletionCarriesExactSubmittedCommand(t *testing.T) {
	root := t.TempDir()
	events := make(chan map[string]any, 16)
	a := New(Config{Executable: os.Args[0], Arguments: []string{"-test.run=^TestHermesAdapterOwnsMultipleSessions$"}, Environment: []string{"ASTRORDER_HERMES_HELPER=1"}, Workspace: root, Allowed: []string{root}, AgentID: "fixture", Emit: func(sid, event string, payload map[string]any, status string) error {
		if event == "hermes.command_complete" {
			events <- payload
		}
		return nil
	}})
	defer a.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	created, err := a.Create(ctx, core.Request{Fields: map[string]any{"cwd": root}})
	if err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{"trigger:complete-before-reply", "normal"} {
		exact := " command " + text
		if _, err := a.Command(ctx, core.Request{SessionID: created.SessionID, Action: "session.send", Fields: map[string]any{"text": text, "command_id": exact}}); err != nil {
			t.Fatal(err)
		}
		select {
		case event := <-events:
			if event["command_id"] != exact || event["agent_id"] != "fixture" || event["session_id"] != created.SessionID {
				t.Fatalf("completion binding lost: %+v", event)
			}
		case <-ctx.Done():
			t.Fatal("completion absent")
		}
	}
}
func TestHermesApprovalPublishesExactDecision(t *testing.T) {
	events := make(chan map[string]any, 1)
	a := New(Config{AgentID: "fixture", Emit: func(sid, event string, payload map[string]any, status string) error {
		if event == "hermes.approval_resolved" {
			events <- payload
		}
		return nil
	}})
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	defer writer.Close()
	a.stdin = writer
	s := &SessionState{SessionID: "s", Status: "waiting_approval", Metadata: map[string]any{}}
	a.sessions["s"] = s
	a.pendingApprovals["exact"] = &approvalRecord{NativeID: "srq-1", SessionID: "s", Method: "approval"}
	if _, err := a.handleApprove(context.Background(), s, core.Request{Fields: map[string]any{"approval_id": "exact", "choice": "deny", "command_id": "decision-id"}}); err != nil {
		t.Fatal(err)
	}
	select {
	case payload := <-events:
		if payload["approval_id"] != "exact" || payload["command_id"] != "decision-id" || payload["state"] != "rejected" {
			t.Fatalf("wrong decision=%v", payload)
		}
	default:
		t.Fatal("approval decision not projected")
	}
}
