package grpcapi_test

import (
	pb "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
	"astrorder.dev/session-daemon/internal/runtime/codex"
	"astrorder.dev/session-daemon/internal/transport/ssh"
	"context"
	"encoding/json"
	"testing"
	"time"
)

func TestCodexUserInputOverOfficialGRPC(t *testing.T) {
	for _, kind := range []string{"codex", "codex-ssh"} {
		t.Run(kind, func(t *testing.T) {
			f := startFixture(t)
			transport := newGRPCControlTransport()
			defer transport.Close()
			replies := make(chan map[string]any, 4)
			go func() {
				decoder, encoder := json.NewDecoder(transport.stdinR), json.NewEncoder(transport.stdoutW)
				decoder.UseNumber()
				for {
					var request map[string]any
					if decoder.Decode(&request) != nil {
						return
					}
					if request["method"] == nil {
						replies <- request
						_ = encoder.Encode(map[string]any{"method": "serverRequest/resolved", "params": map[string]any{"threadId": "question-thread", "requestId": request["id"]}})
						continue
					}
					if request["method"] == "initialized" {
						continue
					}
					result := map[string]any{}
					switch request["method"] {
					case "thread/start":
						result["thread"] = map[string]any{"id": "question-thread"}
					case "turn/start":
						result["turn"] = map[string]any{"id": "question-turn"}
					}
					if encoder.Encode(map[string]any{"id": request["id"], "result": result}) != nil {
						return
					}
					if request["method"] == "turn/start" {
						_ = encoder.Encode(map[string]any{"id": json.Number("9007199254740993"), "method": "item/tool/requestUserInput", "params": map[string]any{"threadId": "question-thread", "turnId": "question-turn", "itemId": "q-item", "isBlocking": true, "questions": []any{map[string]any{"id": "name", "header": "姓名", "question": "叫什么名字？"}}}})
					}
				}
			}()
			adapter := codex.NewSSHAdapter(codex.Config{Executable: "/synthetic/codex", Workspace: "/questions", Allowed: []string{"/questions"}, AgentID: "question-agent"}, f.daemon.Emit, func(context.Context, ssh.CommandSpec) (codex.SSHSession, error) { return transport, nil })
			defer adapter.Close()
			f.daemon.RegisterRuntime(kind, adapter)
			legacyWSOperation(t, f.daemon, map[string]any{"action": "session.create", "agent_type": kind, "cwd": "/questions"})
			ctx, cancel := context.WithTimeout(f.ctx, 8*time.Second)
			defer cancel()
			client := pb.NewSessionControlClient(f.conn)
			ref := &pb.SessionRef{SessionId: "question-thread", AgentId: ptr("question-agent")}
			if _, err := client.Send(ctx, &pb.SendRequest{Meta: controlMeta("question-send"), Session: ref, Input: []*pb.InputPart{{Part: &pb.InputPart_Text{Text: &pb.TextInput{Text: "ask"}}}}}); err != nil {
				t.Fatal(err)
			}
			// Await the emitted request so the test cannot race the native reader.
			stream, err := pb.NewEventStreamClient(f.conn).Subscribe(ctx, &pb.SubscribeRequest{ReplayPageSize: 20})
			if err != nil {
				t.Fatal(err)
			}
			for {
				frame, err := stream.Recv()
				if err != nil {
					t.Fatal(err)
				}
				event := frame.GetReplay()
				if event == nil {
					event = frame.GetLive()
				}
				if event != nil && event.EventType == "codex.notification" {
					var payload map[string]any
					_ = json.Unmarshal(event.GetNative().GetUtf8Json(), &payload)
					if payload["approval_id"] == "codex:9007199254740993" {
						break
					}
				}
			}
			request := &pb.RespondApprovalRequest{Meta: controlMeta("question-answer"), Session: ref, ApprovalId: &pb.ExactIdentifier{Value: &pb.ExactIdentifier_Text{Text: "codex:9007199254740993"}}, Decision: pb.ApprovalDecision_APPROVAL_DECISION_ACCEPT, UserInput: &pb.NativeJson{Utf8Json: []byte(`{"answers":{"name":{"answers":["张三"]}}}`)}}
			for i := 0; i < 2; i++ {
				receipt, err := client.RespondApproval(ctx, request)
				if err != nil || !receipt.GetAccepted() {
					t.Fatalf("answer: %v %v", receipt, err)
				}
			}
			request.UserInput.Utf8Json = []byte(`{"answers":{"name":{"answers":["different"]}}}`)
			if receipt, err := client.RespondApproval(ctx, request); err == nil && receipt.GetAccepted() {
				t.Fatal("operation identity reused with different answer")
			}
			select {
			case reply := <-replies:
				if reply["id"] != json.Number("9007199254740993") || reply["result"].(map[string]any)["answers"] == nil {
					t.Fatal(reply)
				}
			case <-ctx.Done():
				t.Fatal("no native answer")
			}
			select {
			case reply := <-replies:
				t.Fatalf("duplicate native answer: %v", reply)
			default:
			}
		})
	}
}
