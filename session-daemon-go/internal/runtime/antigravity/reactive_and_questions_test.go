package antigravity

import (
	"bufio"
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	core "astrorder.dev/session-daemon/internal/runtime"
	"google.golang.org/protobuf/encoding/protowire"
)

func serveReactiveQuestionMockACP(r io.Reader, w io.Writer, homeDir string) {
	var writeMu sync.Mutex
	sendJSON := func(v map[string]any) {
		raw, _ := json.Marshal(v)
		raw = append(raw, '\n')
		writeMu.Lock()
		_, _ = w.Write(raw)
		writeMu.Unlock()
	}

	setNotFullyIdle := func(sid string, active int) {
		if homeDir == "" {
			return
		}
		dbDir := filepath.Join(homeDir, ".gemini", "antigravity-cli")
		_ = os.MkdirAll(dbDir, 0o755)
		db, err := sql.Open("sqlite", filepath.Join(dbDir, "conversation_summaries.db"))
		if err != nil {
			return
		}
		defer db.Close()
		_, _ = db.Exec(`CREATE TABLE IF NOT EXISTS conversation_summaries (
			conversation_id TEXT PRIMARY KEY,
			title TEXT,
			updated_at TEXT,
			workspace_uris TEXT,
			not_fully_idle INTEGER,
			killed INTEGER
		)`)
		_, _ = db.Exec(`INSERT INTO conversation_summaries(conversation_id,title,updated_at,workspace_uris,not_fully_idle,killed)
			VALUES(?,'开发','2026-11-18T09:37:54Z','[]',?,0)
			ON CONFLICT(conversation_id) DO UPDATE SET not_fully_idle=excluded.not_fully_idle`, sid, active)
	}

	const sid = "22222222-2222-4222-8222-222222222222"
	scan := bufio.NewScanner(r)
	scan.Buffer(make([]byte, 65536), 4_000_000)
	for scan.Scan() {
		line := bytes.TrimSpace(scan.Bytes())
		if len(line) == 0 {
			continue
		}
		var msg map[string]any
		if json.Unmarshal(line, &msg) != nil {
			return
		}
		method := str(msg, "method")
		rawID := msg["id"]
		params, _ := msg["params"].(map[string]any)

		switch method {
		case "initialize":
			sendJSON(map[string]any{
				"jsonrpc": "2.0",
				"id":      rawID,
				"result": map[string]any{
					"protocolVersion":   1,
					"agentCapabilities": map[string]any{"promptCapabilities": map[string]any{"image": true}},
				},
			})
		case "session/new":
			sendJSON(map[string]any{
				"jsonrpc": "2.0",
				"id":      rawID,
				"result":  map[string]any{"sessionId": sid},
			})
		case "session/prompt":
			var text string
			if blocks, ok := params["prompt"].([]any); ok {
				for _, b := range blocks {
					if m, ok := b.(map[string]any); ok {
						text += str(m, "text")
					}
				}
			}
			switch {
			case strings.Contains(text, "ASK_QUESTION"):
				// 1. in_progress tool_call
				sendJSON(map[string]any{
					"jsonrpc": "2.0",
					"method":  "session/update",
					"params": map[string]any{
						"sessionId": sid,
						"update": map[string]any{
							"sessionUpdate": "tool_call",
							"toolCallId":    "call-ask-1",
							"title":         "ask_question",
							"kind":          "other",
							"status":        "in_progress",
							"rawInput": map[string]any{
								"questions": []map[string]any{
									{
										"question":        "选择展示方式？",
										"options":         []string{"新增直连标签页", "合并总览"},
										"is_multi_select": true,
									},
								},
							},
						},
					},
				})
				// 2. pending tool_call_update followed immediately by end_turn
				sendJSON(map[string]any{
					"jsonrpc": "2.0",
					"method":  "session/update",
					"params": map[string]any{
						"sessionId": sid,
						"update": map[string]any{
							"sessionUpdate": "tool_call_update",
							"toolCallId":    "call-ask-1",
							"title":         "ask_question",
							"kind":          "other",
							"status":        "pending",
						},
					},
				})
				sendJSON(map[string]any{
					"jsonrpc": "2.0",
					"id":      rawID,
					"result":  map[string]any{"stopReason": "end_turn"},
				})

			case strings.Contains(text, "ANSWER_QUESTION"):
				// agy completes the previous pending ask_question without repeating rawInput/title
				sendJSON(map[string]any{
					"jsonrpc": "2.0",
					"method":  "session/update",
					"params": map[string]any{
						"sessionId": sid,
						"update": map[string]any{
							"sessionUpdate": "tool_call_update",
							"toolCallId":    "call-ask-1",
							"status":        "completed",
						},
					},
				})
				sendJSON(map[string]any{
					"jsonrpc": "2.0",
					"method":  "session/update",
					"params": map[string]any{
						"sessionId": sid,
						"update": map[string]any{
							"sessionUpdate": "agent_message_chunk",
							"content":       map[string]any{"type": "text", "text": "收到选择，开始实现。"},
						},
					},
				})
				sendJSON(map[string]any{
					"jsonrpc": "2.0",
					"id":      rawID,
					"result":  map[string]any{"stopReason": "end_turn"},
				})

			case strings.Contains(text, "BG_BUILD"):
				setNotFullyIdle(sid, 1)
				sendJSON(map[string]any{
					"jsonrpc": "2.0",
					"method":  "session/update",
					"params": map[string]any{
						"sessionId": sid,
						"update": map[string]any{
							"sessionUpdate": "tool_call",
							"toolCallId":    "call-bg-1",
							"name":          "run_command",
							"status":        "completed",
							"rawInput":      map[string]any{"CommandLine": "npm run build"},
							"rawOutput":     "Tool is running as a background task with task id: " + sid + "/task-1",
						},
					},
				})
				sendJSON(map[string]any{
					"jsonrpc": "2.0",
					"method":  "session/update",
					"params": map[string]any{
						"sessionId": sid,
						"update": map[string]any{
							"sessionUpdate": "agent_message_chunk",
							"content":       map[string]any{"type": "text", "text": "正在编译前后端产物，完成后继续发布。"},
						},
					},
				})
				// Return session/prompt early while background task is still running!
				sendJSON(map[string]any{
					"jsonrpc": "2.0",
					"id":      rawID,
					"result":  map[string]any{"stopReason": "end_turn"},
				})
				// Simulate background task finishing 450ms later and waking agy acp reactively
				go func() {
					time.Sleep(450 * time.Millisecond)
					sendJSON(map[string]any{
						"jsonrpc": "2.0",
						"method":  "session/update",
						"params": map[string]any{
							"sessionId": sid,
							"update": map[string]any{
								"sessionUpdate": "agent_thought_chunk",
								"content":       map[string]any{"type": "text", "text": "编译已完成，开始执行最终校验"},
							},
						},
					})
					sendJSON(map[string]any{
						"jsonrpc": "2.0",
						"method":  "session/update",
						"params": map[string]any{
							"sessionId": sid,
							"update": map[string]any{
								"sessionUpdate": "agent_message_chunk",
								"content":       map[string]any{"type": "text", "text": "全部编译与发布已完成。"},
							},
						},
					})
					time.Sleep(100 * time.Millisecond)
					setNotFullyIdle(sid, 0)
				}()
			}
		}
	}
}

