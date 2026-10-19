package sessions

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	pb "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestHandoffCompletionRequiresExactNativeSuccess(t *testing.T) {
	for _, tc := range []struct {
		name, kind string
		code       int
	}{
		{"hermes success", "hermes", 0}, {"hermes interrupted", "hermes-interrupted", 502}, {"codex success", "codex", 0}, {"interrupted turn", "interrupted", 502}, {"EOF is not completion", "eof", 503}, {"cancel", "cancel", 409},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := &mockDaemonServer{eventsCb: func(req *pb.SubscribeRequest, stream grpc.ServerStreamingServer[pb.EventStreamFrame]) error {
				if len(req.Cursors) != 1 || req.Cursors[0].Session.GetConnectionId() != "ssh-test" {
					t.Errorf("lost scoped replay: %v", req)
				}
				if tc.kind == "eof" {
					return nil
				}
				if tc.kind == "cancel" {
					<-stream.Context().Done()
					return stream.Context().Err()
				}
				payload := map[string]any{"command_id": "summary-command", "status": "complete"}
				eventType := "hermes.command_complete"
				if tc.kind == "hermes-interrupted" {
					payload["status"] = "interrupted"
				}
				if !strings.HasPrefix(tc.kind, "hermes") {
					turnStatus := "completed"
					if tc.kind == "interrupted" {
						turnStatus = "interrupted"
					}
					eventType = "codex.notification"
					payload = map[string]any{"frame": map[string]any{"method": "turn/completed", "params": map[string]any{"turn": map[string]any{"id": "turn", "status": turnStatus}}}}
				}
				native, _ := encodeNativeJSON("fixture", payload)
				// An event for another environment must never finish this operation.
				for _, conn := range []string{"other", "ssh-test"} {
					if err := stream.Send(&pb.EventStreamFrame{Frame: &pb.EventStreamFrame_Replay{Replay: &pb.EventEnvelope{DaemonId: "test-daemon", Session: buildSessionRef("source", "summary", conn), EventType: eventType, NativePayload: native}}}); err != nil {
						return err
					}
				}
				return nil
			}}
			h, _, _, close := setupTestServer(t, srv)
			defer close()
			ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
			defer cancel()
			err := h.awaitHandoffCompletion(ctx, buildSessionRef("source", "summary", "ssh-test"), &pb.CommandReceipt{TurnId: optString("turn")}, "summary-command")
			if tc.code == 0 {
				if err != nil {
					t.Fatal(err)
				}
			} else {
				se, ok := err.(statusError)
				if !ok || se.HTTPStatus() != tc.code {
					t.Fatalf("wrong outcome: %v", err)
				}
			}
		})
	}
}

func TestHandoffFirstSendRejectVersusUnknown(t *testing.T) {
	for _, tc := range []struct {
		name     string
		code     codes.Code
		consumed bool
	}{{"definite rejection", codes.FailedPrecondition, false}, {"unknown delivery", codes.Unavailable, true}} {
		t.Run(tc.name, func(t *testing.T) {
			sends := 0
			srv := &mockDaemonServer{sendCb: func(r *pb.SendRequest) (*pb.CommandReceipt, error) {
				sends++
				return nil, status.Error(tc.code, "fixture")
			}}
			h, st, _, close := setupTestServer(t, srv)
			defer close()
			ctx := context.Background()
			st.UpsertAgent(ctx, map[string]any{"id": "target", "kind": "hermes", "name": "target", "status": "ready"})
			st.UpsertSession(ctx, map[string]any{"id": "target-session", "agent_id": "target", "title": "work", "status": "idle", "handoff_context": "legacy summary", "updated_at": "2026-10-17T00:00:00Z"})
			body := `{"id":"once","agent_id":"target","session_id":"target-session","action":"send","text":"continue"}`
			first := handoffPost(h, "/api/v1/commands", body)
			if first.Code != http.StatusBadGateway {
				t.Fatalf("%d %s", first.Code, first.Body.String())
			}
			handoffPost(h, "/api/v1/commands", body)
			if sends != 1 {
				t.Fatal("duplicate send")
			}
			row, _ := st.GetSession(ctx, "target", "target-session")
			if row["handoff_context_consumed"] != tc.consumed {
				t.Fatalf("claim: %v", row)
			}
			if tc.consumed {
				next := handoffPost(h, "/api/v1/commands", `{"id":"next","agent_id":"target","session_id":"target-session","action":"send","text":"next"}`)
				if next.Code != 409 || sends != 1 {
					t.Fatalf("unknown handoff was unlocked: %d sends=%d", next.Code, sends)
				}
			}
		})
	}
}
