package hermes

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	core "astrorder.dev/session-daemon/internal/runtime"
)

func TestHermesAdapterOwnsMultipleSessions(t *testing.T) {
	if os.Getenv("ASTRORDER_HERMES_HELPER") == "1" {
		fakeHermesHelper()
		os.Exit(0)
	}

	root := t.TempDir()
	emitted := make(chan map[string]any, 20)

	cfg := Config{
		Executable:  os.Args[0],
		Arguments:   []string{"-test.run=TestHermesAdapterOwnsMultipleSessions"},
		Environment: []string{"ASTRORDER_HERMES_HELPER=1"},
		Workspace:   root,
		Allowed:     []string{root},
		AgentID:     "test-hermes",
		Emit: func(sessionID, event string, payload map[string]any, status string) error {
			emitted <- map[string]any{
				"session_id": sessionID,
				"event":      event,
				"payload":    payload,
				"status":     status,
			}
			return nil
		},
	}

	adapter := New(cfg)
	defer adapter.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	first, err := adapter.Create(ctx, core.Request{
		Fields: map[string]any{"cwd": root, "title": "First Session"},
	})
	if err != nil {
		t.Fatalf("first create failed: %v", err)
	}
	if first.SessionID == "" {
		t.Fatal("empty first session identity")
	}

	second, err := adapter.Create(ctx, core.Request{
		Fields: map[string]any{"cwd": root, "title": "Second Session"},
	})
	if err != nil {
		t.Fatalf("second create failed: %v", err)
	}
	if first.SessionID == second.SessionID {
		t.Fatalf("sessions shared identity: %s", first.SessionID)
	}

	// session.send
	sent, err := adapter.Command(ctx, core.Request{
		SessionID: first.SessionID,
		Action:    "session.send",
		Fields:    map[string]any{"text": "hello hermes"},
	})
	if err != nil {
		t.Fatalf("send failed: %v", err)
	}
	if sent.Status != "running" || sent.Payload["accepted"] != true {
		t.Fatalf("unexpected send result: %+v", sent)
	}

	// Wait for message.complete event
	var completed bool
	timer := time.After(3 * time.Second)
waitLoop:
	for {
		select {
		case ev := <-emitted:
			if ev["event"] == "hermes.command_complete" && ev["session_id"] == first.SessionID {
				completed = true
				break waitLoop
			}
		case <-timer:
			break waitLoop
		}
	}
	if !completed {
		t.Fatal("never received hermes.command_complete")
	}

	// Snapshot check
	snap := adapter.Snapshot()
	if snap[first.SessionID].Status != "idle" || snap[second.SessionID].Status != "idle" {
		t.Fatalf("unexpected snapshot: %+v", snap)
	}

	// session.rename
	renamed, err := adapter.Command(ctx, core.Request{
		SessionID: first.SessionID,
		Action:    "session.rename",
		Fields:    map[string]any{"title": "Renamed Session"},
	})
	if err != nil || renamed.Payload["title"] != "Renamed Session" {
		t.Fatalf("rename failed: %v, %+v", err, renamed)
	}

	// session.models
	modelsRes, err := adapter.Command(ctx, core.Request{
		SessionID: first.SessionID,
		Action:    "session.models",
	})
	if err != nil {
		t.Fatalf("models failed: %v", err)
	}
	items, ok := modelsRes.Payload["items"].([]map[string]string)
	if !ok || len(items) == 0 {
		t.Fatalf("unexpected models: %+v", modelsRes)
	}

	// session.commands
	cmdRes, err := adapter.Command(ctx, core.Request{
		SessionID: first.SessionID,
		Action:    "session.commands",
	})
	if err != nil {
		t.Fatalf("commands failed: %v", err)
	}
	if len(cmdRes.Payload["items"].([]map[string]any)) == 0 {
		t.Fatalf("unexpected commands payload: %+v", cmdRes)
	}

	// session.model.read
	modelRead, err := adapter.Command(ctx, core.Request{
		SessionID: first.SessionID,
		Action:    "session.model.read",
	})
	if err != nil || modelRead.Payload["model"] != "test-model" {
		t.Fatalf("model.read failed: %v, %+v", err, modelRead)
	}

	// session.model.set
	modelSet, err := adapter.Command(ctx, core.Request{
		SessionID: first.SessionID,
		Action:    "session.model.set",
		Fields:    map[string]any{"provider": "test-provider", "model": "test-model-2"},
	})
	if err != nil || modelSet.Payload["model"] != "test-model-2" {
		t.Fatalf("model.set failed: %v, %+v", err, modelSet)
	}

	// session.reasoning.set
	reasoningSet, err := adapter.Command(ctx, core.Request{
		SessionID: first.SessionID,
		Action:    "session.reasoning.set",
		Fields:    map[string]any{"effort": "high"},
	})
	if err != nil || reasoningSet.Payload["effort"] != "high" {
		t.Fatalf("reasoning.set failed: %v, %+v", err, reasoningSet)
	}

	// session.approval.read
	apprRead, err := adapter.Command(ctx, core.Request{
		SessionID: first.SessionID,
		Action:    "session.approval.read",
	})
	if err != nil || apprRead.Payload["mode"] != "auto" {
		t.Fatalf("approval.read failed: %v, %+v", err, apprRead)
	}

	// session.approval.set
	apprSet, err := adapter.Command(ctx, core.Request{
		SessionID: first.SessionID,
		Action:    "session.approval.set",
		Fields:    map[string]any{"mode": "manual"},
	})
	if err != nil || apprSet.Payload["mode"] != "manual" {
		t.Fatalf("approval.set failed: %v, %+v", err, apprSet)
	}

	// session.delete
	deleted, err := adapter.Command(ctx, core.Request{
		SessionID: second.SessionID,
		Action:    "session.delete",
	})
	if err != nil || deleted.Payload["deleted"] != second.SessionID {
		t.Fatalf("delete failed: %v, %+v", err, deleted)
	}

	// Second session must no longer be owned
	_, err = adapter.Command(ctx, core.Request{
		SessionID: second.SessionID,
		Action:    "session.send",
		Fields:    map[string]any{"text": "should fail"},
	})
	if err == nil {
		t.Fatal("deleted session was still owned")
	}
}