func TestReactiveQuestionHelper(t *testing.T) {
	if os.Getenv("ASTRORDER_AGY_RQ_HELPER") != "1" {
		return
	}
	serveReactiveQuestionMockACP(os.Stdin, os.Stdout, os.Getenv("ASTRORDER_AGY_TEST_HOME"))
	os.Exit(0)
}

func TestAskQuestionPendingAndBackgroundReactiveLifecycle(t *testing.T) {
	exe, _ := os.Executable()
	home := t.TempDir()
	ws := t.TempDir()

	var mu sync.Mutex
	var events []map[string]any
	a := New(Config{
		Executable:  exe,
		Arguments:   []string{"-test.run=TestReactiveQuestionHelper", "--"},
		Environment: []string{"ASTRORDER_AGY_RQ_HELPER=1", "ASTRORDER_AGY_TEST_HOME=" + home},
		Workspace:   ws,
		Allowed:     []string{ws},
		Home:        home,
		AgentID:     "local-antigravity",
		Emit: func(id, event string, p map[string]any, status string) error {
			mu.Lock()
			defer mu.Unlock()
			events = append(events, map[string]any{"event": event, "payload": p, "status": status})
			return nil
		},
	})
	defer a.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()

	created, err := a.Create(ctx, core.Request{})
	if err != nil {
		t.Fatal(err)
	}

	// Part 1: ask_question pauses cleanly with awaiting_input=true and async question message.
	if _, err := a.Command(ctx, core.Request{
		SessionID: created.SessionID,
		Action:    "session.send",
		Fields:    map[string]any{"text": "ASK_QUESTION", "command_id": "cmd-ask"},
	}); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(4 * time.Second)
	for a.Snapshot()[created.SessionID].Status == "running" && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if got := a.Snapshot()[created.SessionID].Status; got != "idle" {
		t.Fatalf("expected idle while awaiting async question input, got %s", got)
	}

	mu.Lock()
	var sawAsyncQuestion, sawAwaitingCmd, sawRawAskTool bool
	for _, ev := range events {
		p, _ := ev["payload"].(map[string]any)
		if str(p, "type") == "message.upsert" {
			data, _ := p["data"].(map[string]any)
			if str(data, "delivery") == "async" && str(data, "role") == "assistant" {
				sawAsyncQuestion = true
				qs, _ := data["questions"].([]map[string]any)
				if len(qs) != 1 || qs[0]["multi_select"] != true {
					t.Fatalf("expected multi_select async question, got %#v", data)
				}
			}
			if str(data, "role") == "tool" {
				toolMap, _ := data["tool"].(map[string]any)
				if str(toolMap, "name") == "ask_question" {
					sawRawAskTool = true
				}
			}
		}
		if str(p, "type") == "command.upsert" {
			data, _ := p["data"].(map[string]any)
			if str(data, "id") == "cmd-ask" && str(data, "state") == "completed" && data["awaiting_input"] == true {
				sawAwaitingCmd = true
			}
		}
	}
	events = nil
	mu.Unlock()

	if !sawAsyncQuestion || !sawAwaitingCmd || sawRawAskTool {
		t.Fatalf("ask_question mismatch: async=%v awaitingCmd=%v rawTool=%v", sawAsyncQuestion, sawAwaitingCmd, sawRawAskTool)
	}

	// Answering ask_question must not emit an orphan generic tool message.
	if _, err := a.Command(ctx, core.Request{
		SessionID: created.SessionID,
		Action:    "session.send",
		Fields:    map[string]any{"text": "ANSWER_QUESTION", "command_id": "cmd-answer"},
	}); err != nil {
		t.Fatal(err)
	}
	for a.Snapshot()[created.SessionID].Status == "running" && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}

	mu.Lock()
	for _, ev := range events {
		p, _ := ev["payload"].(map[string]any)
		if str(p, "type") == "message.upsert" {
			data, _ := p["data"].(map[string]any)
			if str(data, "role") == "tool" {
				t.Fatalf("unexpected orphan tool message on ask_question completion: %#v", data)
			}
		}
	}
	events = nil
	mu.Unlock()

	// Part 2: Background task keeps session running across intermediate reply until reactive wakeup finishes.
	if _, err := a.Command(ctx, core.Request{
		SessionID: created.SessionID,
		Action:    "session.send",
		Fields:    map[string]any{"text": "BG_BUILD", "command_id": "cmd-bg"},
	}); err != nil {
		t.Fatal(err)
	}

	// Wait 200ms: session/prompt has already returned after "正在编译前后端产物...",
	// but task-1 is still running (not_fully_idle=1), so session MUST stay running and cmd-bg MUST NOT complete yet!
	time.Sleep(200 * time.Millisecond)
	if got := a.Snapshot()[created.SessionID].Status; got != "running" {
		t.Fatalf("session prematurely became %s while background task was still running", got)
	}
	mu.Lock()
	for _, ev := range events {
		p, _ := ev["payload"].(map[string]any)
		if str(p, "type") == "command.upsert" {
			data, _ := p["data"].(map[string]any)
			if str(data, "id") == "cmd-bg" && str(data, "state") == "completed" {
				mu.Unlock()
				t.Fatalf("premature command.upsert completed while background task still active: %#v", data)
			}
		}
	}
	mu.Unlock()

	// Wait for reactive wakeup to complete and clear not_fully_idle=0.
	deadline = time.Now().Add(6 * time.Second)
	for a.Snapshot()[created.SessionID].Status == "running" && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if got := a.Snapshot()[created.SessionID].Status; got != "idle" {
		t.Fatalf("session did not settle to idle after reactive turn finished: %s", got)
	}

	mu.Lock()
	defer mu.Unlock()
	completedCount := 0
	msgIDs := map[string]string{}
	for _, ev := range events {
		p, _ := ev["payload"].(map[string]any)
		if str(p, "type") == "command.upsert" {
			data, _ := p["data"].(map[string]any)
			if str(data, "id") == "cmd-bg" && str(data, "state") == "completed" {
				completedCount++
			}
		}
		if str(p, "type") == "message.upsert" {
			data, _ := p["data"].(map[string]any)
			if str(data, "role") == "assistant" && str(data, "text") != "" {
				msgIDs[str(data, "id")] = str(data, "text")
			}
		}
	}
	if completedCount != 1 {
		t.Fatalf("expected exactly 1 terminal command.upsert after reactive completion, got %d", completedCount)
	}
	if len(msgIDs) < 2 {
		t.Fatalf("expected distinct live assistant messages across initial + reactive turns, got %#v", msgIDs)
	}
}

