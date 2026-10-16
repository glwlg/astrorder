package grok

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	core "astrorder.dev/session-daemon/internal/runtime"
	"astrorder.dev/session-daemon/internal/transport/ssh"
)

func TestGrokApproval_StandardACPAcceptAndDeclineAndCancel(t *testing.T) {
	root := t.TempDir()
	emitted := make([]map[string]any, 0)
	var emitMu sync.Mutex
	emit := func(sessionID, event string, payload map[string]any, status string) error {
		emitMu.Lock()
		defer emitMu.Unlock()
		emitted = append(emitted, map[string]any{
			"session_id": sessionID,
			"event":      event,
			"payload":    payload,
			"status":     status,
		})
		return nil
	}

	adapter := New(Config{
		AgentID:   "local-grok",
		Workspace: root,
		Allowed:   []string{root},
		Emit:      emit,
	})

	sinR, sinW := io.Pipe()
	defer sinR.Close()
	defer sinW.Close()

	captured := make(chan map[string]any, 10)
	go func() {
		dec := json.NewDecoder(sinR)
		for {
			var frame map[string]any
			if err := dec.Decode(&frame); err != nil {
				return
			}
			captured <- frame
		}
	}()

	c := newClient(nil, sinW, nil)
	defer c.close()

	owned := &ownedSession{
		sessionID:        "s-appr-acp",
		workspace:        root,
		status:           "idle",
		client:           c,
		pendingApprovals: make(map[string]*pendingApproval),
		approvalKinds:    make(map[string]string),
	}
	adapter.sessions["s-appr-acp"] = owned

	// --- 1. Test Accept with Numeric ID ---
	permFrameNumeric := map[string]any{
		"jsonrpc": "2.0",
		"id":      float64(101),
		"method":  "session/request_permission",
		"params": map[string]any{
			"sessionId": "s-appr-acp",
			"options": []any{
				map[string]any{"optionId": "opt-deny", "kind": "reject_once"},
				map[string]any{"optionId": "opt-allow-once", "kind": "allow_once"},
				map[string]any{"optionId": "opt-allow-always", "kind": "allow_always"},
			},
			"toolCall": map[string]any{"tool": "bash", "command": "ls"},
		},
	}
	adapter.handleFrameNotification(owned, "s-appr-acp", permFrameNumeric)

	owned.mu.Lock()
	if owned.status != "waiting_approval" {
		t.Fatalf("expected waiting_approval, got %s", owned.status)
	}
	owned.mu.Unlock()

	// session.approval.read returns mode and pending native parameters
	readRes, err := adapter.Command(context.Background(), core.Request{
		SessionID: "s-appr-acp",
		Action:    "session.approval.read",
	})
	if err != nil {
		t.Fatalf("session.approval.read failed: %v", err)
	}
	if readRes.Payload["mode"] != "manual" {
		t.Fatalf("expected mode manual, got %v", readRes.Payload["mode"])
	}
	pendingList, _ := readRes.Payload["pending"].([]map[string]any)
	if len(pendingList) != 1 || pendingList[0]["approval_id"] != "grok:101" {
		t.Fatalf("expected pending grok:101, got: %#v", pendingList)
	}

	// Approve accept
	_, err = adapter.Command(context.Background(), core.Request{
		SessionID: "s-appr-acp",
		Action:    "session.approve",
		Fields: map[string]any{
			"approval_id": "grok:101",
			"decision":    "accept",
		},
	})
	if err != nil {
		t.Fatalf("approve failed: %v", err)
	}

	select {
	case frame := <-captured:
		if frame["id"] != float64(101) {
			t.Fatalf("numeric id type not preserved: %#v", frame["id"])
		}
		result, ok := frame["result"].(map[string]any)
		if !ok {
			t.Fatalf("result not map: %#v", frame)
		}
		outcome, _ := result["outcome"].(map[string]any)
		if outcome["outcome"] != "selected" || outcome["optionId"] != "opt-allow-once" {
			t.Fatalf("expected selected opt-allow-once, got: %#v", outcome)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for approve frame")
	}

	// Verify grok.approval_resolved emitted
	emitMu.Lock()
	var resolvedEvent map[string]any
	for _, e := range emitted {
		if e["event"] == "grok.approval_resolved" {
			resolvedEvent = e
			break
		}
	}
	emitMu.Unlock()
	if resolvedEvent == nil {
		t.Fatalf("grok.approval_resolved was not emitted: %#v", emitted)
	}
	if resolvedEvent["payload"].(map[string]any)["approval_id"] != "grok:101" {
		t.Fatalf("unexpected payload: %#v", resolvedEvent)
	}

	// --- 2. Test Decline with String ID ---
	permFrameString := map[string]any{
		"jsonrpc": "2.0",
		"id":      "req-str-202",
		"method":  "session/request_permission",
		"params": map[string]any{
			"sessionId": "s-appr-acp",
			"options": []any{
				map[string]any{"optionId": "opt-deny-1", "kind": "reject_once"},
				map[string]any{"optionId": "opt-allow-1", "kind": "allow_once"},
			},
		},
	}
	adapter.handleFrameNotification(owned, "s-appr-acp", permFrameString)

	_, err = adapter.Command(context.Background(), core.Request{
		SessionID: "s-appr-acp",
		Action:    "session.approve",
		Fields: map[string]any{
			"approval_id": "grok:req-str-202",
			"decision":    "decline",
		},
	})
	if err != nil {
		t.Fatalf("decline failed: %v", err)
	}

	select {
	case frame := <-captured:
		if frame["id"] != "req-str-202" {
			t.Fatalf("string id type not preserved: %#v", frame["id"])
		}
		result := frame["result"].(map[string]any)
		outcome := result["outcome"].(map[string]any)
		if outcome["outcome"] != "selected" || outcome["optionId"] != "opt-deny-1" {
			t.Fatalf("expected selected opt-deny-1, got: %#v", outcome)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for decline frame")
	}

	// --- 3. Test Cancelled with cancelled=true ---
	permFrameCancel := map[string]any{
		"jsonrpc": "2.0",
		"id":      "req-cancel-303",
		"method":  "session/request_permission",
		"params": map[string]any{
			"sessionId": "s-appr-acp",
			"options": []any{
				map[string]any{"optionId": "opt-deny-1", "kind": "reject_once"},
			},
		},
	}
	adapter.handleFrameNotification(owned, "s-appr-acp", permFrameCancel)

	_, err = adapter.Command(context.Background(), core.Request{
		SessionID: "s-appr-acp",
		Action:    "session.approve",
		Fields: map[string]any{
			"approval_id": "grok:req-cancel-303",
			"decision":    "decline",
			"cancelled":   true,
		},
	})
	if err != nil {
		t.Fatalf("cancel approve failed: %v", err)
	}

	select {
	case frame := <-captured:
		if frame["id"] != "req-cancel-303" {
			t.Fatalf("string id type not preserved: %#v", frame["id"])
		}
		result := frame["result"].(map[string]any)
		outcome := result["outcome"].(map[string]any)
		if outcome["outcome"] != "cancelled" {
			t.Fatalf("expected outcome: cancelled, got: %#v", outcome)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for cancel frame")
	}

	// --- 4. Test Codex Compatibility ---
	codexFrame := map[string]any{
		"jsonrpc": "2.0",
		"id":      float64(404),
		"method":  "item/permissions/requestApproval",
		"params": map[string]any{
			"sessionId": "s-appr-acp",
		},
	}
	adapter.handleFrameNotification(owned, "s-appr-acp", codexFrame)

	_, err = adapter.Command(context.Background(), core.Request{
		SessionID: "s-appr-acp",
		Action:    "session.approve",
		Fields: map[string]any{
			"approval_id": "grok:404",
			"decision":    "accept",
			"permissions": map[string]any{"write": true},
		},
	})
	if err != nil {
		t.Fatalf("codex approve failed: %v", err)
	}

	select {
	case frame := <-captured:
		if frame["id"] != float64(404) {
			t.Fatalf("numeric id type not preserved: %#v", frame["id"])
		}
		result := frame["result"].(map[string]any)
		if result["scope"] != "turn" {
			t.Fatalf("expected scope turn for codex approval: %#v", result)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for codex frame")
	}
}

func TestGrokQuerySessionList_CapabilityBranching(t *testing.T) {
	root := "/grok-probe"

	// Subtest 1: Capability present -> Calls session/list and returns real sessions
	t.Run("CapabilityPresent", func(t *testing.T) {
		sessionListCalled := false
		starter := func(ctx context.Context, spec ssh.CommandSpec) (SSHSession, error) {
			sinR, sinW := io.Pipe()
			soutR, soutW := io.Pipe()
			sess := &fakeGrokSSHSession{
				stdinR: sinR, stdinW: sinW, stdoutR: soutR, stdoutW: soutW, done: make(chan struct{}),
			}
			go func() {
				defer close(sess.done)
				dec := json.NewDecoder(sinR)
				enc := json.NewEncoder(soutW)
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
								"agentCapabilities": map[string]any{
									"sessionCapabilities": map[string]any{
										"list": map[string]any{},
									},
								},
							},
						})
					case "session/list":
						sessionListCalled = true
						_ = enc.Encode(map[string]any{
							"jsonrpc": "2.0",
							"id":      id,
							"result": map[string]any{
								"sessions": []any{
									map[string]any{"sessionId": "sess-list-1"},
								},
							},
						})
					default:
						_ = enc.Encode(map[string]any{"jsonrpc": "2.0", "id": id, "result": map[string]any{}})
					}
				}
			}()
			return sess, nil
		}

		adapter := NewWithSSHStarter(Config{Workspace: root, Allowed: []string{root}}, starter)
		res, err := adapter.Query(context.Background(), core.Request{
			Fields: map[string]any{
				"method": "session/list",
				"cwd":    root,
			},
		})
		if err != nil {
			t.Fatalf("Query session/list failed: %v", err)
		}
		if !sessionListCalled {
			t.Fatal("expected session/list to be called on native ACP")
		}
		rawResult, ok := res["result"].(map[string]any)
		if !ok {
			rawResult = res
		}
		sessions, ok := rawResult["sessions"].([]any)
		if !ok || len(sessions) != 1 {
			t.Fatalf("unexpected sessions output: %#v", res)
		}
	})

	// Subtest 2: Capability absent -> Errors explicitly without calling session/list
	t.Run("CapabilityAbsent", func(t *testing.T) {
		sessionListCalled := false
		starter := func(ctx context.Context, spec ssh.CommandSpec) (SSHSession, error) {
			sinR, sinW := io.Pipe()
			soutR, soutW := io.Pipe()
			sess := &fakeGrokSSHSession{
				stdinR: sinR, stdinW: sinW, stdoutR: soutR, stdoutW: soutW, done: make(chan struct{}),
			}
			go func() {
				defer close(sess.done)
				dec := json.NewDecoder(sinR)
				enc := json.NewEncoder(soutW)
				for {
					var req map[string]any
					if err := dec.Decode(&req); err != nil {
						return
					}
					id := req["id"]
					method, _ := req["method"].(string)
					switch method {
					case "initialize":
						// sessionCapabilities does NOT include list
						_ = enc.Encode(map[string]any{
							"jsonrpc": "2.0",
							"id":      id,
							"result": map[string]any{
								"protocolVersion": 1,
								"agentCapabilities": map[string]any{
									"sessionCapabilities": map[string]any{
										"resume": map[string]any{},
									},
								},
							},
						})
					case "session/list":
						sessionListCalled = true
						_ = enc.Encode(map[string]any{"jsonrpc": "2.0", "id": id, "result": map[string]any{}})
					default:
						_ = enc.Encode(map[string]any{"jsonrpc": "2.0", "id": id, "result": map[string]any{}})
					}
				}
			}()
			return sess, nil
		}

		adapter := NewWithSSHStarter(Config{Workspace: root, Allowed: []string{root}}, starter)
		_, err := adapter.Query(context.Background(), core.Request{
			Fields: map[string]any{
				"method": "session/list",
				"cwd":    root,
			},
		})
		if err == nil || !strings.Contains(err.Error(), "session/list capability") {
			t.Fatalf("expected session/list capability error, got: %v", err)
		}
		if sessionListCalled {
			t.Fatal("session/list was called despite missing capability")
		}
	})
}

