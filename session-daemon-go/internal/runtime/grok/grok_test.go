package grok_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"astrorder.dev/session-daemon/internal/process"
	core "astrorder.dev/session-daemon/internal/runtime"
	"astrorder.dev/session-daemon/internal/runtime/grok"
)

func TestFakeACP_CreateAndSpawn(t *testing.T) {
	if os.Getenv("TEST_MODE") == "fake_acp_basic" {
		runFakeACPBasic()
		return
	}

	root := t.TempDir()
	var emittedEvents []string
	var ownerPID int
	var ownerStatus string
	var emitMu sync.Mutex

	adapter := grok.New(grok.Config{
		Executable: os.Args[0],
		Arguments:  []string{"-test.run=TestFakeACP_CreateAndSpawn"},
		Workspace:  root,
		Allowed:    []string{root},
		AgentID:    "local-grok",
		Emit: func(sessionID, event string, payload map[string]any, status string) error {
			emitMu.Lock()
			emittedEvents = append(emittedEvents, event)
			if event == "runtime.owner" {
				ownerPID, _ = payload["owner_pid"].(int)
				ownerStatus = "recorded"
			}
			emitMu.Unlock()
			return nil
		},
	})
	defer adapter.Close()

	os.Setenv("TEST_MODE", "fake_acp_basic")
	defer os.Unsetenv("TEST_MODE")

	// Create
	created, err := adapter.Create(context.Background(), core.Request{
		Fields: map[string]any{"cwd": root},
	})
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	if created.SessionID != "s-fake-1" || created.Status != "idle" {
		t.Fatalf("unexpected create result: %+v", created)
	}
	emitMu.Lock()
	if ownerStatus != "recorded" || !process.Alive(ownerPID) {
		emitMu.Unlock()
		t.Fatalf("local owner was not published: pid=%d status=%q", ownerPID, ownerStatus)
	}
	emitMu.Unlock()

	// Snapshot
	snap := adapter.Snapshot()
	if snap["s-fake-1"].Status != "idle" {
		t.Fatalf("unexpected snapshot: %+v", snap)
	}

	// Spawn existing returns existing
	spawned, err := adapter.Spawn(context.Background(), core.Request{
		SessionID: "s-fake-1",
		Fields:    map[string]any{"cwd": root},
	})
	if err != nil {
		t.Fatalf("Spawn existing failed: %v", err)
	}
	if spawned.SessionID != "s-fake-1" {
		t.Fatalf("unexpected spawn result: %+v", spawned)
	}
}

func runFakeACPBasic() {
	dec := json.NewDecoder(os.Stdin)
	enc := json.NewEncoder(os.Stdout)
	for {
		var req map[string]any
		if err := dec.Decode(&req); err != nil {
			return
		}
		id := req["id"]
		method, _ := req["method"].(string)

		switch method {
		case "initialize":
			_ = enc.Encode(map[string]any{
				"jsonrpc": "2.0",
				"id":      id,
				"result": map[string]any{
					"protocolVersion": 1,
					"_meta": map[string]any{
						"modelState": map[string]any{
							"currentModelId": "grok-4.6",
							"availableModels": []any{
								map[string]any{
									"modelId": "grok-4.6",
									"name":    "Grok 4.6",
									"_meta": map[string]any{
										"reasoningEfforts": []any{
											map[string]any{"value": "high"},
											map[string]any{"value": "low"},
										},
									},
								},
							},
						},
					},
				},
			})
		case "session/new":
			_ = enc.Encode(map[string]any{
				"jsonrpc": "2.0",
				"id":      id,
				"result":  map[string]any{"sessionId": "s-fake-1"},
			})
		case "session/load":
			_ = enc.Encode(map[string]any{
				"jsonrpc": "2.0",
				"id":      id,
				"result": map[string]any{
					"sessionId": req["params"].(map[string]any)["sessionId"],
					"configOptions": []any{
						map[string]any{"id": "model", "currentValue": "grok-4.6"},
						map[string]any{"id": "reasoning_effort", "currentValue": "high"},
					},
				},
			})
		default:
			_ = enc.Encode(map[string]any{
				"jsonrpc": "2.0",
				"id":      id,
				"result":  map[string]any{},
			})
		}
	}
}

