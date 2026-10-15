package grpcapi_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	pb "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
	"astrorder.dev/session-daemon/internal/runtime/codex"
	"astrorder.dev/session-daemon/internal/transport/ssh"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Exercise the real registry and Codex adapter over authenticated TCP gRPC.
// Only the native stdio peer is synthetic; it captures actual turn/start fields.
func TestCodexApprovalModeOverOfficialGRPC(t *testing.T) {
	for _, kind := range []string{"codex", "codex-ssh"} {
		for _, mode := range []string{"manual", "auto", "full_access"} {
			t.Run(kind+"/"+mode, func(t *testing.T) {
				f := startFixture(t)
				transport := newGRPCControlTransport()
				defer transport.Close()
				turns := make(chan map[string]any, 1)
				go func() {
					decoder, encoder := json.NewDecoder(transport.stdinR), json.NewEncoder(transport.stdoutW)
					for {
						var request map[string]any
						if decoder.Decode(&request) != nil {
							return
						}
						if request["method"] == "initialized" {
							continue
						}
						result := map[string]any{}
						switch request["method"] {
						case "initialize":
						case "thread/start", "thread/read":
							result["thread"] = map[string]any{"id": "approval-thread", "model": "fixture"}
						case "turn/start":
							params, _ := request["params"].(map[string]any)
							turns <- params
							result["turn"] = map[string]any{"id": "approval-turn"}
						default:
							_ = encoder.Encode(map[string]any{"id": request["id"], "error": map[string]any{"code": -32601, "message": "unexpected fixture method"}})
							continue
						}
						if encoder.Encode(map[string]any{"id": request["id"], "result": result}) != nil {
							return
						}
					}
				}()
				adapter := codex.NewSSHAdapter(codex.Config{Executable: "/synthetic/codex", Workspace: "/audit", Allowed: []string{"/audit"}, AgentID: "approval-agent"}, nil,
					func(context.Context, ssh.CommandSpec) (codex.SSHSession, error) { return transport, nil })
				defer adapter.Close()
				f.daemon.RegisterRuntime(kind, adapter)
				legacyWSOperation(t, f.daemon, map[string]any{"action": "session.create", "agent_type": kind, "cwd": "/audit"})
				ctx, cancel := context.WithTimeout(f.ctx, 5*time.Second)
				defer cancel()
				client := pb.NewRuntimeQueryClient(f.conn)
				ref := &pb.SessionRef{SessionId: "approval-thread", AgentId: ptr("approval-agent")}
				request := &pb.ReadApprovalModeRequest{Session: ref}
				if _, err := client.ReadApprovalMode(context.Background(), request); status.Code(err) != codes.Unauthenticated {
					t.Fatalf("auth: %v", err)
				}
				if _, err := client.ReadApprovalMode(ctx, &pb.ReadApprovalModeRequest{Session: &pb.SessionRef{SessionId: ref.SessionId, AgentId: ptr("foreign")}}); status.Code(err) != codes.FailedPrecondition {
					t.Fatalf("owner: %v", err)
				}
				receipt, err := client.SetApprovalMode(ctx, &pb.SetApprovalModeRequest{Meta: controlMeta("approval-set"), Session: ref, Mode: mode})
				if err != nil || receipt.GetCompleted() == nil {
					t.Fatalf("set: %v %v", receipt, err)
				}
				before, err := f.daemon.AuthorityStatus()
				if err != nil {
					t.Fatal(err)
				}
				readback, err := client.ReadApprovalMode(ctx, request)
				if err != nil || readback.GetMode() != mode {
					t.Fatalf("ReadApprovalMode verification failed: got=%v err=%v", readback, err)
				}
				after, err := f.daemon.AuthorityStatus()
				if err != nil || len(after.Sessions) != 1 || after.Sessions[0].MaxSeqID != before.Sessions[0].MaxSeqID || after.Sessions[0].Status != before.Sessions[0].Status {
					t.Fatalf("read mutated state: %v %v", after, err)
				}
				sent, err := pb.NewSessionControlClient(f.conn).Send(ctx, &pb.SendRequest{Meta: controlMeta("approval-send"), Session: ref, Input: []*pb.InputPart{{Part: &pb.InputPart_Text{Text: &pb.TextInput{Text: "fixture"}}}}})
				if err != nil || sent.GetCompleted() == nil {
					t.Fatalf("send: %v %v", sent, err)
				}
				select {
				case params := <-turns:
					policy, reviewer, sandbox := "untrusted", "user", "workspaceWrite"
					if mode == "auto" {
						policy, reviewer = "on-request", "auto_review"
					}
					if mode == "full_access" {
						policy, sandbox = "never", "dangerFullAccess"
					}
					actualSandbox, _ := params["sandboxPolicy"].(map[string]any)
					if params["approvalPolicy"] != policy || params["approvalsReviewer"] != reviewer || actualSandbox["type"] != sandbox {
						t.Fatalf("mode %s not applied to turn/start: %v", mode, params)
					}
				case <-ctx.Done():
					t.Fatal("missing native turn/start")
				}
			})
		}
	}
}