func TestGrokAttachments_TextBase64AndImageCapability(t *testing.T) {
	// 1. Image capability missing -> explicit error
	capsWithoutImage := promptCaps{image: false}
	imgAtt := map[string]any{
		"media_type":     "image/png",
		"content_base64": base64.StdEncoding.EncodeToString([]byte("fake-image")),
	}
	_, err := buildPromptBlocks("look at image", []any{imgAtt}, capsWithoutImage)
	if err == nil || !strings.Contains(err.Error(), "image prompt capabilities") {
		t.Fatalf("expected image capabilities error, got: %v", err)
	}

	// 2. Non-image attachment base64 decoded to text
	txtAtt := map[string]any{
		"name":           "hello.txt",
		"media_type":     "text/plain",
		"content_base64": base64.StdEncoding.EncodeToString([]byte("content of file")),
	}
	blocks, err := buildPromptBlocks("review this", []any{txtAtt}, capsWithoutImage)
	if err != nil {
		t.Fatalf("buildPromptBlocks failed: %v", err)
	}
	found := false
	for _, b := range blocks {
		if text, ok := b["text"].(string); ok && strings.Contains(text, "content of file") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("base64 text attachment was not decoded into text block: %#v", blocks)
	}

	// 3. Binary attachment without text -> explicit rejection, not silently lost
	binAtt := map[string]any{
		"name":           "binary.dat",
		"media_type":     "application/octet-stream",
		"content_base64": base64.StdEncoding.EncodeToString([]byte{0xff, 0xfe, 0xfd, 0x00}),
	}
	_, err = buildPromptBlocks("check binary", []any{binAtt}, capsWithoutImage)
	if err == nil || !strings.Contains(err.Error(), "binary") {
		t.Fatalf("expected binary rejection error, got: %v", err)
	}
}