func TestHermesLongPromptInterrupt(t *testing.T) {
	if os.Getenv("ASTRORDER_HERMES_HELPER") == "1" {
		fakeHermesHelper()
		os.Exit(0)
	}

	root := t.TempDir()
	adapter := New(Config{
		Executable:  os.Args[0],
		Arguments:   []string{"-test.run=TestHermesLongPromptInterrupt"},
		Environment: []string{"ASTRORDER_HERMES_HELPER=1"},
		Workspace:   root,
		Allowed:     []string{root},
		AgentID:     "test-hermes",
	})
	defer adapter.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	sess, err := adapter.Create(ctx, core.Request{
		Fields: map[string]any{"cwd": root, "title": "Interrupt Session"},
	})
	if err != nil {
		t.Fatal(err)
	}

	sent, err := adapter.Command(ctx, core.Request{
		SessionID: sess.SessionID,
		Action:    "session.send",
		Fields:    map[string]any{"text": "slow:sleep-long"},
	})
	if err != nil || sent.Status != "running" {
		t.Fatalf("send failed: %v, %+v", err, sent)
	}

	interrupted, err := adapter.Command(ctx, core.Request{
		SessionID: sess.SessionID,
		Action:    "session.interrupt",
	})
	if err != nil || interrupted.Payload["accepted"] != true {
		t.Fatalf("interrupt failed: %v, %+v", err, interrupted)
	}
}

func TestHermesNativeApproval(t *testing.T) {
	if os.Getenv("ASTRORDER_HERMES_HELPER") == "1" {
		fakeHermesHelper()
		os.Exit(0)
	}

	root := t.TempDir()
	apprChan := make(chan map[string]any, 10)

	adapter := New(Config{
		Executable:  os.Args[0],
		Arguments:   []string{"-test.run=TestHermesNativeApproval"},
		Environment: []string{"ASTRORDER_HERMES_HELPER=1"},
		Workspace:   root,
		Allowed:     []string{root},
		AgentID:     "test-hermes",
		Emit: func(sessionID, event string, payload map[string]any, status string) error {
			if event == "hermes.approval_request" {
				apprChan <- payload
			}
			return nil
		},
	})
	defer adapter.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	sess, err := adapter.Create(ctx, core.Request{
		Fields: map[string]any{"cwd": root},
	})
	if err != nil {
		t.Fatal(err)
	}

	_, err = adapter.Command(ctx, core.Request{
		SessionID: sess.SessionID,
		Action:    "session.send",
		Fields:    map[string]any{"text": "trigger:approval"},
	})
	if err != nil {
		t.Fatal(err)
	}

	var reqID string
	select {
	case payload := <-apprChan:
		reqID, _ = payload["approval_id"].(string)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for approval request")
	}

	if reqID == "" {
		t.Fatal("empty approval_id received")
	}

	snap := adapter.Snapshot()
	if snap[sess.SessionID].Status != "waiting_approval" {
		t.Fatalf("expected status waiting_approval, got: %s", snap[sess.SessionID].Status)
	}

	resp, err := adapter.Command(ctx, core.Request{
		SessionID: sess.SessionID,
		Action:    "session.approve",
		Fields: map[string]any{
			"approval_id": reqID,
			"choice":      "once",
		},
	})
	if err != nil || resp.Payload["accepted"] != true {
		t.Fatalf("approve failed: %v, %+v", err, resp)
	}
}

func TestHermesAllowlistSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()

	symlinkPath := filepath.Join(root, "escape_link")
	_ = os.Symlink(outside, symlinkPath)

	adapter := New(Config{
		Workspace: root,
		Allowed:   []string{root},
		AgentID:   "test-hermes",
	})
	defer adapter.Close()

	ctx := context.Background()

	_, err := adapter.Create(ctx, core.Request{Fields: map[string]any{"cwd": outside}})
	if err == nil {
		t.Fatal("expected outside workspace to be rejected")
	}

	_, err = adapter.Create(ctx, core.Request{Fields: map[string]any{"cwd": symlinkPath}})
	if err == nil {
		t.Fatal("expected symlink escape to be rejected")
	}
}

func TestHermesEmitFailureFailcloseOwnOnly(t *testing.T) {
	if os.Getenv("ASTRORDER_HERMES_HELPER") == "1" {
		fakeHermesHelper()
		os.Exit(0)
	}

	root := t.TempDir()
	var failFor string
	var mu sync.Mutex

	adapter := New(Config{
		Executable:  os.Args[0],
		Arguments:   []string{"-test.run=TestHermesEmitFailureFailcloseOwnOnly"},
		Environment: []string{"ASTRORDER_HERMES_HELPER=1"},
		Workspace:   root,
		Allowed:     []string{root},
		AgentID:     "test-hermes",
		Emit: func(sessionID, event string, payload map[string]any, status string) error {
			mu.Lock()
			target := failFor
			mu.Unlock()
			if sessionID == target {
				return fmt.Errorf("simulated emit failure")
			}
			return nil
		},
	})
	defer adapter.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	s1, err := adapter.Create(ctx, core.Request{Fields: map[string]any{"cwd": root, "title": "S1"}})
	if err != nil {
		t.Fatal(err)
	}
	s2, err := adapter.Create(ctx, core.Request{Fields: map[string]any{"cwd": root, "title": "S2"}})
	if err != nil {
		t.Fatal(err)
	}

	mu.Lock()
	failFor = s1.SessionID
	mu.Unlock()

	_, err = adapter.Command(ctx, core.Request{
		SessionID: s1.SessionID,
		Action:    "session.send",
		Fields:    map[string]any{"text": "trigger:complete-fast"},
	})
	if err != nil {
		t.Fatal(err)
	}

	time.Sleep(500 * time.Millisecond)

	snap := adapter.Snapshot()
	if snap[s1.SessionID].Status != "error" {
		t.Fatalf("expected s1 to be in error state after emit failure, got: %s", snap[s1.SessionID].Status)
	}
	if snap[s2.SessionID].Status != "idle" {
		t.Fatalf("expected s2 to remain idle, got: %s", snap[s2.SessionID].Status)
	}
}

