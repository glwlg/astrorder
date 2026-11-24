package antigravity

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	core "astrorder.dev/session-daemon/internal/runtime"
)

func TestSteerHelper(t *testing.T) {
	if os.Getenv("ASTRORDER_AGY_STEER_HELPER") != "1" {
		return
	}
	var writeMu sync.Mutex
	sendJSON := func(v map[string]any) {
		raw, _ := json.Marshal(v)
		writeMu.Lock()
		_, _ = os.Stdout.Write(append(raw, '\n'))
		writeMu.Unlock()
	}

	var mu sync.Mutex
	type activePrompt struct {
		reqID   any
		handoff chan struct{}
		finish  chan struct{}
	}
	active := map[string]*activePrompt{}
	const sid = "33333333-3333-4333-8333-333333333333"

	scan := bufio.NewScanner(os.Stdin)
	scan.Buffer(make([]byte, 65536), 8_000_000)
	for scan.Scan() {
		line := strings.TrimSpace(scan.Text())
		if line == "" {
			continue
		}
		var msg map[string]any
		if json.Unmarshal([]byte(line), &msg) != nil {
			continue
		}
		method, _ := msg["method"].(string)
		rawID := msg["id"]
		params, _ := msg["params"].(map[string]any)

		switch method {
		case "initialize":
			sendJSON(map[string]any{
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
		case "session/new":
			sendJSON(map[string]any{
				"jsonrpc": "2.0",
				"id":      rawID,
				"result": map[string]any{
					"sessionId": sid,
					"configOptions": []map[string]any{
						{"id": "model", "currentValue": "gemini-3.8-flash-high"},
						{"id": "mode", "currentValue": "default"},
					},
				},
			})
		case "session/close":
			sendJSON(map[string]any{"jsonrpc": "2.0", "id": rawID, "result": map[string]any{}})
		case "session/cancel":
			mu.Lock()
			ap := active[sid]
			mu.Unlock()
			if ap != nil {
				select {
				case <-ap.finish:
				default:
					close(ap.finish)
				}
			}
		case "session/prompt":
			promptArr, _ := params["prompt"].([]any)
			var text string
			for _, elem := range promptArr {
				if pm, ok := elem.(map[string]any); ok && str(pm, "type") == "text" {
					text += str(pm, "text")
				}
			}

			if strings.Contains(text, "FAIL_STEER") {
				sendJSON(map[string]any{
					"jsonrpc": "2.0",
					"id":      rawID,
					"error":   map[string]any{"code": -32603, "message": "injected steer error"},
				})
				// Let the original turn finish after a short delay so test observes survival.
				go func() {
					time.Sleep(60 * time.Millisecond)
					mu.Lock()
					ap := active[sid]
					mu.Unlock()
					if ap != nil {
						select {
						case <-ap.finish:
						default:
							close(ap.finish)
						}
					}
				}()
				continue
			}

			if strings.Contains(text, "WAIT_APPROVAL") {
				ap := &activePrompt{reqID: rawID, handoff: make(chan struct{}), finish: make(chan struct{})}
				mu.Lock()
				active[sid] = ap
				mu.Unlock()
				go func() {
					sendJSON(map[string]any{
						"jsonrpc": "2.0",
						"id":      "perm-steer-1",
						"method":  "session/request_permission",
						"params": map[string]any{
							"sessionId": sid,
							"toolCall":  map[string]any{"toolCallId": "tc-perm", "title": "Need approval", "kind": "execute"},
							"options":   []map[string]any{{"optionId": "allow_once", "name": "Allow", "kind": "allow_once"}},
						},
					})
					<-ap.finish
					sendJSON(map[string]any{
						"jsonrpc": "2.0",
						"id":      rawID,
						"result":  map[string]any{"stopReason": "cancelled"},
					})
				}()
				continue
			}

			if strings.Contains(text, "WAIT_STEER") {
				ap := &activePrompt{reqID: rawID, handoff: make(chan struct{}), finish: make(chan struct{})}
				mu.Lock()
				active[sid] = ap
				mu.Unlock()
				go func() {
					sendJSON(map[string]any{
						"jsonrpc": "2.0",
						"method":  "session/update",
						"params": map[string]any{
							"sessionId": sid,
							"update": map[string]any{
								"sessionUpdate": "tool_call",
								"toolCallId":    "call_step1",
								"title":         "sleep 3 && echo STEP_ONE_DONE",
								"kind":          "execute",
								"status":        "in_progress",
							},
						},
					})
					select {
					case <-ap.handoff:
						// Real agy v1.5.15 behavior: prompt #1 immediately resolves end_turn when prompt #2 steers.
						sendJSON(map[string]any{
							"jsonrpc": "2.0",
							"id":      rawID,
							"result": map[string]any{
								"stopReason": "end_turn",
								"usage": map[string]any{
									"inputTokens":  100,
									"outputTokens": 20,
									"totalTokens":  120,
								},
							},
						})
					case <-ap.finish:
						sendJSON(map[string]any{
							"jsonrpc": "2.0",
							"id":      rawID,
							"result":  map[string]any{"stopReason": "end_turn"},
						})
					}
				}()
				continue
			}

			// Steering prompt (or normal prompt) arrives: hand off any running prompt #1 first!
			mu.Lock()
			prev := active[sid]
			next := &activePrompt{reqID: rawID, handoff: make(chan struct{}), finish: make(chan struct{})}
			active[sid] = next
			mu.Unlock()
			if prev != nil {
				select {
				case <-prev.handoff:
				default:
					close(prev.handoff)
				}
			}
			go func(promptReqID any, body string) {
				// Simulate in-flight tool #1 finishing AFTER prompt #1 returned and BEFORE steer reply.
				time.Sleep(40 * time.Millisecond)
				sendJSON(map[string]any{
					"jsonrpc": "2.0",
					"method":  "session/update",
					"params": map[string]any{
						"sessionId": sid,
						"update": map[string]any{
							"sessionUpdate": "tool_call_update",
							"toolCallId":    "call_step1",
							"status":        "completed",
							"rawOutput":     map[string]any{"output": "STEP_ONE_DONE"},
						},
					},
				})
				time.Sleep(20 * time.Millisecond)
				sendJSON(map[string]any{
					"jsonrpc": "2.0",
					"method":  "session/update",
					"params": map[string]any{
						"sessionId": sid,
						"update": map[string]any{
							"sessionUpdate": "agent_message_chunk",
							"content":       map[string]any{"type": "text", "text": "STEER_ACK:" + body},
						},
					},
				})
				sendJSON(map[string]any{
					"jsonrpc": "2.0",
					"id":      promptReqID,
					"result": map[string]any{
						"stopReason": "end_turn",
						"usage": map[string]any{
							"inputTokens":  50,
							"outputTokens": 15,
							"totalTokens":  65,
						},
					},
				})
				mu.Lock()
				if active[sid] == next {
					delete(active, sid)
				}
				mu.Unlock()
			}(rawID, text)
		}
	}
}

func newSteerTestAdapter(t *testing.T, onEvent func(id, kind string, payload map[string]any, status string)) (*Adapter, string) {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ws := t.TempDir()
	a := New(Config{
		Executable:  exe,
		Arguments:   []string{"-test.run=TestSteerHelper", "--"},
		Environment: []string{"ASTRORDER_AGY_STEER_HELPER=1"},
		Workspace:   ws,
		Allowed:     []string{ws},
		AgentID:     "agy-steer-test",
		Emit: func(id, kind string, payload map[string]any, status string) error {
			if onEvent != nil {
				onEvent(id, kind, payload, status)
			}
			return nil
		},
	})
	t.Cleanup(func() { _ = a.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	created, err := a.Create(ctx, core.Request{Fields: map[string]any{}})
	if err != nil {
		t.Fatalf("create failed: %v", err)
	}
	return a, created.SessionID
}

func TestSteerConcurrentPromptHandoffAndIdleIsolation(t *testing.T) {
	var mu sync.Mutex
	var statusTimeline []string
	cmdStates := map[string][]string{}
	var toolUpdates []map[string]any
	var messages []string
	toolStarted := make(chan struct{}, 1)

	a, sid := newSteerTestAdapter(t, func(_, kind string, payload map[string]any, _ string) {
		if kind != "connector.event" {
			return
		}
		evType, _ := payload["type"].(string)
		data, _ := payload["data"].(map[string]any)
		mu.Lock()
		defer mu.Unlock()
		switch evType {
		case "session.upsert":
			if st := str(data, "status"); st != "" {
				statusTimeline = append(statusTimeline, st)
			}
		case "command.upsert":
			cid := str(data, "id")
			cmdStates[cid] = append(cmdStates[cid], str(data, "state"))
		case "task.upsert":
			toolUpdates = append(toolUpdates, data)
			if str(data, "id") == "agy:tool:call_step1" && str(data, "status") == "running" {
				select {
				case toolStarted <- struct{}{}:
				default:
				}
			}
		case "message.upsert":
			if txt := str(data, "text"); txt != "" {
				messages = append(messages, txt)
			}
		}
	})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// session.steer must be rejected while idle.
	if _, err := a.Command(ctx, core.Request{
		SessionID: sid,
		Action:    "session.steer",
		Fields:    map[string]any{"text": "too early"},
	}); err == nil {
		t.Fatal("expected session.steer while idle to fail")
	}

	// Start Prompt #1.
	res1, err := a.Command(ctx, core.Request{
		SessionID: sid,
		Action:    "session.send",
		Fields:    map[string]any{"text": "WAIT_STEER", "command_id": "cmd-1"},
	})
	if err != nil || res1.Payload["accepted"] != true {
		t.Fatalf("prompt #1 failed: %+v %v", res1, err)
	}
	select {
	case <-toolStarted:
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for tool_call #1")
	}

	// Queued idle_only send while running MUST be rejected (never cross into steering).
	if _, err := a.Command(ctx, core.Request{
		SessionID: sid,
		Action:    "session.send",
		Fields: map[string]any{
			"text":       "queued only",
			"command_id": "cmd-queued",
			"params":     map[string]any{"idle_only": true},
		},
	}); err == nil {
		t.Fatal("expected idle_only send while running to be rejected")
	}

	// Explicit steering send while running succeeds and hands off turn ownership.
	res2, err := a.Command(ctx, core.Request{
		SessionID: sid,
		Action:    "session.send",
		Fields:    map[string]any{"text": "change course now", "command_id": "cmd-2"},
	})
	if err != nil || res2.Payload["accepted"] != true || res2.Payload["steered"] != true {
		t.Fatalf("steer send failed: %+v %v", res2, err)
	}

	// Wait for the steered turn to settle back to idle.
	deadline := time.Now().Add(5 * time.Second)
	for a.Snapshot()[sid].Status != "idle" && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if got := a.Snapshot()[sid].Status; got != "idle" {
		t.Fatalf("steered turn did not settle to idle: %s", got)
	}

	mu.Lock()
	defer mu.Unlock()
	// Confirm cmd-1 and cmd-2 both completed.
	if len(cmdStates["cmd-1"]) == 0 || cmdStates["cmd-1"][len(cmdStates["cmd-1"])-1] != "completed" {
		t.Fatalf("expected cmd-1 completed, got %+v", cmdStates["cmd-1"])
	}
	if len(cmdStates["cmd-2"]) == 0 || cmdStates["cmd-2"][len(cmdStates["cmd-2"])-1] != "completed" {
		t.Fatalf("expected cmd-2 completed, got %+v", cmdStates["cmd-2"])
	}
	// Confirm NO premature idle transition happened while steered turn was still running.
	idleCount := 0
	for _, st := range statusTimeline {
		if st == "idle" {
			idleCount++
		}
	}
	// Initial openSession emits idle once (index 0); final turn completion emits idle once.
	if idleCount != 2 {
		t.Fatalf("expected exactly 2 idle announcements (open + final completion), got %d in %v", idleCount, statusTimeline)
	}
	// Confirm tool_call_update after handoff preserved tool metadata and marked completed.
	foundCompletedTool := false
	for _, tu := range toolUpdates {
		if str(tu, "id") == "agy:tool:call_step1" && str(tu, "status") == "completed" {
			foundCompletedTool = true
		}
	}
	if !foundCompletedTool {
		t.Fatalf("expected call_step1 tool_call_update to complete cleanly across steer, got %+v", toolUpdates)
	}
	foundAck := false
	for _, m := range messages {
		if strings.Contains(m, "STEER_ACK:change course now") {
			foundAck = true
		}
	}
	if !foundAck {
		t.Fatalf("missing steered reply in messages: %v", messages)
	}
}

func TestSteerExplicitSessionSteerWithInputParts(t *testing.T) {
	toolStarted := make(chan struct{}, 1)
	a, sid := newSteerTestAdapter(t, func(_, kind string, payload map[string]any, _ string) {
		if kind != "connector.event" {
			return
		}
		if payload["type"] == "task.upsert" {
			data, _ := payload["data"].(map[string]any)
			if str(data, "id") == "agy:tool:call_step1" && str(data, "status") == "running" {
				select {
				case toolStarted <- struct{}{}:
				default:
				}
			}
		}
	})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if _, err := a.Command(ctx, core.Request{
		SessionID: sid,
		Action:    "session.send",
		Fields:    map[string]any{"text": "WAIT_STEER", "command_id": "cmd-base"},
	}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-toolStarted:
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for tool_call")
	}

	// Simulate gRPC Session.Steer payload shape (input parts without text/prompt).
	res, err := a.Command(ctx, core.Request{
		SessionID: sid,
		Action:    "session.steer",
		Fields: map[string]any{
			"command_id": "cmd-steer-rpc",
			"input":      []any{map[string]any{"type": "text", "text": "rpc steer guidance"}},
		},
	})
	if err != nil || res.Payload["accepted"] != true || res.Payload["steered"] != true {
		t.Fatalf("session.steer failed: %+v %v", res, err)
	}

	deadline := time.Now().Add(5 * time.Second)
	for a.Snapshot()[sid].Status != "idle" && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if a.Snapshot()[sid].Status != "idle" {
		t.Fatalf("expected idle after session.steer, got %s", a.Snapshot()[sid].Status)
	}
}

func TestSteerRejectedWhileWaitingApproval(t *testing.T) {
	a, sid := newSteerTestAdapter(t, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if _, err := a.Command(ctx, core.Request{
		SessionID: sid,
		Action:    "session.send",
		Fields:    map[string]any{"text": "WAIT_APPROVAL", "command_id": "cmd-perm"},
	}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for a.Snapshot()[sid].Status != "waiting_approval" && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if a.Snapshot()[sid].Status != "waiting_approval" {
		t.Fatalf("expected waiting_approval, got %s", a.Snapshot()[sid].Status)
	}

	if _, err := a.Command(ctx, core.Request{
		SessionID: sid,
		Action:    "session.send",
		Fields:    map[string]any{"text": "steer while blocked"},
	}); err == nil || !strings.Contains(err.Error(), "waiting for approval") {
		t.Fatalf("expected waiting_approval rejection, got %v", err)
	}
	if _, err := a.Command(ctx, core.Request{
		SessionID: sid,
		Action:    "session.steer",
		Fields:    map[string]any{"text": "steer while blocked"},
	}); err == nil || !strings.Contains(err.Error(), "waiting for approval") {
		t.Fatalf("expected waiting_approval rejection on session.steer, got %v", err)
	}
}

func TestSteerFailurePreservesOriginalRunningTurn(t *testing.T) {
	var mu sync.Mutex
	cmdStates := map[string]string{}
	toolStarted := make(chan struct{}, 1)

	a, sid := newSteerTestAdapter(t, func(_, kind string, payload map[string]any, _ string) {
		if kind != "connector.event" {
			return
		}
		evType, _ := payload["type"].(string)
		data, _ := payload["data"].(map[string]any)
		if evType == "command.upsert" {
			mu.Lock()
			cmdStates[str(data, "id")] = str(data, "state")
			mu.Unlock()
		}
		if evType == "task.upsert" && str(data, "id") == "agy:tool:call_step1" && str(data, "status") == "running" {
			select {
			case toolStarted <- struct{}{}:
			default:
			}
		}
	})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if _, err := a.Command(ctx, core.Request{
		SessionID: sid,
		Action:    "session.send",
		Fields:    map[string]any{"text": "WAIT_STEER", "command_id": "cmd-orig"},
	}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-toolStarted:
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for original turn")
	}

	if _, err := a.Command(ctx, core.Request{
		SessionID: sid,
		Action:    "session.send",
		Fields:    map[string]any{"text": "FAIL_STEER", "command_id": "cmd-fail-steer"},
	}); err != nil {
		t.Fatal(err)
	}

	// Wait for the steer error to arrive while original turn is still active, then original settles.
	deadline := time.Now().Add(5 * time.Second)
	for a.Snapshot()[sid].Status != "idle" && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if a.Snapshot()[sid].Status != "idle" {
		t.Fatalf("expected session to settle cleanly to idle (not error), got %s", a.Snapshot()[sid].Status)
	}

	mu.Lock()
	defer mu.Unlock()
	if cmdStates["cmd-fail-steer"] != "failed" {
		t.Fatalf("expected failed steer command state=failed, got %q", cmdStates["cmd-fail-steer"])
	}
	if cmdStates["cmd-orig"] != "completed" {
		t.Fatalf("expected original command state=completed, got %q", cmdStates["cmd-orig"])
	}
}

func TestSteerDuringReactiveWaitingTakesOver(t *testing.T) {
	var mu sync.Mutex
	cmdStates := map[string]string{}

	a, sid := newSteerTestAdapter(t, func(_, kind string, payload map[string]any, _ string) {
		if kind == "connector.event" && payload["type"] == "command.upsert" {
			data, _ := payload["data"].(map[string]any)
			mu.Lock()
			cmdStates[str(data, "id")] = str(data, "state")
			mu.Unlock()
		}
	})

	// Put session into reactiveWaiting (prompt #1 already returned early while bg task runs).
	a.mu.Lock()
	s := a.sessions[sid]
	a.mu.Unlock()
	s.mu.Lock()
	s.status = "running"
	s.promptRunning = false
	s.activePrompts = 0
	s.reactiveWaiting = true
	s.command = "cmd-bg"
	s.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	res, err := a.Command(ctx, core.Request{
		SessionID: sid,
		Action:    "session.send",
		Fields:    map[string]any{"text": "reactive steer", "command_id": "cmd-reactive-steer"},
	})
	if err != nil || res.Payload["steered"] != true {
		t.Fatalf("reactive steer failed: %+v %v", res, err)
	}

	deadline := time.Now().Add(5 * time.Second)
	for a.Snapshot()[sid].Status != "idle" && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if a.Snapshot()[sid].Status != "idle" {
		t.Fatalf("expected idle after reactive steer, got %s", a.Snapshot()[sid].Status)
	}

	mu.Lock()
	defer mu.Unlock()
	if cmdStates["cmd-bg"] != "completed" {
		t.Fatalf("expected previous reactive command cmd-bg completed, got %q", cmdStates["cmd-bg"])
	}
	if cmdStates["cmd-reactive-steer"] != "completed" {
		t.Fatalf("expected cmd-reactive-steer completed, got %q", cmdStates["cmd-reactive-steer"])
	}
}
