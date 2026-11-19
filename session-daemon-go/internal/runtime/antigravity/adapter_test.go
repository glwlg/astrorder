package antigravity

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	core "astrorder.dev/session-daemon/internal/runtime"
)

func serveMockACP(r io.Reader, w io.Writer, defaultSessionID string, crashOnPrompt bool) {
	var writeMu sync.Mutex
	sendJSON := func(v map[string]any) {
		raw, _ := json.Marshal(v)
		raw = append(raw, '\n')
		writeMu.Lock()
		_, _ = w.Write(raw)
		writeMu.Unlock()
	}

	var stateMu sync.Mutex
	curModel := "gemini-test-medium"
	curMode := "default"
	curThought := "medium"
	permSeq := 100
	permCh := map[string]chan map[string]any{}
	cancelCh := map[string]chan struct{}{}

	scan := bufio.NewScanner(r)
	scan.Buffer(make([]byte, 65536), 8_000_000)
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

		method := str(msg, "method")
		rawID, hasID := msg["id"]
		params, _ := msg["params"].(map[string]any)

		if method == "" && hasID {
			stateMu.Lock()
			ch := permCh[fmt.Sprint(rawID)]
			delete(permCh, fmt.Sprint(rawID))
			stateMu.Unlock()
			if ch != nil {
				res, _ := msg["result"].(map[string]any)
				ch <- res
			}
			continue
		}

		switch method {
		case "initialize":
			sendJSON(map[string]any{
				"jsonrpc": "2.0",
				"id":      rawID,
				"result": map[string]any{
					"protocolVersion": 1,
					"agentInfo":       map[string]any{"name": "agy-acp-mock", "version": "1.5.10"},
					"agentCapabilities": map[string]any{
						"loadSession":        true,
						"promptCapabilities": map[string]any{"image": true, "embeddedContext": true},
						"sessionCapabilities": map[string]any{
							"resume": map[string]any{},
							"close":  map[string]any{},
							"load":   map[string]any{},
							"list":   map[string]any{},
						},
					},
				},
			})

		case "session/new", "session/resume", "session/load":
			sid := str(params, "sessionId")
			if sid == "" {
				sid = defaultSessionID
			}
			stateMu.Lock()
			m, md, th := curModel, curMode, curThought
			stateMu.Unlock()

			sendJSON(map[string]any{
				"jsonrpc": "2.0",
				"method":  "session/update",
				"params": map[string]any{
					"sessionId": sid,
					"update": map[string]any{
						"sessionUpdate": "available_commands_update",
						"availableCommands": []map[string]any{
							{"name": "compact", "description": "Compact context", "input": map[string]any{"hint": "instructions"}},
							{"name": "plan", "description": "Enter plan mode"},
						},
					},
				},
			})

			sendJSON(map[string]any{
				"jsonrpc": "2.0",
				"id":      rawID,
				"result": map[string]any{
					"sessionId": sid,
					"models": map[string]any{
						"currentModelId": m,
						"availableModels": []map[string]any{
							{"modelId": "gemini-test-low", "name": "Test Gemini (Low)"},
							{"modelId": "gemini-test-medium", "name": "Test Gemini (Medium)"},
							{"modelId": "gemini-test-high", "name": "Test Gemini (High)"},
							{"modelId": "claude-test", "name": "Test Claude"},
						},
					},
					"modes": map[string]any{
						"currentModeId": md,
						"availableModes": []map[string]any{
							{"id": "default", "name": "Default"},
							{"id": "auto", "name": "Auto"},
						},
					},
					"configOptions": []map[string]any{
						{"id": "model", "currentValue": "gemini-test"},
						{
							"id":           "thought_level",
							"currentValue": th,
							"options": []map[string]any{
								{"value": "low"},
								{"value": "medium"},
								{"value": "high"},
							},
						},
						{"id": "mode", "currentValue": md},
					},
					"_meta": map[string]any{
						"antigravity": map[string]any{
							"tier": map[string]any{"id": "g1-ultra-tier", "name": "Google AI Ultra"},
						},
					},
				},
			})

		case "session/set_model":
			next := str(params, "modelId")
			stateMu.Lock()
			curModel = next
			stateMu.Unlock()
			sendJSON(map[string]any{"jsonrpc": "2.0", "id": rawID, "result": map[string]any{}})

		case "session/set_mode":
			next := str(params, "modeId")
			stateMu.Lock()
			curMode = next
			stateMu.Unlock()
			sendJSON(map[string]any{"jsonrpc": "2.0", "id": rawID, "result": map[string]any{}})

		case "session/set_config_option":
			cfgID := str(params, "configId")
			val := str(params, "value")
			if cfgID != "thought_level" || (val != "low" && val != "medium" && val != "high") {
				sendJSON(map[string]any{
					"jsonrpc": "2.0",
					"id":      rawID,
					"error":   map[string]any{"code": -32602, "message": "unsupported config option"},
				})
				continue
			}
			stateMu.Lock()
			curThought = val
			curModel = "gemini-test-" + val
			md := curMode
			stateMu.Unlock()
			sendJSON(map[string]any{
				"jsonrpc": "2.0",
				"id":      rawID,
				"result": map[string]any{
					"configOptions": []map[string]any{
						{"id": "model", "currentValue": "gemini-test"},
						{
							"id":           "thought_level",
							"currentValue": val,
							"options": []map[string]any{
								{"value": "low"},
								{"value": "medium"},
								{"value": "high"},
							},
						},
						{"id": "mode", "currentValue": md},
					},
				},
			})

		case "session/cancel":
			sid := str(params, "sessionId")
			stateMu.Lock()
			cch := cancelCh[sid]
			stateMu.Unlock()
			if cch != nil {
				select {
				case <-cch:
				default:
					close(cch)
				}
			}

		case "session/close":
			sendJSON(map[string]any{"jsonrpc": "2.0", "id": rawID, "result": map[string]any{}})

		case "session/prompt":
			if crashOnPrompt {
				return
			}
			sid := str(params, "sessionId")
			promptArr, _ := params["prompt"].([]any)
			var promptText string
			hasImage := false
			for _, elem := range promptArr {
				pm, _ := elem.(map[string]any)
				if str(pm, "type") == "text" {
					promptText += str(pm, "text")
				} else if str(pm, "type") == "image" {
					hasImage = true
				}
			}

			cch := make(chan struct{})
			stateMu.Lock()
			cancelCh[sid] = cch
			stateMu.Unlock()

			go func(reqID any, sessionID, text string, withImg bool, cancelled <-chan struct{}) {
				defer func() {
					stateMu.Lock()
					delete(cancelCh, sessionID)
					stateMu.Unlock()
				}()

				if strings.Contains(text, "WAIT_CANCEL") {
					sendJSON(map[string]any{
						"jsonrpc": "2.0",
						"method":  "session/update",
						"params": map[string]any{
							"sessionId": sessionID,
							"update": map[string]any{
								"sessionUpdate": "agent_thought_chunk",
								"content":       map[string]any{"type": "text", "text": "waiting for cancel"},
							},
						},
					})
					<-cancelled
					sendJSON(map[string]any{
						"jsonrpc": "2.0",
						"id":      reqID,
						"result":  map[string]any{"stopReason": "cancelled"},
					})
					return
				}

				if strings.Contains(text, "NEED_APPROVAL") {
					stateMu.Lock()
					permSeq++
					pid := permSeq
					pch := make(chan map[string]any, 1)
					permCh[fmt.Sprint(pid)] = pch
					stateMu.Unlock()

					sendJSON(map[string]any{
						"jsonrpc": "2.0",
						"id":      pid,
						"method":  "session/request_permission",
						"params": map[string]any{
							"sessionId": sessionID,
							"toolCall": map[string]any{
								"toolCallId": "call-perm-1",
								"name":       "run_command",
								"title":      "Run echo approved",
								"kind":       "execute",
								"rawInput":   map[string]any{"CommandLine": "echo approved"},
							},
							"options": []map[string]any{
								{"optionId": "allow_once", "name": "Allow once", "kind": "allow_once"},
								{"optionId": "allow_always", "name": "Always allow", "kind": "allow_always"},
								{"optionId": "reject_once", "name": "Reject", "kind": "reject_once"},
							},
						},
					})

					select {
					case pres := <-pch:
						outcome, _ := pres["outcome"].(map[string]any)
						opt := str(outcome, "optionId")
						if str(outcome, "outcome") != "selected" || strings.HasPrefix(opt, "reject") {
							sendJSON(map[string]any{
								"jsonrpc": "2.0",
								"method":  "session/update",
								"params": map[string]any{
									"sessionId": sessionID,
									"update": map[string]any{
										"sessionUpdate": "tool_call",
										"toolCallId":    "call-perm-1",
										"name":          "run_command",
										"status":        "failed",
										"error":         "permission denied by user",
									},
								},
							})
							sendJSON(map[string]any{
								"jsonrpc": "2.0",
								"id":      reqID,
								"result":  map[string]any{"stopReason": "end_turn"},
							})
							return
						}
					case <-cancelled:
						sendJSON(map[string]any{
							"jsonrpc": "2.0",
							"id":      reqID,
							"result":  map[string]any{"stopReason": "cancelled"},
						})
						return
					}
				}

				sendJSON(map[string]any{
					"jsonrpc": "2.0",
					"method":  "session/update",
					"params": map[string]any{
						"sessionId": sessionID,
						"update": map[string]any{
							"sessionUpdate": "agent_thought_chunk",
							"content":       map[string]any{"type": "text", "text": "thinking step"},
						},
					},
				})
				sendJSON(map[string]any{
					"jsonrpc": "2.0",
					"method":  "session/update",
					"params": map[string]any{
						"sessionId": sessionID,
						"update": map[string]any{
							"sessionUpdate": "tool_call",
							"toolCallId":    "call-1",
							"name":          "run_command",
							"kind":          "execute",
							"title":         "Run status check",
							"status":        "completed",
							"rawInput":      map[string]any{"CommandLine": "git status -sb"},
							"rawOutput":     "## main\n",
						},
					},
				})
				reply := "hello response"
				if withImg {
					reply = "image response"
				}
				sendJSON(map[string]any{
					"jsonrpc": "2.0",
					"method":  "session/update",
					"params": map[string]any{
						"sessionId": sessionID,
						"update": map[string]any{
							"sessionUpdate": "agent_message_chunk",
							"content":       map[string]any{"type": "text", "text": reply},
						},
					},
				})
				sendJSON(map[string]any{
					"jsonrpc": "2.0",
					"method":  "session/update",
					"params": map[string]any{
						"sessionId": sessionID,
						"update": map[string]any{
							"sessionUpdate": "usage_update",
							"used":          19585,
							"size":          1048576,
						},
					},
				})
				sendJSON(map[string]any{
					"jsonrpc": "2.0",
					"id":      reqID,
					"result":  map[string]any{"stopReason": "end_turn"},
				})
			}(rawID, sid, promptText, hasImage, cch)

		default:
			sendJSON(map[string]any{
				"jsonrpc": "2.0",
				"id":      rawID,
				"error":   map[string]any{"code": -32601, "message": "Method not found"},
			})
		}
	}
}