func fakeHermesHelper() {
	d := json.NewDecoder(os.Stdin)
	e := json.NewEncoder(os.Stdout)

	_ = e.Encode(map[string]any{
		"jsonrpc": "2.0",
		"method":  "event",
		"params": map[string]any{
			"type": "gateway.ready",
		},
	})

	var sidCounter int
	sidMap := map[string]string{}
	activeSessions := map[string]string{}
	configValues := map[string]any{}
	titles := map[string]string{}

	for {
		var r map[string]any
		if d.Decode(&r) != nil {
			return
		}
		method, _ := r["method"].(string)
		id := r["id"]
		params, _ := r["params"].(map[string]any)

		var result any = map[string]any{"ok": true}

		switch method {
		case "command.dispatch":
			if params["name"] == "loop-alias" {
				result = map[string]any{"type": "alias", "target": "/loop-alias"}
			} else {
				result = map[string]any{"type": "output", "output": "fixture"}
			}
		case "testing.spawn_child":
			child := exec.Command(os.Args[0], "-test.run=TestHermesGatewayChildCleanup")
			child.Env = append(os.Environ(), "ASTRORDER_HERMES_NATIVE_CHILD=1")
			if err := child.Start(); err != nil {
				os.Exit(2)
			}
			result = map[string]any{"child_pid": child.Process.Pid}
		case "session.create":
			sidCounter++
			liveSID := fmt.Sprintf("live-sid-%d", sidCounter)
			storedID := fmt.Sprintf("durable-session-%d", sidCounter)
			sidMap[liveSID] = storedID
			result = map[string]any{
				"session_id":        liveSID,
				"stored_session_id": storedID,
				"info": map[string]any{
					"model":    "test-model",
					"provider": "test-provider",
				},
			}

		case "session.branch":
			sidCounter++
			liveSID := fmt.Sprintf("live-sid-%d", sidCounter)
			storedID := fmt.Sprintf("durable-session-%d", sidCounter)
			sidMap[liveSID] = storedID
			result = map[string]any{
				"session_id":        liveSID,
				"stored_session_id": storedID,
			}

		case "session.resume":
			if params["session_id"] == "stored-large-transcript" && params["omit_messages"] != true {
				_ = e.Encode(map[string]any{"id": id, "result": map[string]any{"messages": strings.Repeat("x", 5*1024*1024)}})
				continue
			}
			cwd, _ := os.Getwd()
			if override := os.Getenv("ASTRORDER_HERMES_RESUME_CWD"); override != "" {
				cwd = override
			}
			sessID, _ := params["session_id"].(string)
			if sessID == "missing-native-session" {
				e.Encode(map[string]any{"id": id, "error": map[string]any{"code": 4007, "message": "not found"}})
				continue
			}
			liveSID := sessID
			if strings.HasPrefix(sessID, "stored-concurrent") {
				sidCounter++
				liveSID = fmt.Sprintf("concurrent-handle-%d", sidCounter)
				time.Sleep(10 * time.Millisecond)
			}
			for l, s := range sidMap {
				if s == sessID {
					liveSID = l
					break
				}
			}
			result = map[string]any{
				"session_id": liveSID,
				"info": map[string]any{
					"cwd":      cwd,
					"model":    "test-model",
					"provider": "test-provider",
				},
			}

		case "prompt.submit":
			sessID, _ := params["session_id"].(string)
			text, _ := params["text"].(string)
			activeSessions[sessID] = "running"
			if text == "trigger:complete-before-reply" {
				delete(activeSessions, sessID)
				e.Encode(map[string]any{"method": "event", "params": map[string]any{"type": "message.complete", "session_id": sessID}})
				time.Sleep(30 * time.Millisecond)
				e.Encode(map[string]any{"id": id, "result": map[string]any{"status": "streaming"}})
				continue
			}
			result = map[string]any{"status": "streaming"}
			_ = e.Encode(map[string]any{"id": id, "result": result})

			if text == "trigger:approval" {
				go func(sid string) {
					time.Sleep(50 * time.Millisecond)
					_ = e.Encode(map[string]any{
						"jsonrpc": "2.0",
						"id":      "srq-appr-test",
						"method":  "approval",
						"params": map[string]any{
							"session_id": sid,
							"request_id": "req-native-1",
							"command":    "rm -rf /tmp",
						},
					})
				}(sessID)
			} else if text == "slow:sleep-long" {
			} else {
				go func(sid string) {
					time.Sleep(80 * time.Millisecond)
					delete(activeSessions, sid)
					_ = e.Encode(map[string]any{
						"jsonrpc": "2.0",
						"method":  "event",
						"params": map[string]any{
							"type":       "message.complete",
							"session_id": sid,
							"payload":    map[string]any{"text": "done"},
						},
					})
				}(sessID)
			}
			continue

		case "session.active_list":
			var rows []map[string]any
			for sid, st := range activeSessions {
				stored := sidMap[sid]
				if stored == "" {
					stored = sid
				}
				rows = append(rows, map[string]any{
					"id":          sid,
					"session_key": stored,
					"status":      st,
				})
			}
			result = map[string]any{"sessions": rows}

		case "session.interrupt":
			sessID, _ := params["session_id"].(string)
			delete(activeSessions, sessID)
			result = map[string]any{"status": "interrupted"}

		case "session.title":
			live, _ := params["session_id"].(string)
			if title, ok := params["title"].(string); ok {
				titles[live] = title
			}
			result = map[string]any{"title": titles[live]}

		case "session.delete":
			sessID, _ := params["session_id"].(string)
			busy := false
			for live, stored := range sidMap {
				if stored == sessID && activeSessions[live] != "" {
					busy = true
				}
			}
			if busy {
				e.Encode(map[string]any{"id": id, "error": map[string]any{"code": 4023, "message": "native handle open"}})
				continue
			}
			result = map[string]any{"deleted": sessID}

		case "session.close":
			live, _ := params["session_id"].(string)
			delete(activeSessions, live)
			result = map[string]any{"closed": true}

		case "model.options":
			if os.Getenv("ASTRORDER_HERMES_SLOW_MODELS") == "1" {
				time.Sleep(300 * time.Millisecond)
			}
			result = map[string]any{
				"providers": []any{
					map[string]any{
						"slug":          "test-provider",
						"name":          "Test Provider",
						"authenticated": true,
						"models":        []any{"test-model", "test-model-2"},
					},
				},
			}

		case "commands.catalog":
			result = map[string]any{
				"pairs": []any{
					[]any{"/help", "Show help"},
				},
				"commands": map[string]any{
					"/help": map[string]any{"argument_mode": "text"},
				},
			}

		case "config.get":
			key, _ := params["key"].(string)
			if value, exists := configValues[key]; exists {
				result = map[string]any{"value": value}
			} else if key == "reasoning" {
				result = map[string]any{"value": "medium"}
			} else if key == "approvals.mode" {
				result = map[string]any{"value": "smart"}
			} else {
				result = map[string]any{"value": "default"}
			}

		case "config.set":
			if os.Getenv("ASTRORDER_HERMES_REFUSE_CONFIG") != "1" {
				key, _ := params["key"].(string)
				configValues[key] = params["value"]
			}
			result = map[string]any{"ok": true}

		case "approval.respond":
			result = map[string]any{"resolved": true}
		}

		_ = e.Encode(map[string]any{"id": id, "result": result})
	}
}