func TestGrokHistoryPage_IsolatedReplay(t *testing.T) {
	root := "/grok-probe"
	starter := func(ctx context.Context, spec ssh.CommandSpec) (SSHSession, error) {
		sinR, sinW := io.Pipe()
		soutR, soutW := io.Pipe()
		sess := &fakeGrokSSHSession{
			stdinR: sinR, stdinW: sinW, stdoutR: soutR, stdoutW: soutW, done: make(chan struct{}),
		}
		go func() {
			defer close(sess.done)
			dec := json.NewDecoder(sinR)
			enc := json.NewEncoder(soutW)
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
					// Replay notifications then reply
					_ = enc.Encode(map[string]any{
						"jsonrpc": "2.0",
						"method":  "session/update",
						"params": map[string]any{
							"sessionId": "s-hist-1",
							"update": map[string]any{
								"item": map[string]any{
									"role":    "user",
									"content": "first user question",
									"_meta": map[string]any{
										"astrorderItemId": "item-u1",
									},
								},
							},
						},
					})
					_ = enc.Encode(map[string]any{
						"jsonrpc": "2.0",
						"method":  "session/update",
						"params": map[string]any{
							"sessionId": "s-hist-1",
							"update": map[string]any{
								"item": map[string]any{
									"role":    "assistant",
									"content": "assistant reply",
									"_meta": map[string]any{
										"eventId": "ev-a1",
									},
								},
							},
						},
					})
					_ = enc.Encode(map[string]any{
						"jsonrpc": "2.0",
						"id":      id,
						"result": map[string]any{
							"sessionId": "s-hist-1",
						},
					})
				default:
					_ = enc.Encode(map[string]any{"jsonrpc": "2.0", "id": id, "result": map[string]any{}})
				}
			}
		}()
		return sess, nil
	}

	adapter := NewWithSSHStarter(Config{Workspace: root, Allowed: []string{root}}, starter)
	pageRes, err := adapter.handleHistoryPage(context.Background(), nil, "s-hist-1", core.Request{
		Fields: map[string]any{"limit": 10},
	})
	if err != nil {
		t.Fatalf("handleHistoryPage failed: %v", err)
	}

	items, ok := pageRes.Payload["items"].([]any)
	if !ok || len(items) != 2 {
		t.Fatalf("expected 2 items, got: %#v", pageRes.Payload)
	}
	item0 := items[0].(map[string]any)
	if item0["id"] != "item-u1" {
		t.Fatalf("expected stable id item-u1, got %v", item0["id"])
	}
	meta0 := item0["_meta"].(map[string]any)
	if meta0["astrorderItemId"] != "item-u1" {
		t.Fatalf("expected _meta.astrorderItemId item-u1, got %v", meta0["astrorderItemId"])
	}

	item1 := items[1].(map[string]any)
	if item1["id"] != "ev-a1" {
		t.Fatalf("expected stable id ev-a1 from eventId, got %v", item1["id"])
	}
}

