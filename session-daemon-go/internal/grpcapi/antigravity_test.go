package grpcapi_test

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	pb "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
	"astrorder.dev/session-daemon/internal/runtime/antigravity"
)

func TestAntigravityGRPCHelper(t *testing.T) {
	if os.Getenv("ASTRORDER_AGY_GRPC_HELPER") != "1" {
		return
	}
	for _, arg := range os.Args {
		if arg == "models" {
			fmt.Println("gemini-test\tTest Gemini")
			os.Exit(0)
		}
	}
	id := "11111111-1111-4111-8111-111111111111"
	model := "gemini-test"
	for i, arg := range os.Args {
		if arg == "--model" && i+1 < len(os.Args) {
			model = os.Args[i+1]
		}
	}
	permission := "request-review"
	for _, arg := range os.Args {
		if arg == "--dangerously-skip-permissions" {
			permission = "always-proceed"
		}
	}
	fmt.Printf("{\"event\":\"init\",\"conversation_id\":%q,\"init\":{\"model\":%q,\"permission_mode\":%q}}\n", id, model, permission)
	scan := bufio.NewScanner(os.Stdin)
	index := 0
	for scan.Scan() {
		var input map[string]any
		if json.Unmarshal(scan.Bytes(), &input) != nil || input["event"] != "user" {
			os.Exit(4)
		}
		message, ok := input["message"].(map[string]any)
		if !ok || message["content"] != "fixture text" {
			os.Exit(5)
		}
		index++
		fmt.Printf("{\"event\":\"step_update\",\"step_update\":{\"conversation_id\":%q,\"step_index\":%d,\"state\":\"ERROR\",\"step_type\":\"tool\",\"tool_name\":\"run_command\",\"tool_info\":{\"error\":{\"message\":\"permission denied fixture\"}}}}\n", id, index*2)
		fmt.Printf("{\"event\":\"step_update\",\"step_update\":{\"conversation_id\":%q,\"step_index\":%d,\"state\":\"DONE\",\"step_type\":\"agent_response\",\"text_delta\":\"fixture response\"}}\n", id, index*2+1)
		fmt.Printf("{\"event\":\"result\",\"result\":{\"conversation_id\":%q,\"status\":\"SUCCESS\"}}\n", id)
	}
	os.Exit(0)
}

