package antigravity

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	core "astrorder.dev/session-daemon/internal/runtime"
	sshtrans "astrorder.dev/session-daemon/internal/transport/ssh"
	"google.golang.org/protobuf/encoding/protowire"
)

func pbBytes(num protowire.Number, val []byte) []byte {
	var b []byte
	b = protowire.AppendTag(b, num, protowire.BytesType)
	return protowire.AppendBytes(b, val)
}

func pbStr(num protowire.Number, s string) []byte {
	return pbBytes(num, []byte(s))
}

func pbVarint(num protowire.Number, v uint64) []byte {
	var b []byte
	b = protowire.AppendTag(b, num, protowire.VarintType)
	return protowire.AppendVarint(b, v)
}

func TestDecodeStep132ToolsAndACPToolNormalization(t *testing.T) {
	dir := t.TempDir()
	id := "22222222-2222-4222-8222-222222222222"
	db, err := sql.Open("sqlite", filepath.Join(dir, id+".db"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`CREATE TABLE steps(idx INTEGER PRIMARY KEY,step_type INTEGER,status INTEGER,step_format INTEGER,step_payload BLOB,metadata BLOB)`)
	if err != nil {
		t.Fatal(err)
	}

	makeStep := func(name, jsonArgs string, kvs map[string]string, outText string, bg map[string]string, errMsg string) ([]byte, []byte) {
		var toolCall []byte
		toolCall = append(toolCall, pbStr(1, "call-1")...)
		toolCall = append(toolCall, pbStr(2, name)...)
		toolCall = append(toolCall, pbStr(3, jsonArgs)...)
		meta := pbBytes(4, toolCall)

		var d140 []byte
		for k, v := range kvs {
			kv := append(pbStr(1, k), pbStr(2, v)...)
			d140 = append(d140, pbBytes(1, kv)...)
		}
		if outText != "" {
			res140 := pbStr(1, outText)
			d140 = append(d140, pbBytes(2, res140)...)
		}

		var payload []byte
		payload = append(payload, pbVarint(1, 132)...)
		payload = append(payload, pbVarint(4, 3)...)
		payload = append(payload, pbBytes(140, d140)...)
		if len(bg) > 0 {
			var d148 []byte
			if bg["task_id"] != "" {
				d148 = append(d148, pbStr(1, bg["task_id"])...)
			}
			if bg["output"] != "" {
				d148 = append(d148, pbStr(3, bg["output"])...)
			}
			if bg["command"] != "" {
				d148 = append(d148, pbStr(4, bg["command"])...)
			}
			if bg["summary"] != "" {
				d148 = append(d148, pbStr(7, bg["summary"])...)
			}
			payload = append(payload, pbBytes(148, d148)...)
		}
		if errMsg != "" {
			payload = append(payload, pbBytes(31, pbStr(2, errMsg))...)
		}
		return payload, meta
	}

	p1, m1 := makeStep(
		"run_command",
		`{"CommandLine":"git status -sb","toolSummary":"Check git status"}`,
		map[string]string{"CommandLine": "git status -sb", "toolSummary": "Check git status"},
		"Created At: 2026-11-18T10:00:00Z\nCompleted At: 2026-11-18T10:00:01Z\nCommand exited with code 0.\n\nCommand output:\n## master\n",
		nil, "",
	)
	p2, m2 := makeStep(
		"read_file",
		`{"Path":"/repo/main.go","StartLine":1,"EndLine":20}`,
		map[string]string{"Path": "/repo/main.go", "StartLine": "1", "EndLine": "20"},
		"Created At: 2026-11-18T10:00:02Z\nCompleted At: 2026-11-18T10:00:03Z\nShowing lines 1-2 of 50\n1: package main\n2: func main() {}\n",
		nil, "",
	)
	p3, m3 := makeStep(
		"edit_file",
		`{"Path":"/repo/main.go","Target":"old()","Replacement":"new()"}`,
		map[string]string{"Path": "/repo/main.go", "Target": "old()", "Replacement": "new()"},
		"Created At: 2026-11-18T10:00:04Z\nCompleted At: 2026-11-18T10:00:05Z\nMade 1 replacement.\n",
		nil, "",
	)
	p4, m4 := makeStep(
		"write_file",
		`{"Path":"/repo/new.txt","Content":"hello\nworld"}`,
		map[string]string{"Path": "/repo/new.txt", "Content": "hello\nworld"},
		"Created file.\n",
		nil, "",
	)

	for i, row := range []struct {
		st      int
		payload []byte
		meta    []byte
	}{
		{3, p1, m1},
		{3, p2, m2},
		{3, p3, m3},
		{3, p4, m4},
	} {
		if _, err := db.Exec(`INSERT INTO steps VALUES(?,132,?,0,?,?)`, i+1, row.st, row.payload, row.meta); err != nil {
			t.Fatal(err)
		}
	}
	db.Close()

	items, _, err := readHistory(context.Background(), dir, id, "", 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 4 {
		t.Fatalf("expected 4 tool items, got %d: %+v", len(items), items)
	}

	t0 := items[0]["tool"].(map[string]any)
	a0 := t0["arguments"].(map[string]any)
	if a0["command"] != "git status -sb" || a0["summary"] != "Check git status" || t0["exit_code"] != 0 {
		t.Fatalf("normalized run_command unexpected: tool=%+v", t0)
	}
	if items[0]["text"] != "## master" {
		t.Fatalf("expected clean command stdout '## master', got %q", items[0]["text"])
	}

	t1 := items[1]["tool"].(map[string]any)
	a1 := t1["arguments"].(map[string]any)
	if a1["path"] != "/repo/main.go" || a1["start_line"] != 1 || a1["end_line"] != 20 {
		t.Fatalf("normalized read_file args unexpected: %+v", a1)
	}
	var readJSON map[string]any
	if err := json.Unmarshal([]byte(items[1]["text"].(string)), &readJSON); err != nil {
		t.Fatalf("expected JSON content for read_file text, got %q: %v", items[1]["text"], err)
	}
	if readJSON["total_lines"] != float64(50) || readJSON["truncated"] != true || readJSON["content"] != "1: package main\n2: func main() {}\n" {
		t.Fatalf("read_file JSON mismatch: %+v", readJSON)
	}

	t2 := items[2]["tool"].(map[string]any)
	a2 := t2["arguments"].(map[string]any)
	if a2["path"] != "/repo/main.go" || a2["old_string"] != "old()" || a2["new_string"] != "new()" {
		t.Fatalf("normalized edit_file args unexpected: %+v", a2)
	}

	t3 := items[3]["tool"].(map[string]any)
	a3 := t3["arguments"].(map[string]any)
	changes, _ := a3["changes"].([]map[string]any)
	if len(changes) != 1 || changes[0]["path"] != "/repo/new.txt" {
		t.Fatalf("normalized write_file changes unexpected: %+v", a3)
	}

	// Verify live ACP tool_call + diff content normalization.
	var emittedTool map[string]any
	ad := New(Config{Emit: func(_, _ string, p map[string]any, _ string) error {
		if p["type"] == "message.upsert" {
			emittedTool = p["data"].(map[string]any)
		}
		return nil
	}})
	s := newSession(id, dir, nil)
	defer s.stopWorker()
	ad.handleSessionUpdate(s, map[string]any{
		"sessionUpdate": "tool_call",
		"toolCallId":    "acp-edit-1",
		"kind":          "edit",
		"title":         "Edit /src/app.go",
		"status":        "completed",
		"content": []any{
			map[string]any{
				"type":    "diff",
				"path":    "/src/app.go",
				"oldText": "before()",
				"newText": "after()",
			},
		},
	})
	liveTool := emittedTool["tool"].(map[string]any)
	liveArgs := liveTool["arguments"].(map[string]any)
	if liveArgs["path"] != "/src/app.go" || liveArgs["old_string"] != "before()" || liveArgs["new_string"] != "after()" {
		t.Fatalf("live ACP diff normalization failed: %+v", liveArgs)
	}
}

func TestReadNativeSessionUsageAndEmitEnrichment(t *testing.T) {
	home := t.TempDir()
	cliDir := filepath.Join(home, ".gemini", "antigravity-cli")
	convDir := filepath.Join(cliDir, "conversations")
	if err := os.MkdirAll(convDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cliDir, "settings.json"), []byte(`{"model":"Gemini 4 Carbon (High)"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	id := "33333333-3333-4333-8333-333333333333"
	db, err := sql.Open("sqlite", filepath.Join(convDir, id+".db"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`
		CREATE TABLE steps(idx INTEGER PRIMARY KEY,step_type INTEGER,status INTEGER,step_format INTEGER,step_payload BLOB,metadata BLOB);
		CREATE TABLE gen_metadata(idx INTEGER PRIMARY KEY,data BLOB,size INTEGER);
	`)
	if err != nil {
		t.Fatal(err)
	}

	stepUsage := append(pbVarint(2, 1000), pbVarint(3, 200)...)
	stepUsage = append(stepUsage, pbVarint(5, 4000)...)
	stepUsage = append(stepUsage, pbVarint(10, 50)...)
	stepMeta := pbBytes(9, stepUsage)
	if _, err := db.Exec(`INSERT INTO steps VALUES(1,15,3,0,?,?)`, pbVarint(1, 15), stepMeta); err != nil {
		t.Fatal(err)
	}

	genUsage := append(pbVarint(2, 500), pbVarint(3, 150)...)
	genUsage = append(genUsage, pbVarint(5, 9500)...)
	genUsage = append(genUsage, pbVarint(10, 40)...)
	cwm := append(pbVarint(1, 9000), pbVarint(4, 512000)...)
	cs := pbBytes(10, cwm)
	var cm []byte
	cm = append(cm, pbBytes(4, genUsage)...)
	cm = append(cm, pbBytes(9, cs)...)
	cm = append(cm, pbStr(19, "gemini-4-carbon")...)
	cm = append(cm, pbStr(21, "Gemini 4 Carbon (High)")...)
	genBlob := pbBytes(1, cm)
	if _, err := db.Exec(`INSERT INTO gen_metadata VALUES(1,?,?)`, genBlob, len(genBlob)); err != nil {
		t.Fatal(err)
	}
	db.Close()

	usage, err := readNativeSessionUsage(context.Background(), home, id, false)
	if err != nil {
		t.Fatal(err)
	}
	if usage["context_available"] != true || usage["last_input_tokens"] != int64(10000) || usage["context_window"] != int64(512000) {
		t.Fatalf("unexpected native usage: %+v", usage)
	}

	var metrics []map[string]any
	ad := New(Config{Home: home, Emit: func(_, kind string, p map[string]any, _ string) error {
		if kind == "token.usage" {
			metrics = append(metrics, p)
		}
		return nil
	}})
	s := newSession(id, home, nil)
	defer s.stopWorker()
	if err := ad.emitUsage(s, map[string]any{"usage": map[string]any{"input_tokens": float64(500), "output_tokens": float64(150), "cache_read_tokens": float64(9500), "total_tokens": float64(10150)}}); err != nil {
		t.Fatal(err)
	}
	if len(metrics) != 1 || metrics[0]["context_available"] != true || metrics[0]["last_input_tokens"] != int64(10000) || metrics[0]["context_window"] != int64(512000) {
		t.Fatalf("emitUsage did not enrich context: %+v", metrics)
	}
}

func TestFastUnboundDeleteAndMultiplexedACPControls(t *testing.T) {
	home := t.TempDir()
	cliDir := filepath.Join(home, ".gemini", "antigravity-cli")
	convDir := filepath.Join(cliDir, "conversations")
	deskDir := filepath.Join(home, ".gemini", "antigravity", "conversations")
	if err := os.MkdirAll(convDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(deskDir, 0o700); err != nil {
		t.Fatal(err)
	}

	idleID := "55555555-5555-4555-8555-555555555555"
	swarmOrphanID := "66666666-6666-4666-8666-666666666666"
	busyID := "77777777-7777-4777-8777-777777777777"
	desktopOnlyID := "88888888-8888-4888-8888-888888888888"
	liveID := "99999999-9999-4999-8999-999999999999"

	sumDB, err := sql.Open("sqlite", filepath.Join(cliDir, "conversation_summaries.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer sumDB.Close()
	if _, err := sumDB.Exec(`CREATE TABLE conversation_summaries (conversation_id TEXT PRIMARY KEY, not_fully_idle INTEGER, killed INTEGER);
		INSERT INTO conversation_summaries VALUES (?, 0, 0), (?, 0, 0), (?, 1, 0), (?, 0, 0)`, idleID, swarmOrphanID, busyID, liveID); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(convDir, idleID+".db"), []byte("dummy"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(convDir, liveID+".db"), []byte("live"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(deskDir, desktopOnlyID+".db"), []byte("desk"), 0o600); err != nil {
		t.Fatal(err)
	}

	var acpStarts atomic.Int32
	starter := func(_ context.Context, spec sshtrans.CommandSpec) (SSHSession, error) {
		if len(spec.Args) > 0 && spec.Args[len(spec.Args)-1] == "models" {
			fake := newFakeAgySSHSession()
			go func() {
				_, _ = fmt.Fprint(fake.stdoutW, "gemini-test-medium\tTest Gemini (Medium)\nclaude-test\tTest Claude\n")
				fake.finishRemote(0)
			}()
			return fake, nil
		}
		if len(spec.Args) == 0 || spec.Args[len(spec.Args)-1] != "acp" {
			return nil, fmt.Errorf("expected official 'acp' subcommand, got %v", spec.Args)
		}
		acpStarts.Add(1)
		fake := newFakeAgySSHSession()
		go func() {
			serveMockACP(fake.stdinR, fake.stdoutW, liveID, false)
			fake.finishRemote(0)
		}()
		return fake, nil
	}

	helper := func(ctx context.Context, payload map[string]any) (map[string]any, error) {
		payload["home"] = home
		return ExecuteHelper(ctx, payload)
	}

	ad := NewWithSSH(Config{Executable: "/usr/local/bin/agy", Workspace: "/work", Allowed: []string{"/work"}, ConnectionID: "ssh-acp"}, starter, helper)
	defer ad.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// 1. Delete unattached CLI session and orphan Swarm session with ZERO agy CLI spawns!
	for _, targetID := range []string{idleID, swarmOrphanID} {
		if _, err := ad.Spawn(ctx, core.Request{SessionID: targetID, Fields: map[string]any{"delete_only": true}}); err != nil {
			t.Fatalf("delete_only spawn %s: %v", targetID, err)
		}
		delRes, err := ad.Command(ctx, core.Request{SessionID: targetID, Action: "session.delete"})
		if err != nil || delRes.Payload["deleted"] != targetID {
			t.Fatalf("fast delete %s failed: %+v %v", targetID, delRes, err)
		}
	}
	if acpStarts.Load() != 0 {
		t.Fatalf("expected 0 agy acp launches for unbound deletion, got %d", acpStarts.Load())
	}

	// Busy external conversation and desktop-only conversation must be rejected!
	for _, blockedID := range []string{busyID, desktopOnlyID} {
		_, _ = ad.Spawn(ctx, core.Request{SessionID: blockedID, Fields: map[string]any{"delete_only": true}})
		if _, err := ad.Command(ctx, core.Request{SessionID: blockedID, Action: "session.delete"}); err == nil {
			t.Fatalf("expected unbound delete rejection for %s", blockedID)
		}
	}

	// 2. Single warm agy acp process serves attach + instant model/mode/reasoning switches without restarting!
	if _, err := ad.Spawn(ctx, core.Request{SessionID: liveID, Fields: map[string]any{"cwd": "/work"}}); err != nil {
		t.Fatalf("spawn: %v", err)
	}
	t0 := time.Now()
	if _, err := ad.Command(ctx, core.Request{SessionID: liveID, Action: "session.model.set", Fields: map[string]any{"provider": "antigravity", "model": "gemini-test-high"}}); err != nil {
		t.Fatalf("switchModel: %v", err)
	}
	if _, err := ad.Command(ctx, core.Request{SessionID: liveID, Action: "session.approval.set", Fields: map[string]any{"mode": "auto"}}); err != nil {
		t.Fatalf("switchApproval: %v", err)
	}
	if _, err := ad.Command(ctx, core.Request{SessionID: liveID, Action: "session.reasoning.set", Fields: map[string]any{"effort": "low"}}); err != nil {
		t.Fatalf("switchReasoning: %v", err)
	}
	if elapsed := time.Since(t0); elapsed > 50*time.Millisecond {
		t.Fatalf("expected instant ACP model+mode+reasoning RPCs under 50ms, took %v", elapsed)
	}
	if acpStarts.Load() != 1 {
		t.Fatalf("expected exactly 1 multiplexed agy acp process, got %d", acpStarts.Load())
	}
}