func TestGrokSettingsSync(t *testing.T) {
	owned := &ownedSession{
		sessionID: "s-sync",
		status:    "idle",
	}
	adapter := New(Config{})

	// available_commands_update
	adapter.handleFrameNotification(owned, "s-sync", map[string]any{
		"method": "session/update",
		"params": map[string]any{
			"sessionId": "s-sync",
			"update": map[string]any{
				"sessionUpdate": "available_commands_update",
				"availableCommands": []any{
					map[string]any{"name": "/help", "description": "show help"},
				},
			},
		},
	})
	owned.mu.Lock()
	if len(owned.commands) != 1 || owned.commands[0]["name"] != "help" {
		t.Fatalf("commands not synced: %#v", owned.commands)
	}
	owned.mu.Unlock()

	// config_option_update
	adapter.handleFrameNotification(owned, "s-sync", map[string]any{
		"method": "session/update",
		"params": map[string]any{
			"sessionId": "s-sync",
			"update": map[string]any{
				"sessionUpdate": "config_option_update",
				"configId":      "reasoning_effort",
				"value":         "high",
			},
		},
	})
	owned.mu.Lock()
	if owned.effort != "high" {
		t.Fatalf("effort not synced: %s", owned.effort)
	}
	owned.mu.Unlock()

	// current_mode_update
	adapter.handleFrameNotification(owned, "s-sync", map[string]any{
		"method": "session/update",
		"params": map[string]any{
			"sessionId": "s-sync",
			"update": map[string]any{
				"sessionUpdate": "current_mode_update",
				"model_id":      "grok-4.6",
			},
		},
	})
	owned.mu.Lock()
	if owned.model != "grok-4.6" {
		t.Fatalf("model not synced: %s", owned.model)
	}
	owned.mu.Unlock()
}