func TestStreamHelper(t *testing.T) {
	if os.Getenv("ASTRORDER_AGY_HELPER") != "1" {
		return
	}
	for _, arg := range os.Args {
		if arg == "models" {
			fmt.Println("gemini-test-medium\tTest Gemini (Medium)")
			fmt.Println("claude-test\tTest Claude")
			os.Exit(0)
		}
	}
	serveMockACP(os.Stdin, os.Stdout, "11111111-1111-4111-8111-111111111111", os.Getenv("ASTRORDER_AGY_EOF") == "1")
	os.Exit(0)
}

func TestACPLifecycleAndCapabilities(t *testing.T) {
	exe, _ := os.Executable()
	var mu sync.Mutex
	var events []map[string]any
	ws := t.TempDir()
	a := New(Config{
		Executable:  exe,
		Arguments:   []string{"-test.run=TestStreamHelper", "--"},
		Environment: []string{"ASTRORDER_AGY_HELPER=1"},
		Workspace:   ws,
		Allowed:     []string{ws},
		AgentID:     "local-antigravity",
		Emit: func(id, event string, p map[string]any, status string) error {
			mu.Lock()
			defer mu.Unlock()
			events = append(events, map[string]any{"event": event, "payload": p, "status": status})
			return nil
		},
	})
	defer a.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Fork must be cleanly rejected before dispatch in ACP mode.
	if _, err := a.Create(ctx, core.Request{Fields: map[string]any{"parent_session_id": "11111111-1111-4111-8111-111111111111"}}); err == nil {
		t.Fatal("expected ACP fork rejection")
	}

	created, err := a.Create(ctx, core.Request{Fields: map[string]any{"effort": "high"}})
	if err != nil {
		t.Fatal(err)
	}
	if created.SessionID != "11111111-1111-4111-8111-111111111111" || created.Payload["effort"] != "high" {
		t.Fatalf("unexpected created session: %#v", created)
	}

	// Verify slash commands from available_commands_update.
	deadline := time.Now().Add(2 * time.Second)
	var cmds []map[string]any
	for time.Now().Before(deadline) {
		cmdRes, cErr := a.Command(ctx, core.Request{SessionID: created.SessionID, Action: "session.commands"})
		if cErr == nil {
			cmds, _ = cmdRes.Payload["items"].([]map[string]any)
			if len(cmds) == 2 {
				break
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	if len(cmds) != 2 || cmds[0]["name"] != "compact" {
		t.Fatalf("expected ACP slash commands, got %+v", cmds)
	}

	// Verify compliant tier-only quota metadata from _meta.antigravity.tier.
	quotaRes, err := a.Query(ctx, core.Request{Fields: map[string]any{"method": "quota.read"}})
	if err != nil || quotaRes["plan"] != "Google AI Ultra" {
		t.Fatalf("unexpected ACP tier quota: %+v %v", quotaRes, err)
	}

	// Send text + multimodal PNG attachment.
	pngBytes := []byte{0x89, 0x50, 0x4e, 0x47}
	sendReq := core.Request{
		SessionID: created.SessionID,
		Action:    "session.send",
		Fields: map[string]any{
			"text":       "hello",
			"command_id": "cmd-1",
			"attachments": []any{
				map[string]any{
					"name":           "shot.png",
					"media_type":     "image/png",
					"content_base64": base64.StdEncoding.EncodeToString(pngBytes),
				},
			},
		},
	}
	if _, err = a.Command(ctx, sendReq); err != nil {
		t.Fatalf("multimodal send failed: %v", err)
	}
	deadline = time.Now().Add(5 * time.Second)
	for a.Snapshot()[created.SessionID].Status == "running" && time.Now().Before(deadline) {
		time.Sleep(2 * time.Millisecond)
	}
	if a.Snapshot()[created.SessionID].Status != "idle" {
		t.Fatalf("turn did not settle: %+v", a.Snapshot()[created.SessionID])
	}

	// Interactive permission request + approve flow.
	if _, err = a.Command(ctx, core.Request{
		SessionID: created.SessionID,
		Action:    "session.send",
		Fields:    map[string]any{"text": "NEED_APPROVAL", "command_id": "cmd-perm"},
	}); err != nil {
		t.Fatal(err)
	}
	var approvalID string
	deadline = time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if a.Snapshot()[created.SessionID].Status == "waiting_approval" {
			readApp, _ := a.Command(ctx, core.Request{SessionID: created.SessionID, Action: "session.approval.read"})
			if pending, ok := readApp.Payload["pending"].([]map[string]any); ok && len(pending) == 1 {
				approvalID = fmt.Sprint(pending[0]["approval_id"])
				break
			}
		}
		time.Sleep(2 * time.Millisecond)
	}
	if approvalID == "" {
		t.Fatal("expected waiting_approval with pending permission request")
	}
	if _, err = a.Command(ctx, core.Request{
		SessionID: created.SessionID,
		Action:    "session.approve",
		Fields:    map[string]any{"approval_id": approvalID, "decision": "accept", "choice": "once"},
	}); err != nil {
		t.Fatalf("approve failed: %v", err)
	}
	deadline = time.Now().Add(3 * time.Second)
	for a.Snapshot()[created.SessionID].Status != "idle" && time.Now().Before(deadline) {
		time.Sleep(2 * time.Millisecond)
	}
	if a.Snapshot()[created.SessionID].Status != "idle" {
		t.Fatal("approved turn did not settle to idle")
	}

	// Malformed attachment metadata must still be rejected before dispatch.
	badAttach := core.Request{
		SessionID: created.SessionID,
		Action:    "session.send",
		Fields: map[string]any{
			"text":        "hello",
			"attachments": []any{map[string]any{"name": "broken.png"}},
		},
	}
	if _, err = a.Command(ctx, badAttach); err == nil {
		t.Fatal("incomplete attachment metadata accepted")
	}
	if _, err = a.Command(ctx, core.Request{SessionID: created.SessionID, Action: "session.steer"}); err == nil {
		t.Fatal("unsupported steer accepted")
	}

	mu.Lock()
	for _, ev := range events {
		if ev["event"] == "runtime.owner" {
			payload := ev["payload"].(map[string]any)
			if pid, ok := payload["owner_pid"].(int); !ok || pid <= 0 {
				t.Errorf("runtime owner must identify its actual process: %+v", payload)
			}
		}
	}
	raw, _ := json.Marshal(events)
	mu.Unlock()
	if !strings.Contains(string(raw), "image response") || !strings.Contains(string(raw), "1048576") {
		t.Fatalf("missing expected ACP response or 1M usage_update in events: %s", raw)
	}
}

func TestEOFIsFailure(t *testing.T) {
	exe, _ := os.Executable()
	ws := t.TempDir()
	a := New(Config{
		Executable:  exe,
		Arguments:   []string{"-test.run=TestStreamHelper", "--"},
		Environment: []string{"ASTRORDER_AGY_HELPER=1", "ASTRORDER_AGY_EOF=1"},
		Workspace:   ws,
		Allowed:     []string{ws},
	})
	defer a.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	r, err := a.Create(ctx, core.Request{Fields: map[string]any{}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = a.Command(ctx, core.Request{SessionID: r.SessionID, Action: "session.send", Fields: map[string]any{"text": "hello"}})
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for a.Snapshot()[r.SessionID].Status == "running" && time.Now().Before(deadline) {
		time.Sleep(2 * time.Millisecond)
	}
	if a.Snapshot()[r.SessionID].Status != "error" {
		t.Fatalf("EOF disguised as completion: %+v", a.Snapshot()[r.SessionID])
	}
}

func TestInterruptWaitsForACPStopReason(t *testing.T) {
	exe, _ := os.Executable()
	ws := t.TempDir()
	a := New(Config{
		Executable:  exe,
		Arguments:   []string{"-test.run=TestStreamHelper", "--"},
		Environment: []string{"ASTRORDER_AGY_HELPER=1"},
		Workspace:   ws,
		Allowed:     []string{ws},
	})
	defer a.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	created, err := a.Create(ctx, core.Request{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Command(ctx, core.Request{SessionID: created.SessionID, Action: "session.send", Fields: map[string]any{"text": "WAIT_CANCEL", "command_id": "cancel-cmd"}}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(30 * time.Millisecond)
	res, err := a.Command(ctx, core.Request{SessionID: created.SessionID, Action: "session.interrupt"})
	if err != nil || res.Payload["accepted"] != true {
		t.Fatalf("interrupt failed: %+v %v", res, err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for a.Snapshot()[created.SessionID].Status == "running" && time.Now().Before(deadline) {
		time.Sleep(2 * time.Millisecond)
	}
	if a.Snapshot()[created.SessionID].Status != "idle" {
		t.Fatalf("cancelled turn did not settle to idle: %+v", a.Snapshot()[created.SessionID])
	}
}

func TestHistoryRejectsTraversal(t *testing.T) {
	_, _, err := readHistory(context.Background(), filepath.Join(t.TempDir(), "conversations"), "../secrets", "", 50)
	if err == nil {
		t.Fatal("invalid native identity accepted")
	}
}