func TestDecodeStepAskQuestionPendingAndCompleted(t *testing.T) {
	sid := "33333333-3333-4333-8333-333333333333"
	rawQuestions := `[{"question":"统计范围？","options":["(Recommended) 全部直连","仅 Antigravity"],"is_multi_select":false}]`

	var metaTool []byte
	metaTool = protowire.AppendTag(metaTool, 2, protowire.BytesType)
	metaTool = protowire.AppendString(metaTool, "ask_question")

	var meta []byte
	meta = protowire.AppendTag(meta, 4, protowire.BytesType)
	meta = protowire.AppendBytes(meta, metaTool)

	var kv []byte
	kv = protowire.AppendTag(kv, 1, protowire.BytesType)
	kv = protowire.AppendString(kv, "questions")
	kv = protowire.AppendTag(kv, 2, protowire.BytesType)
	kv = protowire.AppendString(kv, rawQuestions)

	var d140 []byte
	d140 = protowire.AppendTag(d140, 1, protowire.BytesType)
	d140 = protowire.AppendBytes(d140, kv)

	var step []byte
	step = protowire.AppendTag(step, 1, protowire.VarintType)
	step = protowire.AppendVarint(step, 99)
	step = protowire.AppendTag(step, 140, protowire.BytesType)
	step = protowire.AppendBytes(step, d140)

	pendingItem, err := decodeStep(sid, 10, 99, 1, step, meta)
	if err != nil {
		t.Fatal(err)
	}
	if pendingItem["role"] != "assistant" || pendingItem["kind"] != "message" || pendingItem["delivery"] != "async" || pendingItem["answered"] != false {
		t.Fatalf("unexpected pending ask_question item: %#v", pendingItem)
	}

	completedItem, err := decodeStep(sid, 10, 99, 3, step, meta)
	if err != nil {
		t.Fatal(err)
	}
	if completedItem["answered"] != true {
		t.Fatalf("expected completed ask_question to mark answered=true, got %#v", completedItem)
	}
}