func TestHermesHistoryPageDirectSQLite(t *testing.T) {
	root := t.TempDir()
	dbPath := filepath.Join(root, "state.db")

	ctx := context.Background()
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	_, err = db.Exec("CREATE TABLE sessions (id TEXT PRIMARY KEY, title TEXT, cwd TEXT)")
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec("INSERT INTO sessions (id, title, cwd) VALUES ('sess-1', 'Test', '/tmp')")
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec("CREATE TABLE messages (id INTEGER PRIMARY KEY, session_id TEXT, role TEXT, content TEXT, timestamp REAL)")
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec("INSERT INTO messages (id, session_id, role, content, timestamp) VALUES (1, 'sess-1', 'user', 'hello', 1.0), (2, 'sess-1', 'assistant', 'hi there', 2.0), (3, 'sess-1', 'user', 'how are you', 3.0)")
	if err != nil {
		t.Fatal(err)
	}

	adapter := New(Config{
		Workspace:   root,
		Allowed:     []string{root},
		AgentID:     "test-hermes",
		StateDBPath: dbPath,
	})
	defer adapter.Close()

	adapter.mu.Lock()
	adapter.sessions["sess-1"] = &SessionState{
		SessionID: "sess-1",
		Status:    "idle",
		Metadata:  map[string]any{},
	}
	adapter.mu.Unlock()

	res, err := adapter.Command(ctx, core.Request{
		SessionID: "sess-1",
		Action:    "session.history_page",
		Fields: map[string]any{
			"limit": 2,
		},
	})
	if err != nil {
		t.Fatalf("history_page failed: %v", err)
	}

	items, ok := res.Payload["items"].([]map[string]any)
	if !ok || len(items) != 2 {
		t.Fatalf("expected 2 items, got %+v", res.Payload)
	}
	nextCursor, _ := res.Payload["next_cursor"].(string)
	if nextCursor == "" {
		t.Fatal("expected next_cursor")
	}

	res2, err := adapter.Command(ctx, core.Request{
		SessionID: "sess-1",
		Action:    "session.history_page",
		Fields: map[string]any{
			"limit":  2,
			"before": nextCursor,
		},
	})
	if err != nil {
		t.Fatalf("second page failed: %v", err)
	}
	items2, ok := res2.Payload["items"].([]map[string]any)
	if !ok || len(items2) != 1 {
		t.Fatalf("expected 1 item on second page, got %+v", res2.Payload)
	}
}
