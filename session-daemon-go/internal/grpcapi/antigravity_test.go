package grpcapi_test

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	pb "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
	"astrorder.dev/session-daemon/internal/runtime/antigravity"
)

func serveMockGRPCACP(r io.Reader, w io.Writer, defaultID string) {
	var writeMu sync.Mutex
	send := func(v map[string]any) {
		raw, _ := json.Marshal(v)
		raw = append(raw, '\n')
		writeMu.Lock()
		_, _ = w.Write(raw)
		writeMu.Unlock()
	}

	curModel := "gemini-test"
	curMode := "default"
	curThought := "medium"

	scan := bufio.NewScanner(r)
	scan.Buffer(make([]byte, 65536), 4_000_000)
	for scan.Scan() {
		line := bytes.TrimSpace(scan.Bytes())
		if len(line) == 0 {
			continue
		}
		dec := json.NewDecoder(bytes.NewReader(line))
		dec.UseNumber()
		var msg map[string]any
		if dec.Decode(&msg) != nil {
			return
		}
		method, _ := msg["method"].(string)
		rawID := msg["id"]
		params, _ := msg["params"].(map[string]any)

		switch method {
		case "initialize":
			send(map[string]any{
				"jsonrpc": "2.0",
				"id":      rawID,
				"result": map[string]any{
					"protocolVersion": 1,
					"agentCapabilities": map[string]any{
						"loadSession":        true,
						"promptCapabilities": map[string]any{"image": true, "embeddedContext": true},
						"sessionCapabilities": map[string]any{
							"resume": map[string]any{},
							"close":  map[string]any{},
						},
					},
				},
			})
		case "session/new", "session/resume":
			sid, _ := params["sessionId"].(string)
			if sid == "" {
				sid = defaultID
			}
			send(map[string]any{
				"jsonrpc": "2.0",
				"method":  "session/update",
				"params": map[string]any{
					"sessionId": sid,
					"update": map[string]any{
						"sessionUpdate": "available_commands_update",
						"availableCommands": []map[string]any{
							{"name": "compact", "description": "Compact context"},
						},
					},
				},
			})
			send(map[string]any{
				"jsonrpc": "2.0",
				"id":      rawID,
				"result": map[string]any{
					"sessionId": sid,
					"models": map[string]any{
						"currentModelId": curModel,
						"availableModels": []map[string]any{
							{"modelId": "gemini-test", "name": "Test Gemini"},
							{"modelId": "claude-test", "name": "Test Claude"},
						},
					},
					"modes": map[string]any{
						"currentModeId": curMode,
					},
					"configOptions": []map[string]any{
						{
							"id":           "thought_level",
							"currentValue": curThought,
							"options":      []map[string]any{{"value": "low"}, {"value": "medium"}, {"value": "high"}},
						},
					},
				},
			})
		case "session/set_model":
			if m, ok := params["modelId"].(string); ok {
				curModel = m
			}
			send(map[string]any{"jsonrpc": "2.0", "id": rawID, "result": map[string]any{}})
		case "session/set_mode":
			if md, ok := params["modeId"].(string); ok {
				curMode = md
			}
			send(map[string]any{"jsonrpc": "2.0", "id": rawID, "result": map[string]any{}})
		case "session/set_config_option":
			if val, ok := params["value"].(string); ok {
				curThought = val
			}
			send(map[string]any{
				"jsonrpc": "2.0",
				"id":      rawID,
				"result": map[string]any{
					"configOptions": []map[string]any{
						{
							"id":           "thought_level",
							"currentValue": curThought,
							"options":      []map[string]any{{"value": "low"}, {"value": "medium"}, {"value": "high"}},
						},
					},
				},
			})
		case "session/close":
			send(map[string]any{"jsonrpc": "2.0", "id": rawID, "result": map[string]any{}})
		case "session/prompt":
			sid, _ := params["sessionId"].(string)
			send(map[string]any{
				"jsonrpc": "2.0",
				"method":  "session/update",
				"params": map[string]any{
					"sessionId": sid,
					"update": map[string]any{
						"sessionUpdate": "tool_call",
						"toolCallId":    fmt.Sprintf("call-%v", rawID),
						"name":          "run_command",
						"status":        "failed",
						"error":         "permission denied fixture",
					},
				},
			})
			send(map[string]any{
				"jsonrpc": "2.0",
				"method":  "session/update",
				"params": map[string]any{
					"sessionId": sid,
					"update": map[string]any{
						"sessionUpdate": "agent_message_chunk",
						"content":       map[string]any{"type": "text", "text": "fixture response"},
					},
				},
			})
			send(map[string]any{
				"jsonrpc": "2.0",
				"id":      rawID,
				"result":  map[string]any{"stopReason": "end_turn"},
			})
		}
	}
}

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
	serveMockGRPCACP(os.Stdin, os.Stdout, "11111111-1111-4111-8111-111111111111")
	os.Exit(0)
}

