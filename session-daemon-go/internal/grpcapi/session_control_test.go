package grpcapi_test

import (
	"context"
	"encoding/json"
	"sync"
	"testing"

	pb "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
	core "astrorder.dev/session-daemon/internal/runtime"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type controlRuntimeFixture struct {
	mu       sync.Mutex
	requests []core.Request
	created  int
}

func (a *controlRuntimeFixture) Create(_ context.Context, r core.Request) (core.Result, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.created++
	a.requests = append(a.requests, r)
	return core.Result{SessionID: "control-owned", Status: "idle", Payload: map[string]any{"agent_id": "fixture", "created": true}}, nil
}
func (a *controlRuntimeFixture) Spawn(ctx context.Context, r core.Request) (core.Result, error) {
	return core.Result{SessionID: r.SessionID, Status: "idle", Payload: map[string]any{"agent_id": "fixture"}}, nil
}
func (a *controlRuntimeFixture) Command(_ context.Context, r core.Request) (core.Result, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.requests = append(a.requests, r)
	payload := map[string]any{"accepted": true}
	state := "idle"
	if r.Action == "session.send" {
		payload["turn_id"] = "native-turn"
		state = "running"
	}
	if r.Action == "session.delete" {
		payload["deleted"] = r.SessionID
	}
	if r.Action == "session.model.set" {
		payload["model"] = r.Fields["model"]
		payload["provider"] = r.Fields["provider"]
	}
	return core.Result{SessionID: r.SessionID, Status: state, Payload: payload}, nil
}
func controlMeta(id string) *pb.CommandMeta {
	return &pb.CommandMeta{OperationId: id, CommandId: ptr(id), Canonicalization: pb.CanonicalizationVersion_CANONICALIZATION_VERSION_LEGACY_JSON_V1}
}

func TestSessionControlOfficialTCPAndDurableReceipts(t *testing.T) {
	f := startFixture(t)
	a := &controlRuntimeFixture{}
	f.daemon.RegisterRuntime("hermes", a)
	client := pb.NewSessionControlClient(f.conn)
	create := &pb.CreateSessionRequest{Meta: controlMeta("create-one"), Target: &pb.RuntimeTarget{Runtime: pb.RuntimeKind_RUNTIME_KIND_HERMES}, Options: &pb.CreateSessionRequest_Hermes{Hermes: &pb.HermesCreateOptions{}}}
	if _, err := client.CreateSession(context.Background(), create); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("auth: %v", err)
	}
	receipt, err := client.CreateSession(f.ctx, create)
	if err != nil || receipt.Session.SessionId != "control-owned" || receipt.OperationState != pb.OperationState_OPERATION_STATE_COMPLETED {
		t.Fatalf("create: %v %v", receipt, err)
	}
	if receipt.Session.GetAgentId() != "fixture" {
		t.Fatalf("create omitted authoritative owner: %+v", receipt.Session)
	}
	if !receipt.Accepted || receipt.GetCompleted() == nil || !receipt.GetCompleted().Accepted {
		t.Fatalf("create receipt accepted missing or false: receipt.Accepted=%v, completed=%+v", receipt.Accepted, receipt.GetCompleted())
	}
	if _, err = client.CreateSession(f.ctx, create); err != nil {
		t.Fatal(err)
	}
	if a.created != 1 {
		t.Fatalf("duplicate create=%d", a.created)
	}
	ref := &pb.SessionRef{SessionId: "control-owned", AgentId: ptr("fixture")}
	send := &pb.SendRequest{Meta: controlMeta("send-one"), Session: ref, Input: []*pb.InputPart{{Part: &pb.InputPart_Text{Text: &pb.TextInput{Text: "synthetic payload"}}}}}
	receipt, err = client.Send(f.ctx, send)
	if err != nil || receipt.TurnId == nil || *receipt.TurnId != "native-turn" {
		t.Fatalf("send: %v %v", receipt, err)
	}
	if _, err = client.Send(f.ctx, send); err != nil {
		t.Fatal(err)
	}
	send.Input[0].GetText().Text = "changed"
	if _, err = client.Send(f.ctx, send); status.Code(err) != codes.AlreadyExists && status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("changed payload accepted: %v", err)
	}
	foreign := &pb.RenameSessionRequest{Meta: controlMeta("foreign"), Session: &pb.SessionRef{SessionId: "control-owned", AgentId: ptr("other")}, Title: "new"}
	if _, err = client.RenameSession(f.ctx, foreign); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("foreign: %v", err)
	}
	model := &pb.SetModelRequest{Meta: controlMeta("model-one"), Session: ref, Provider: "fixture", Model: "literal/model"}
	receipt, err = pb.NewRuntimeQueryClient(f.conn).SetModel(f.ctx, model)
	if err != nil || receipt.GetCompleted().GetModel() != "literal/model" {
		t.Fatalf("model: %v %v", receipt, err)
	}
	var native map[string]any
	json.Unmarshal(receipt.GetCompleted().Native.Utf8Json, &native)
	if native["provider"] != "fixture" {
		t.Fatal("native missing")
	}
	modelEmptyProvider := &pb.SetModelRequest{Meta: controlMeta("model-two"), Session: ref, Provider: "", Model: "literal/codex-model"}
	receipt, err = pb.NewRuntimeQueryClient(f.conn).SetModel(f.ctx, modelEmptyProvider)
	if err != nil || receipt.GetCompleted().GetModel() != "literal/codex-model" {
		t.Fatalf("model with empty provider: %v %v", receipt, err)
	}
	deleted := &pb.DeleteSessionRequest{Meta: controlMeta("delete-one"), Session: ref}
	if _, err = client.DeleteSession(f.ctx, deleted); err != nil {
		t.Fatal(err)
	}
	if _, err = client.DeleteSession(f.ctx, deleted); err != nil {
		t.Fatalf("delete retry after unbind: %v", err)
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	sends := 0
	deletes := 0
	for _, request := range a.requests {
		if request.Action == "session.send" {
			sends++
		}
		if request.Action == "session.delete" {
			deletes++
		}
	}
	if sends != 1 || deletes != 1 {
		t.Fatalf("redispatched sends=%d deletes=%d", sends, deletes)
	}
}

func TestSessionControlMatchesLegacyCommandOnlyPayload(t *testing.T) {
	f := startFixture(t)
	a := &controlRuntimeFixture{}
	f.daemon.RegisterRuntime("hermes", a)
	legacyWSOperation(t, f.daemon, map[string]any{"action": "session.create", "agent_type": "hermes"})
	legacyWSOperation(t, f.daemon, map[string]any{"action": "session.rename", "session_id": "control-owned", "command_id": "legacy-rename", "title": "literal title"})
	_, err := pb.NewSessionControlClient(f.conn).RenameSession(f.ctx, &pb.RenameSessionRequest{Meta: controlMeta("legacy-rename"), Session: &pb.SessionRef{SessionId: "control-owned"}, Title: "literal title"})
	if err != nil {
		t.Fatal(err)
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.requests) != 2 {
		t.Fatalf("legacy receipt redispatched: %d", len(a.requests))
	}
}
