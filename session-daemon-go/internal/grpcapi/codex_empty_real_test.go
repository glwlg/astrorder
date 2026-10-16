package grpcapi_test

import (
	pb "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
	"astrorder.dev/session-daemon/internal/runtime/codex"
	"context"
	"os"
	"testing"
	"time"
)

func TestRealCodexEmptyReadsOverGRPC(t *testing.T) {
	executable := os.Getenv("ASTRORDER_REAL_CODEX")
	if executable == "" {
		t.Skip("ASTRORDER_REAL_CODEX is not configured")
	}
	f := startFixture(t)
	root, home := t.TempDir(), t.TempDir()
	adapter := codex.NewAdapter(codex.Config{Executable: executable, Workspace: root, Allowed: []string{root}, AgentID: "empty-grpc", Environment: []string{"CODEX_HOME=" + home}}, nil)
	defer adapter.Close()
	f.daemon.RegisterRuntime("codex", adapter)
	ctx, cancel := context.WithTimeout(f.ctx, 20*time.Second)
	defer cancel()
	created, err := pb.NewSessionControlClient(f.conn).CreateSession(ctx, &pb.CreateSessionRequest{Meta: controlMeta("empty-real-create"), Target: &pb.RuntimeTarget{Runtime: pb.RuntimeKind_RUNTIME_KIND_CODEX}, Options: &pb.CreateSessionRequest_Codex{Codex: &pb.CodexCreateOptions{Cwd: root}}})
	if err != nil || created.GetCompleted() == nil {
		t.Fatalf("create: %v %v", created, err)
	}
	client := pb.NewRuntimeQueryClient(f.conn)
	for i := 0; i < 3; i++ {
		page, err := client.GetHistoryPage(ctx, &pb.HistoryPageRequest{Session: created.Session, Limit: 50})
		if err != nil || len(page.GetItems()) != 0 || page.GetHasMore() {
			t.Fatalf("history: %v %v", page, err)
		}
		processes, err := client.ListBackgroundProcesses(ctx, &pb.ListBackgroundProcessesRequest{Session: created.Session})
		if err != nil || len(processes.GetProcesses()) != 0 {
			t.Fatalf("processes: %v %v", processes, err)
		}
	}
}
