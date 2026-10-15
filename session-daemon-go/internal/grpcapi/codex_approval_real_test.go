package grpcapi_test

import (
	"context"
	"os"
	"testing"
	"time"

	pb "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
	"astrorder.dev/session-daemon/internal/runtime/codex"
)

// Opt-in smoke with the installed Codex executable, empty CODEX_HOME, and
// independent daemon state. It never reads the user's native chats or credentials.
func TestRealCodexApprovalReadbackOverGRPC(t *testing.T) {
	executable := os.Getenv("ASTRORDER_REAL_CODEX")
	if executable == "" {
		t.Skip("ASTRORDER_REAL_CODEX is not configured")
	}
	f := startFixture(t)
	root, home := t.TempDir(), t.TempDir()
	adapter := codex.NewAdapter(codex.Config{Executable: executable, Workspace: root, Allowed: []string{root}, AgentID: "approval-real", Environment: []string{"CODEX_HOME=" + home}}, nil)
	defer adapter.Close()
	f.daemon.RegisterRuntime("codex", adapter)
	ctx, cancel := context.WithTimeout(f.ctx, 30*time.Second)
	defer cancel()
	created, err := pb.NewSessionControlClient(f.conn).CreateSession(ctx, &pb.CreateSessionRequest{Meta: controlMeta("approval-real-create"), Target: &pb.RuntimeTarget{Runtime: pb.RuntimeKind_RUNTIME_KIND_CODEX}, Options: &pb.CreateSessionRequest_Codex{Codex: &pb.CodexCreateOptions{Cwd: root}}})
	if err != nil || created.GetCompleted() == nil {
		t.Fatalf("real create: %v %v", created, err)
	}
	ref := created.Session
	client := pb.NewRuntimeQueryClient(f.conn)
	initial, err := client.ReadApprovalMode(ctx, &pb.ReadApprovalModeRequest{Session: ref})
	if err != nil || initial.GetMode() != "manual" {
		t.Fatalf("initial read: %v %v", initial, err)
	}
	for _, mode := range []string{"auto", "full_access", "manual"} {
		receipt, err := client.SetApprovalMode(ctx, &pb.SetApprovalModeRequest{Meta: controlMeta("approval-real-" + mode), Session: ref, Mode: mode})
		if err != nil || receipt.GetCompleted() == nil {
			t.Fatalf("set %s: %v %v", mode, receipt, err)
		}
		readback, err := client.ReadApprovalMode(ctx, &pb.ReadApprovalModeRequest{Session: ref})
		if err != nil || readback.GetMode() != mode {
			t.Fatalf("read %s: %v %v", mode, readback, err)
		}
		t.Logf("real Codex + TCP gRPC: %s confirmed", mode)
	}
}
