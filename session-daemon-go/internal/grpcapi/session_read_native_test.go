package grpcapi_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"

	"testing"
	"time"

	sessiondv1 "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
	"astrorder.dev/session-daemon/internal/runtime/hermes"
)

// Synthetic Hermes gateway, exercised through the production adapter and TCP.
func TestSessionReadActualHermesAdapterOverTCP(t *testing.T) {
	if os.Getenv("ASTRORDER_SESSION_READ_HELPER") == "1" {
		encoder := json.NewEncoder(os.Stdout)
		_ = encoder.Encode(map[string]any{"method": "event", "params": map[string]any{"type": "gateway.ready"}})
		decoder := json.NewDecoder(os.Stdin)
		decoder.UseNumber()
		for {
			var request map[string]any
			if decoder.Decode(&request) != nil {
				return
			}
			method := request["method"]
			params, _ := request["params"].(map[string]any)
			var result any
			switch method {
			case "session.create":
				result = map[string]any{"session_id": "native-live-handle", "stored_session_id": "native-durable", "info": map[string]any{"cwd": os.Getenv("ASTRORDER_SESSION_READ_ROOT")}}
			case "session.resume":
				if params["omit_messages"] != true {
					_ = encoder.Encode(map[string]any{"id": request["id"], "error": map[string]any{"code": 4000, "message": "read resumed messages"}})
					continue
				}
				result = map[string]any{"session_id": "native-live-handle", "info": map[string]any{"cwd": os.Getenv("ASTRORDER_SESSION_READ_ROOT"), "provider": "fixture", "model": "native-model"}}
			case "model.options":
				result = map[string]any{"providers": []any{map[string]any{"slug": "fixture", "authenticated": true, "models": []any{"native-model"}}}}
			case "commands.catalog":
				result = map[string]any{"pairs": []any{[]any{"/plan", "fixture command"}}, "commands": map[string]any{"/plan": map[string]any{"argument_mode": "text"}}}
			case "config.get":
				result = map[string]any{"value": "high"}
			case "session.history":
				result = map[string]any{"messages": []any{map[string]any{"id": "older", "role": "user", "content": "fixture"}, map[string]any{"id": "middle", "role": "assistant", "content": "fixture"}, map[string]any{"id": json.Number("9007199254740993"), "role": "assistant", "content": "synthetic"}}}
			default:
				_ = encoder.Encode(map[string]any{"id": request["id"], "error": map[string]any{"code": 4000, "message": "unexpected native action"}})
				continue
			}
			if encoder.Encode(map[string]any{"id": request["id"], "result": result}) != nil {
				return
			}
		}
	}
	f := startFixture(t)
	root := t.TempDir()
	adapter := hermes.New(hermes.Config{Executable: os.Args[0], Arguments: []string{"-test.run=^TestSessionReadActualHermesAdapterOverTCP$"}, Environment: []string{"ASTRORDER_SESSION_READ_HELPER=1", "ASTRORDER_SESSION_READ_ROOT=" + root}, Workspace: root, Allowed: []string{root}, AgentID: "native-fixture", StateDBPath: filepath.Join(root, "absent-state.db"), Emit: f.daemon.Emit})
	f.daemon.RegisterRuntime("hermes", adapter)
	ctx, cancel := context.WithTimeout(f.ctx, 5*time.Second)
	defer cancel()
	legacyWSOperation(t, f.daemon, map[string]any{"action": "session.create", "agent_type": "hermes", "cwd": root})
	client := sessiondv1.NewRuntimeQueryClient(f.conn)
	session := &sessiondv1.SessionRef{SessionId: "native-durable", AgentId: ptr("native-fixture")}
	before, err := f.daemon.AuthorityStatus()
	if err != nil {
		t.Fatal(err)
	}
	models, err := client.ListModels(ctx, &sessiondv1.ListModelsRequest{Session: session})
	if err != nil || len(models.GetItems()) != 1 {
		t.Fatalf("native models: %+v %v", models, err)
	}
	commands, err := client.ListCommands(ctx, &sessiondv1.ListCommandsRequest{Session: session})
	if err != nil || len(commands.GetItems()) != 1 || commands.Items[0].Name != "plan" {
		t.Fatalf("native commands: %+v %v", commands, err)
	}
	model, err := client.ReadModel(ctx, &sessiondv1.ReadModelRequest{Session: session})
	if err != nil || model.GetModel() != "native-model" || model.GetEffort() != "high" {
		t.Fatalf("native model state: %+v %v", model, err)
	}
	page, err := client.GetHistoryPage(ctx, &sessiondv1.HistoryPageRequest{Session: session, Limit: 1})
	if err != nil || len(page.GetItems()) != 1 {
		t.Fatalf("native history: %+v %v", page, err)
	}
	if string(page.Items[0].Utf8Json) != `{"content":"synthetic","id":9007199254740993,"role":"assistant"}` {
		t.Fatalf("native integer changed: %s", page.Items[0].Utf8Json)
	}
	if page.NextCursor == nil || page.NextCursor.GetValue() == "" {
		t.Fatal("RPC history did not page")
	}
	page, err = client.GetHistoryPage(ctx, &sessiondv1.HistoryPageRequest{Session: session, Limit: 1, Before: page.NextCursor})
	if err != nil || len(page.GetItems()) != 1 || string(page.Items[0].Utf8Json) != `{"content":"fixture","id":"middle","role":"assistant"}` {
		t.Fatalf("RPC history continuation: %+v %v", page, err)
	}
	after, err := f.daemon.AuthorityStatus()
	if err != nil || after.Sessions[0].MaxSeqID != before.Sessions[0].MaxSeqID {
		t.Fatalf("read wrote journal: %+v %v", after, err)
	}
}