func TestFakeACP_SendAndCompletion(t *testing.T) {
	if os.Getenv("TEST_MODE") == "fake_acp_send" {
		runFakeACPSend()
		return
	}

	root := t.TempDir()
	type eventRecord struct {
		sessionID string
		event     string
		payload   map[string]any
		status    string
	}
	var events []eventRecord
	var mu sync.Mutex

	adapter := grok.New(grok.Config{
		Executable: os.Args[0],
		Arguments:  []string{"-test.run=TestFakeACP_SendAndCompletion"},
		Workspace:  root,
		Allowed:    []string{root},
		AgentID:    "local-grok",
		Emit: func(sessionID, event string, payload map[string]any, status string) error {
			mu.Lock()
			events = append(events, eventRecord{sessionID: sessionID, event: event, payload: payload, status: status})
			mu.Unlock()
			return nil
		},
	})
	defer adapter.Close()

	os.Setenv("TEST_MODE", "fake_acp_send")
	defer os.Unsetenv("TEST_MODE")

	_, err := adapter.Spawn(context.Background(), core.Request{
		SessionID: "s-send-1",
		Fields:    map[string]any{"cwd": root},
	})
	if err != nil {
		t.Fatalf("Spawn failed: %v", err)
	}

	res, err := adapter.Command(context.Background(), core.Request{
		SessionID: "s-send-1",
		Action:    "session.send",
		Fields: map[string]any{
			"command_id": "c-1",
			"prompt":     "hello grok",
		},
	})
	if err != nil {
		t.Fatalf("Command session.send failed: %v", err)
	}
	if res.Status != "running" || res.Payload["accepted"] != true {
		t.Fatalf("expected running and accepted, got: %+v", res)
	}

	// Wait for background completion
	deadline := time.Now().Add(3 * time.Second)
	var completed *eventRecord
	for time.Now().Before(deadline) {
		mu.Lock()
		for i := range events {
			if events[i].event == "grok.completed" {
				completed = &events[i]
				break
			}
		}
		mu.Unlock()
		if completed != nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	if completed == nil {
		t.Fatal("grok.completed event was not emitted")
	}
	if completed.payload["agent_id"] != "local-grok" {
		t.Fatalf("expected agent_id local-grok, got: %v", completed.payload["agent_id"])
	}
	if completed.payload["command_id"] != "c-1" {
		t.Fatalf("expected command_id c-1, got: %v", completed.payload["command_id"])
	}
	if completed.status != "idle" {
		t.Fatalf("expected status idle, got: %s", completed.status)
	}
}

func runFakeACPSend() {
	dec := json.NewDecoder(os.Stdin)
	enc := json.NewEncoder(os.Stdout)
	for {
		var req map[string]any
		if err := dec.Decode(&req); err != nil {
			return
		}
		id := req["id"]
		method, _ := req["method"].(string)

		switch method {
		case "initialize":
			_ = enc.Encode(map[string]any{
				"jsonrpc": "2.0",
				"id":      id,
				"result": map[string]any{
					"protocolVersion": 1,
					"_meta": map[string]any{
						"modelState": map[string]any{
							"currentModelId": "grok-4.6",
							"availableModels": []any{
								map[string]any{"modelId": "grok-4.6", "name": "Grok 4.6"},
							},
						},
					},
				},
			})
		case "session/load":
			_ = enc.Encode(map[string]any{
				"jsonrpc": "2.0",
				"id":      id,
				"result":  map[string]any{"sessionId": req["params"].(map[string]any)["sessionId"]},
			})
		case "session/prompt":
			// Emit notification frame first
			_ = enc.Encode(map[string]any{
				"jsonrpc": "2.0",
				"method":  "session/update",
				"params": map[string]any{
					"sessionId": req["params"].(map[string]any)["sessionId"],
					"update": map[string]any{
						"sessionUpdate": "agent_message_chunk",
						"content":       map[string]any{"text": "Hello world!"},
					},
				},
			})
			time.Sleep(50 * time.Millisecond)
			// Return prompt result
			_ = enc.Encode(map[string]any{
				"jsonrpc": "2.0",
				"id":      id,
				"result":  map[string]any{"stopReason": "end_turn"},
			})
		default:
			_ = enc.Encode(map[string]any{"jsonrpc": "2.0", "id": id, "result": map[string]any{}})
		}
	}
}

func TestFakeACP_ConcurrentInterrupt(t *testing.T) {
	if os.Getenv("TEST_MODE") == "fake_acp_interrupt" {
		runFakeACPInterrupt()
		return
	}

	root := t.TempDir()
	var completedPayload map[string]any
	var mu sync.Mutex

	adapter := grok.New(grok.Config{
		Executable: os.Args[0],
		Arguments:  []string{"-test.run=TestFakeACP_ConcurrentInterrupt"},
		Workspace:  root,
		Allowed:    []string{root},
		AgentID:    "local-grok",
		Emit: func(sessionID, event string, payload map[string]any, status string) error {
			if event == "grok.completed" {
				mu.Lock()
				completedPayload = payload
				mu.Unlock()
			}
			return nil
		},
	})
	defer adapter.Close()

	os.Setenv("TEST_MODE", "fake_acp_interrupt")
	defer os.Unsetenv("TEST_MODE")

	_, err := adapter.Spawn(context.Background(), core.Request{
		SessionID: "s-int-1",
		Fields:    map[string]any{"cwd": root},
	})
	if err != nil {
		t.Fatalf("Spawn failed: %v", err)
	}

	// Send long-running prompt
	_, err = adapter.Command(context.Background(), core.Request{
		SessionID: "s-int-1",
		Action:    "session.send",
		Fields: map[string]any{
			"command_id": "c-int",
			"prompt":     "take your time",
		},
	})
	if err != nil {
		t.Fatalf("Send failed: %v", err)
	}

	// Concurrent interrupt
	time.Sleep(50 * time.Millisecond)
	intRes, err := adapter.Command(context.Background(), core.Request{
		SessionID: "s-int-1",
		Action:    "session.interrupt",
	})
	if err != nil {
		t.Fatalf("Interrupt failed: %v", err)
	}
	if intRes.Status != "running" || intRes.Payload["interrupted"] != true {
		t.Fatalf("unexpected interrupt result: %+v", intRes)
	}

	// Verify completed event has cancelled: true
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		cp := completedPayload
		mu.Unlock()
		if cp != nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	mu.Lock()
	defer mu.Unlock()
	if completedPayload == nil {
		t.Fatal("expected grok.completed event")
	}
	if completedPayload["cancelled"] != true {
		t.Fatalf("expected cancelled: true, got: %+v", completedPayload)
	}
}

func runFakeACPInterrupt() {
	dec := json.NewDecoder(os.Stdin)
	enc := json.NewEncoder(os.Stdout)
	var promptID any
	for {
		var req map[string]any
		if err := dec.Decode(&req); err != nil {
			return
		}
		id := req["id"]
		method, _ := req["method"].(string)
		switch method {
		case "initialize":
			enc.Encode(map[string]any{"jsonrpc": "2.0", "id": id, "result": map[string]any{"protocolVersion": 1}})
		case "session/load":
			enc.Encode(map[string]any{"jsonrpc": "2.0", "id": id, "result": map[string]any{"sessionId": req["params"].(map[string]any)["sessionId"]}})
		case "session/prompt":
			promptID = id // Keep reader available to receive cancellation.
		case "session/cancel":
			if promptID != nil {
				enc.Encode(map[string]any{"jsonrpc": "2.0", "id": promptID, "result": map[string]any{"stopReason": "cancelled"}})
				promptID = nil
			}
		default:
			enc.Encode(map[string]any{"jsonrpc": "2.0", "id": id, "result": map[string]any{}})
		}
	}
}

func TestFakeACP_ApprovalAndPermissions(t *testing.T) {
	if os.Getenv("TEST_MODE") == "fake_acp_approval" {
		runFakeACPApproval()
		return
	}

	root := t.TempDir()
	adapter := grok.New(grok.Config{
		Executable: os.Args[0],
		Arguments:  []string{"-test.run=TestFakeACP_ApprovalAndPermissions"},
		Workspace:  root,
		Allowed:    []string{root},
		AgentID:    "local-grok",
	})
	defer adapter.Close()

	os.Setenv("TEST_MODE", "fake_acp_approval")
	defer os.Unsetenv("TEST_MODE")

	_, err := adapter.Spawn(context.Background(), core.Request{
		SessionID: "s-appr-1",
		Fields:    map[string]any{"cwd": root},
	})
	if err != nil {
		t.Fatalf("Spawn failed: %v", err)
	}

	// Wait for fake ACP to send the server request
	time.Sleep(100 * time.Millisecond)

	// Check status is waiting_approval
	snap := adapter.Snapshot()
	if snap["s-appr-1"].Status != "waiting_approval" {
		t.Fatalf("expected waiting_approval, got: %s", snap["s-appr-1"].Status)
	}

	// Accept without explicit permissions must fail
	_, err = adapter.Command(context.Background(), core.Request{
		SessionID: "s-appr-1",
		Action:    "session.approve",
		Fields: map[string]any{
			"approval_id": "grok:42",
			"decision":    "accept",
		},
	})
	if err == nil || !strings.Contains(err.Error(), "explicit granted permissions") {
		t.Fatalf("expected explicit permissions error, got: %v", err)
	}

	// Accept with explicit permissions succeeds
	apprRes, err := adapter.Command(context.Background(), core.Request{
		SessionID: "s-appr-1",
		Action:    "session.approve",
		Fields: map[string]any{
			"approval_id": "grok:42",
			"decision":    "accept",
			"permissions": map[string]any{"fs": "read"},
		},
	})
	if err != nil {
		t.Fatalf("approval failed: %v", err)
	}
	if apprRes.Payload["accepted"] != true {
		t.Fatalf("expected accepted: true, got: %+v", apprRes)
	}

	// Duplicate approval rejected
	_, err = adapter.Command(context.Background(), core.Request{
		SessionID: "s-appr-1",
		Action:    "session.approve",
		Fields: map[string]any{
			"approval_id": "grok:42",
			"decision":    "accept",
			"permissions": map[string]any{"fs": "read"},
		},
	})
	if err == nil {
		t.Fatal("expected duplicate approval to be rejected")
	}
}

func runFakeACPApproval() {
	dec := json.NewDecoder(os.Stdin)
	enc := json.NewEncoder(os.Stdout)
	for {
		var req map[string]any
		if err := dec.Decode(&req); err != nil {
			return
		}
		id := req["id"]
		method, _ := req["method"].(string)

		switch method {
		case "initialize":
			_ = enc.Encode(map[string]any{
				"jsonrpc": "2.0",
				"id":      id,
				"result":  map[string]any{"protocolVersion": 1},
			})
		case "session/load":
			_ = enc.Encode(map[string]any{
				"jsonrpc": "2.0",
				"id":      id,
				"result":  map[string]any{"sessionId": req["params"].(map[string]any)["sessionId"]},
			})
			// Push approval request to daemon
			time.Sleep(20 * time.Millisecond)
			_ = enc.Encode(map[string]any{
				"jsonrpc": "2.0",
				"id":      42,
				"method":  "item/permissions/requestApproval",
				"params": map[string]any{
					"sessionId": "s-appr-1",
				},
			})
		default:
			if id == float64(42) {
				// Approved result received
				return
			}
			_ = enc.Encode(map[string]any{"jsonrpc": "2.0", "id": id, "result": map[string]any{}})
		}
	}
}

func TestFakeACP_ModelAndReasoningSetting(t *testing.T) {
	if os.Getenv("TEST_MODE") == "fake_acp_models" {
		runFakeACPBasic()
		return
	}

	root := t.TempDir()
	adapter := grok.New(grok.Config{
		Executable: os.Args[0],
		Arguments:  []string{"-test.run=TestFakeACP_ModelAndReasoningSetting"},
		Workspace:  root,
		Allowed:    []string{root},
	})
	defer adapter.Close()

	os.Setenv("TEST_MODE", "fake_acp_models")
	defer os.Unsetenv("TEST_MODE")

	_, err := adapter.Spawn(context.Background(), core.Request{
		SessionID: "s-mod-1",
		Fields:    map[string]any{"cwd": root},
	})
	if err != nil {
		t.Fatalf("Spawn failed: %v", err)
	}

	// Models list
	modelsRes, err := adapter.Command(context.Background(), core.Request{
		SessionID: "s-mod-1",
		Action:    "session.models",
	})
	if err != nil {
		t.Fatalf("session.models failed: %v", err)
	}
	items, _ := modelsRes.Payload["items"].([]map[string]any)
	if len(items) == 0 {
		t.Fatalf("expected non-empty models list, got: %+v", modelsRes)
	}

	// Model read
	readRes, err := adapter.Command(context.Background(), core.Request{
		SessionID: "s-mod-1",
		Action:    "session.model.read",
	})
	if err != nil {
		t.Fatalf("model.read failed: %v", err)
	}
	if readRes.Payload["model"] != "grok-4.6" || readRes.Payload["effort"] != "high" {
		t.Fatalf("unexpected read result: %+v", readRes)
	}

	// Model set invalid
	_, err = adapter.Command(context.Background(), core.Request{
		SessionID: "s-mod-1",
		Action:    "session.model.set",
		Fields:    map[string]any{"model": "nonexistent-model"},
	})
	if err == nil {
		t.Fatal("expected error on nonexistent model")
	}

	// Model set valid
	setRes, err := adapter.Command(context.Background(), core.Request{
		SessionID: "s-mod-1",
		Action:    "session.model.set",
		Fields:    map[string]any{"model": "grok-4.6"},
	})
	if err != nil {
		t.Fatalf("model.set failed: %v", err)
	}
	if setRes.Payload["model"] != "grok-4.6" {
		t.Fatalf("unexpected model set result: %+v", setRes)
	}

	// Reasoning set invalid
	_, err = adapter.Command(context.Background(), core.Request{
		SessionID: "s-mod-1",
		Action:    "session.reasoning.set",
		Fields:    map[string]any{"effort": "ultra-max"},
	})
	if err == nil {
		t.Fatal("expected error on invalid reasoning effort")
	}

	// Reasoning set valid
	effRes, err := adapter.Command(context.Background(), core.Request{
		SessionID: "s-mod-1",
		Action:    "session.reasoning.set",
		Fields:    map[string]any{"effort": "low"},
	})
	if err != nil {
		t.Fatalf("reasoning.set failed: %v", err)
	}
	if effRes.Payload["effort"] != "low" {
		t.Fatalf("unexpected reasoning set result: %+v", effRes)
	}
}

func TestFakeACP_EOFHandling(t *testing.T) {
	if os.Getenv("TEST_MODE") == "fake_acp_eof" {
		// Exit immediately
		os.Exit(0)
		return
	}

	root := t.TempDir()
	adapter := grok.New(grok.Config{
		Executable: os.Args[0],
		Arguments:  []string{"-test.run=TestFakeACP_EOFHandling"},
		Workspace:  root,
		Allowed:    []string{root},
	})
	defer adapter.Close()

	os.Setenv("TEST_MODE", "fake_acp_eof")
	defer os.Unsetenv("TEST_MODE")

	_, err := adapter.Spawn(context.Background(), core.Request{
		SessionID: "s-eof-1",
		Fields:    map[string]any{"cwd": root},
	})
	if err == nil {
		t.Fatal("expected spawn to fail on immediate EOF")
	}
}

func TestFakeACP_InvalidInputAndAllowlist(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()

	adapter := grok.New(grok.Config{
		Executable: "dummy",
		Workspace:  root,
		Allowed:    []string{root},
	})
	defer adapter.Close()

	// Outside allowlist
	_, err := adapter.Spawn(context.Background(), core.Request{
		SessionID: "s-out-1",
		Fields:    map[string]any{"cwd": outside},
	})
	if err == nil || !strings.Contains(err.Error(), "allowlisted") {
		t.Fatalf("expected allowlist error, got: %v", err)
	}

	// Missing session ID
	_, err = adapter.Spawn(context.Background(), core.Request{
		Fields: map[string]any{"cwd": root},
	})
	if err == nil || !strings.Contains(err.Error(), "session ID is required") {
		t.Fatalf("expected session ID error, got: %v", err)
	}

	// Command on unowned session
	_, err = adapter.Command(context.Background(), core.Request{
		SessionID: "not-owned",
		Action:    "session.send",
	})
	if err == nil || !strings.Contains(err.Error(), "not daemon-owned") {
		t.Fatalf("expected not daemon-owned error, got: %v", err)
	}
}

func TestFakeACP_EmitFailure(t *testing.T) {
	if os.Getenv("TEST_MODE") == "fake_acp_emit_err" {
		runFakeACPSend()
		return
	}

	root := t.TempDir()
	adapter := grok.New(grok.Config{
		Executable: os.Args[0],
		Arguments:  []string{"-test.run=TestFakeACP_EmitFailure"},
		Workspace:  root,
		Allowed:    []string{root},
		Emit: func(sessionID, event string, payload map[string]any, status string) error {
			if event == "runtime.owner" {
				return nil
			}
			return fmt.Errorf("intentional emit failure")
		},
	})
	defer adapter.Close()

	os.Setenv("TEST_MODE", "fake_acp_emit_err")
	defer os.Unsetenv("TEST_MODE")

	_, err := adapter.Spawn(context.Background(), core.Request{
		SessionID: "s-emit-1",
		Fields:    map[string]any{"cwd": root},
	})
	if err != nil {
		t.Fatalf("Spawn failed: %v", err)
	}

	// Send should still succeed without panic even when Emit fails
	res, err := adapter.Command(context.Background(), core.Request{
		SessionID: "s-emit-1",
		Action:    "session.send",
		Fields: map[string]any{
			"command_id": "c-emit",
			"prompt":     "hello",
		},
	})
	if err != nil {
		t.Fatalf("Send failed: %v", err)
	}
	if res.Status != "running" {
		t.Fatalf("expected running, got: %s", res.Status)
	}
	time.Sleep(100 * time.Millisecond)
}

func TestRealGrokIntegration(t *testing.T) {
	grokBin := "/home/luwei/.grok/bin/grok"
	if _, err := os.Stat(grokBin); err != nil {
		t.Skip("real Grok binary not found on host; native not verifiable")
	}

	root := t.TempDir()
	adapter := grok.New(grok.Config{
		Executable: grokBin,
		Workspace:  root,
		Allowed:    []string{root},
	})
	defer adapter.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Try unprompted create (calls initialize + session/new)
	res, err := adapter.Create(ctx, core.Request{
		Fields: map[string]any{"cwd": root},
	})
	if err != nil {
		t.Fatalf("native Grok create failed: %v", err)
	}
	if res.SessionID == "" {
		t.Fatal("native Grok create omitted session identity")
	}
}
