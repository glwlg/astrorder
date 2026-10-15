package grpcapi_test

import (
	"context"
	"encoding/json"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	pb "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
	"astrorder.dev/session-daemon/internal/runtime/codex"
	"astrorder.dev/session-daemon/internal/transport/ssh"
)

func TestCodexGuidanceOverOfficialGRPCIsDurableAndTurnScoped(t *testing.T) {
	for _, kind := range []string{"codex", "codex-ssh"} {
		t.Run(kind, func(t *testing.T) {
			f := startFixture(t)
			transport := newGRPCControlTransport()
			defer transport.Close()
			var starts, steers atomic.Int32
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
					case "thread/start":
						result["thread"] = map[string]any{"id": "guidance-thread"}
					case "turn/start":
						starts.Add(1)
						result["turn"] = map[string]any{"id": "guidance-turn"}
					case "turn/steer":
						steers.Add(1)
						params := request["params"].(map[string]any)
						if params["threadId"] != "guidance-thread" || params["expectedTurnId"] != "guidance-turn" {
							t.Errorf("guidance crossed turn identity: %+v", params)
						}
						result["turnId"] = "guidance-turn"
					default:
						t.Errorf("unexpected native method: %v", request["method"])
					}
					if encoder.Encode(map[string]any{"id": request["id"], "result": result}) != nil {
						return
					}
				}
			}()
			adapter := codex.NewSSHAdapter(codex.Config{Executable: "/synthetic/codex", Workspace: "/guidance", Allowed: []string{"/guidance"}, AgentID: "guidance-agent"}, f.daemon.Emit, func(context.Context, ssh.CommandSpec) (codex.SSHSession, error) { return transport, nil })
			defer adapter.Close()
			f.daemon.RegisterRuntime(kind, adapter)
			legacyWSOperation(t, f.daemon, map[string]any{"action": "session.create", "agent_type": kind, "cwd": "/guidance"})
			ctx, cancel := context.WithTimeout(f.ctx, 5*time.Second)
			defer cancel()
			client := pb.NewSessionControlClient(f.conn)
			ref := &pb.SessionRef{SessionId: "guidance-thread", AgentId: ptr("guidance-agent")}
			input := []*pb.InputPart{{Part: &pb.InputPart_Text{Text: &pb.TextInput{Text: "fixture"}}}}
			first, err := client.Send(ctx, &pb.SendRequest{Meta: controlMeta("original"), Session: ref, Input: input})
			if err != nil || !first.GetAccepted() || first.GetTurnId() != "guidance-turn" {
				t.Fatalf("initial turn: %v %v", first, err)
			}
			request := &pb.SendRequest{Meta: controlMeta("guidance"), Session: ref, Input: input}
			for i := 0; i < 2; i++ {
				receipt, err := client.Send(ctx, request)
				if err != nil || !receipt.GetAccepted() || receipt.GetTurnId() != "guidance-turn" || receipt.GetStatus() != pb.SessionStatus_SESSION_STATUS_RUNNING {
					t.Fatalf("active owned Send: %v %v", receipt, err)
				}
				var native map[string]any
				if err := json.Unmarshal(receipt.GetCompleted().GetNative().GetUtf8Json(), &native); err != nil || native["completed"] != true {
					t.Fatalf("guidance lacks durable delivery confirmation: %v %v", native, err)
				}
			}
			stream, err := pb.NewEventStreamClient(f.conn).Subscribe(ctx, &pb.SubscribeRequest{ReplayPageSize: 10})
			if err != nil {
				t.Fatal(err)
			}
			deliveries := 0
			for {
				frame, err := stream.Recv()
				if err != nil {
					t.Fatal(err)
				}
				if frame.GetLiveReady() != nil {
					break
				}
				if event := frame.GetReplay(); event != nil && event.EventType == "codex.command_complete" {
					deliveries++
					if event.Session.SessionId != ref.SessionId || !strings.Contains(string(event.GetNative().GetUtf8Json()), `"command_id":"guidance"`) {
						t.Fatalf("WAL delivery lost guidance identity: %v", event)
					}
				}
			}
			if deliveries != 1 {
				t.Fatalf("guidance produced %d WAL deliveries", deliveries)
			}
			if starts.Load() != 1 || steers.Load() != 1 {
				t.Fatalf("replayed guidance redispatched or started another turn: starts=%d steers=%d", starts.Load(), steers.Load())
			}
		})
	}
}