func TestAntigravityOfficialGRPCLifecycle(t *testing.T) {
	f := startFixture(t)
	exe, _ := os.Executable()
	dir := t.TempDir()
	aid := "local-antigravity"
	a := antigravity.New(antigravity.Config{Executable: exe, Arguments: []string{"-test.run=TestAntigravityGRPCHelper", "--"}, Environment: []string{"ASTRORDER_AGY_GRPC_HELPER=1"}, Workspace: dir, Allowed: []string{dir}, Home: filepath.Join(dir, "home"), AgentID: aid, Emit: f.daemon.Emit})
	defer a.Close()
	f.daemon.RegisterRuntime("antigravity", a)
	control := pb.NewSessionControlClient(f.conn)
	query := pb.NewRuntimeQueryClient(f.conn)
	target := &pb.RuntimeTarget{Runtime: pb.RuntimeKind_RUNTIME_KIND_ANTIGRAVITY, AgentId: &aid}
	probe := &pb.SessionRef{SessionId: "probe-antigravity-local", AgentId: &aid}
	if _, err := control.AttachSession(f.ctx, &pb.AttachSessionRequest{Target: target, Session: probe, RuntimeControl: true}); err != nil {
		t.Fatal(err)
	}
	models, err := query.ListModels(f.ctx, &pb.ListModelsRequest{Session: probe})
	if err != nil || len(models.GetItems()) != 1 || models.Items[0].GetLabel() != "Test Gemini" || len(models.Items[0].SupportedEfforts) != 0 {
		t.Fatalf("models: %v %v", models, err)
	}
	if _, err = query.QueryNative(f.ctx, &pb.NativeQueryRequest{Target: target, Query: &pb.NativeQueryRequest_AntigravityModelsList{AntigravityModelsList: &pb.AntigravityModelsListQuery{}}}); err != nil {
		t.Fatal(err)
	}
	created, err := control.CreateSession(f.ctx, &pb.CreateSessionRequest{Meta: controlMeta("agy-create"), Target: target, Options: &pb.CreateSessionRequest_Antigravity{Antigravity: &pb.AntigravityCreateOptions{Cwd: dir}}})
	if err != nil || created.GetCompleted() == nil || created.GetSession().GetAgentId() != aid {
		t.Fatalf("create: %v %v", created, err)
	}
	ref := created.Session
	switched, err := query.SetModel(f.ctx, &pb.SetModelRequest{Meta: controlMeta("agy-model"), Session: ref, Provider: "antigravity", Model: "gemini-test"})
	if err != nil || !switched.GetAccepted() {
		t.Fatalf("switch: %v %v", switched, err)
	}
	readback, err := query.ReadModel(f.ctx, &pb.ReadModelRequest{Session: ref})
	if err != nil || readback.GetModel() != "gemini-test" {
		t.Fatalf("model readback: %v %v", readback, err)
	}
	initialApproval, err := query.ReadApprovalMode(f.ctx, &pb.ReadApprovalModeRequest{Session: ref})
	if err != nil || initialApproval.GetMode() != "manual" {
		t.Fatalf("native approval: %v %v", initialApproval, err)
	}
	for index, mode := range []string{"full_access", "manual"} {
		changed, e := query.SetApprovalMode(f.ctx, &pb.SetApprovalModeRequest{Meta: controlMeta(fmt.Sprintf("agy-approval-%d", index)), Session: ref, Mode: mode})
		if e != nil || changed.GetCompleted() == nil || !changed.GetAccepted() {
			t.Fatalf("approval switch: %v %v", changed, e)
		}
		confirmed, e := query.ReadApprovalMode(f.ctx, &pb.ReadApprovalModeRequest{Session: ref})
		if e != nil || confirmed.GetMode() != mode {
			t.Fatalf("approval readback: %v %v", confirmed, e)
		}
	}
	// Non-text input must be rejected before dispatch, including image/skill types
	// that the shared gRPC path previously removed for non-Codex runtimes.
	for index, input := range []*pb.InputPart{
		{Part: &pb.InputPart_ImageUrl{ImageUrl: &pb.ImageUrlInput{Url: "https://invalid.test/image.png"}}},
		{Part: &pb.InputPart_Skill{Skill: &pb.SkillRefInput{Name: "fixture", Path: "fixture"}}},
	} {
		receipt, err := control.Send(f.ctx, &pb.SendRequest{Meta: controlMeta(fmt.Sprintf("agy-reject-%d", index)), Session: ref, Input: []*pb.InputPart{{Part: &pb.InputPart_Text{Text: &pb.TextInput{Text: "fixture text"}}}, input}})
		if err == nil && receipt.GetCompleted() != nil {
			t.Fatalf("non-text input accepted: %v", receipt)
		}
		if a.Snapshot()[ref.SessionId].Status != "idle" {
			t.Fatal("rejection changed native activity")
		}
	}
	for round := 0; round < 2; round++ {
		receipt, err := control.Send(f.ctx, &pb.SendRequest{Meta: controlMeta(fmt.Sprintf("agy-send-%d", round)), Session: ref, Input: []*pb.InputPart{{Part: &pb.InputPart_Text{Text: &pb.TextInput{Text: "fixture text"}}}}})
		if err != nil || receipt.GetCompleted() == nil || !receipt.GetAccepted() {
			t.Fatalf("send: %v %v", receipt, err)
		}
		deadline := time.Now().Add(3 * time.Second)
		for a.Snapshot()[ref.SessionId].Status == "running" && time.Now().Before(deadline) {
			time.Sleep(time.Millisecond)
		}
		if a.Snapshot()[ref.SessionId].Status != "idle" {
			t.Fatal("native result did not confirm idle")
		}
	}
	frames, err := f.daemon.ReplayPage(ref.SessionId, 0, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	responses := 0
	for _, frame := range frames.Frames {
		if frame.Event == "connector.event" && frame.Payload["type"] == "message.upsert" {
			responses++
		}
	}
	if responses != 4 {
		t.Fatalf("live round trips: %d", responses)
	}
	if _, err := control.CloseSession(f.ctx, &pb.CloseSessionRequest{Meta: controlMeta("agy-close"), Session: ref}); err != nil {
		t.Fatal(err)
	}
}
