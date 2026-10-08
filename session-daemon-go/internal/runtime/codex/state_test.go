package codex

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestWorkspaceRejectsSymlinkEscape(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	link := filepath.Join(root, "escape")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	r := New(Config{Allowed: []string{root}})
	defer r.Close()
	if _, err := r.workspace(link); err == nil {
		t.Fatal("symlink escaped workspace allowlist")
	}
}

func TestApprovalResolvedAndCompletedProjectNativeStatus(t *testing.T) {
	r := New(Config{})
	defer r.Close()
	r.setSession("thread", "idle")
	r.handleNotification("turn/started", map[string]any{"params": map[string]any{"threadId": "thread", "turn": map[string]any{"id": "turn"}}})
	r.handleNotification("item/commandExecution/requestApproval", map[string]any{"id": float64(91), "params": map[string]any{"threadId": "thread"}})
	if r.status != "waiting_approval" {
		t.Fatal(r.status)
	}
	r.handleNotification("serverRequest/resolved", map[string]any{"params": map[string]any{"requestId": float64(91)}})
	if r.status != "running" || len(r.pendingApprovals) != 0 {
		t.Fatalf("approval resolution: %s %v", r.status, r.pendingApprovals)
	}
	r.handleNotification("turn/completed", map[string]any{"params": map[string]any{"threadId": "thread", "turn": map[string]any{"id": "turn", "status": "completed"}}})
	if r.status != "idle" {
		t.Fatal(r.status)
	}
}

func TestDeleteRefusesActiveSession(t *testing.T) {
	r := New(Config{})
	defer r.Close()
	r.setSession("thread", "running")
	if _, err := r.Command(context.Background(), Command{Action: "session.delete", SessionID: "thread"}); err == nil {
		t.Fatal("active thread deletion accepted")
	}
}