func TestAntigravityOfficialGRPCLifecycle(t *testing.T) {
	f := startFixture(t)
	exe, _ := os.Executable()
	dir := t.TempDir()
	aid := "local-antigravity"
	a := antigravity.New(antigravity.Config{
		Executable:  exe,
		Arguments:   []string{"-test.run=TestAntigravityGRPCHelper", "--"},
		Environment: []string{"ASTRORDER_AGY_GRPC_HELPER=1"},
		Workspace:   dir,
		Allowed:     []string{dir},
		Home:        filepath.Join(dir, "home"),
		AgentID:     aid,
		Emit:        f.daemon.Emit,
	})
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
	created, err := control.CreateSession(f.ctx, &pb.CreateSessionRequest{
		Meta:    controlMeta("agy-create"),
		Target:  target,
		Options: &pb.CreateSessionRequest_Antigravity{Antigravity: &pb.AntigravityCreateOptions{Cwd: dir}},
	})
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
	for index, mode := range []string{"auto", "full_access", "manual"} {
		changed, e := query.SetApprovalMode(f.ctx, &pb.SetApprovalModeRequest{Meta: controlMeta(fmt.Sprintf("agy-approval-%d", index)), Session: ref, Mode: mode})
		if e != nil || changed.GetCompleted() == nil || !changed.GetAccepted() {
			t.Fatalf("approval switch: %v %v", changed, e)
		}
		confirmed, e := query.ReadApprovalMode(f.ctx, &pb.ReadApprovalModeRequest{Session: ref})
		if e != nil || confirmed.GetMode() != mode {
			t.Fatalf("approval readback: %v %v", confirmed, e)
		}
	}

	// ListCommands over ACP must return availableCommands.
	cmds, err := query.ListCommands(f.ctx, &pb.ListCommandsRequest{Session: ref})
	if err != nil || len(cmds.GetItems()) != 1 || cmds.Items[0].GetName() != "compact" {
		t.Fatalf("ListCommands: %+v %v", cmds, err)
	}

	// External HTTP image URLs and skill inputs must be rejected before dispatch,
	// whereas valid base64 data-URI images are supported by official agy acp!
	for index, input := range []*pb.InputPart{
		{Part: &pb.InputPart_ImageUrl{ImageUrl: &pb.ImageUrlInput{Url: "https://invalid.test/image.png"}}},
		{Part: &pb.InputPart_Skill{Skill: &pb.SkillRefInput{Name: "fixture", Path: "fixture"}}},
	} {
		receipt, err := control.Send(f.ctx, &pb.SendRequest{
			Meta:    controlMeta(fmt.Sprintf("agy-reject-%d", index)),
			Session: ref,
			Input:   []*pb.InputPart{{Part: &pb.InputPart_Text{Text: &pb.TextInput{Text: "fixture text"}}}, input},
		})
		if err == nil && receipt.GetCompleted() != nil {
			t.Fatalf("unsupported input accepted: %v", receipt)
		}
		if a.Snapshot()[ref.SessionId].Status != "idle" {
			t.Fatal("rejection changed native activity")
		}
	}

	dataImg := "data:image/png;base64," + base64.StdEncoding.EncodeToString([]byte{0x89, 0x50, 0x4e, 0x47})
	for round := 0; round < 2; round++ {
		inputs := []*pb.InputPart{{Part: &pb.InputPart_Text{Text: &pb.TextInput{Text: "fixture text"}}}}
		if round == 1 {
			inputs = append(inputs, &pb.InputPart{Part: &pb.InputPart_ImageUrl{ImageUrl: &pb.ImageUrlInput{Url: dataImg}}})
		}
		receipt, err := control.Send(f.ctx, &pb.SendRequest{
			Meta:    controlMeta(fmt.Sprintf("agy-send-%d", round)),
			Session: ref,
			Input:   inputs,
		})
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
