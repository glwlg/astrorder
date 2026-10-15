package grpcapi_test

import (
	pb "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
	core "astrorder.dev/session-daemon/internal/runtime"
	"context"
	"encoding/json"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"testing"
)

type goalRuntimeFixture struct{ controlRuntimeFixture }

func (a *goalRuntimeFixture) Command(ctx context.Context, r core.Request) (core.Result, error) {
	result, err := a.controlRuntimeFixture.Command(ctx, r)
	if r.Action == "session.goal.set" {
		result.Payload["goal"] = map[string]any{"title": r.Fields["objective"], "status": "active"}
	}
	if r.Action == "session.goal.clear" {
		result.Payload["goal"] = nil
	}
	return result, err
}
func TestGoalControlAuthenticatedDurableAndOwnerScoped(t *testing.T) {
	f := startFixture(t)
	a := &goalRuntimeFixture{}
	f.daemon.RegisterRuntime("hermes", a)
	sessionClient := pb.NewSessionControlClient(f.conn)
	if _, err := sessionClient.CreateSession(f.ctx, &pb.CreateSessionRequest{Meta: controlMeta("goal-create"), Target: &pb.RuntimeTarget{Runtime: pb.RuntimeKind_RUNTIME_KIND_HERMES}, Options: &pb.CreateSessionRequest_Hermes{Hermes: &pb.HermesCreateOptions{}}}); err != nil {
		t.Fatal(err)
	}
	client := pb.NewRuntimeQueryClient(f.conn)
	ref := &pb.SessionRef{SessionId: "control-owned", AgentId: ptr("fixture")}
	req := &pb.SetGoalRequest{Meta: controlMeta("goal-set"), Session: ref, Objective: "new objective"}
	if _, err := client.SetGoal(context.Background(), req); status.Code(err) != codes.Unauthenticated {
		t.Fatal("auth", err)
	}
	for range 2 {
		receipt, err := client.SetGoal(f.ctx, req)
		if err != nil || receipt.OperationState != pb.OperationState_OPERATION_STATE_COMPLETED {
			t.Fatal("set", receipt, err)
		}
		var native map[string]any
		if err = json.Unmarshal(receipt.GetCompleted().Native.Utf8Json, &native); err != nil || native["goal"].(map[string]any)["title"] != "new objective" {
			t.Fatal("native readback", native, err)
		}
	}
	req.Objective = "changed"
	if _, err := client.SetGoal(f.ctx, req); status.Code(err) != codes.AlreadyExists {
		t.Fatal("payload conflict", err)
	}
	if _, err := client.ClearGoal(f.ctx, &pb.ClearGoalRequest{Meta: controlMeta("goal-foreign"), Session: &pb.SessionRef{SessionId: "control-owned", AgentId: ptr("foreign")}}); status.Code(err) != codes.FailedPrecondition {
		t.Fatal("foreign owner", err)
	}
	for range 2 {
		receipt, err := client.ClearGoal(f.ctx, &pb.ClearGoalRequest{Meta: controlMeta("goal-clear"), Session: ref})
		if err != nil || receipt.GetCompleted() == nil {
			t.Fatal("clear", receipt, err)
		}
	}
	for _, mode := range []string{"paused", "active"} {
		receipt, err := client.SetGoal(f.ctx, &pb.SetGoalRequest{Meta: controlMeta("goal-" + mode), Session: ref, Status: mode})
		if err != nil || receipt.GetCompleted() == nil {
			t.Fatal("lifecycle", mode, receipt, err)
		}
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	sets, clears := 0, 0
	for _, r := range a.requests {
		if r.Action == "session.goal.set" {
			if r.Fields["status"] == "paused" || r.Fields["status"] == "active" {
				continue
			}
			sets++
		}
		if r.Action == "session.goal.clear" {
			clears++
		}
	}
	if sets != 1 || clears != 1 {
		t.Fatal("duplicate native mutations", sets, clears)
	}
	if _, err := client.SetGoal(f.ctx, &pb.SetGoalRequest{Meta: controlMeta("goal-empty"), Session: ref, Objective: "  "}); status.Code(err) != codes.InvalidArgument {
		t.Fatal("empty", err)
	}
}
