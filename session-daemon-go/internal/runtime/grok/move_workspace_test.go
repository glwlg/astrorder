package grok_test

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	core "astrorder.dev/session-daemon/internal/runtime"
	"astrorder.dev/session-daemon/internal/runtime/grok"
)

func runFakeGrokMoveACP() {
	logPath := os.Getenv("FAKE_GROK_MOVE_LOG")
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	enc := json.NewEncoder(os.Stdout)
	cwd, _ := os.Getwd()

	for scanner.Scan() {
		var req map[string]any
		if err := json.Unmarshal(scanner.Bytes(), &req); err != nil {
			continue
		}
		method, _ := req["method"].(string)
		id, hasID := req["id"]
		if !hasID {
			continue
		}
		params, _ := req["params"].(map[string]any)
		switch method {
		case "initialize":
			_ = enc.Encode(map[string]any{
				"jsonrpc": "2.0",
				"id":      id,
				"result": map[string]any{
					"protocolVersion": 1,
					"agentInfo":       map[string]any{"name": "fake-grok", "version": "1.0.0"},
					"agentCapabilities": map[string]any{
						"loadSession": true,
					},
				},
			})
		case "session/load":
			sid, _ := params["sessionId"].(string)
			reqCwd, _ := params["cwd"].(string)
			if logPath != "" {
				if f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600); err == nil {
					_, _ = fmt.Fprintf(f, "load|cwd=%s|process_cwd=%s|sid=%s\n", reqCwd, cwd, sid)
					_ = f.Close()
				}
			}
			_ = enc.Encode(map[string]any{
				"jsonrpc": "2.0",
				"id":      id,
				"result": map[string]any{
					"sessionId": sid,
				},
			})
		default:
			_ = enc.Encode(map[string]any{"jsonrpc": "2.0", "id": id, "result": map[string]any{}})
		}
	}
}

func TestGrokMoveWorkspaceHelperProcess(t *testing.T) {
	if os.Getenv("FAKE_GROK_MOVE_MODE") != "1" {
		return
	}
	runFakeGrokMoveACP()
	os.Exit(0)
}

func TestGrokMoveWorkspaceClosesAndVerifiesSessionLoad(t *testing.T) {
	root := t.TempDir()
	oldDir := filepath.Join(root, "proj-a")
	newDir := filepath.Join(root, "proj-b")
	outsideDir := t.TempDir()
	_ = os.MkdirAll(oldDir, 0o755)
	_ = os.MkdirAll(newDir, 0o755)
	resolvedOld, _ := filepath.EvalSymlinks(oldDir)
	resolvedNew, _ := filepath.EvalSymlinks(newDir)
	logPath := filepath.Join(root, "grok-move.log")

	t.Setenv("FAKE_GROK_MOVE_MODE", "1")
	t.Setenv("FAKE_GROK_MOVE_LOG", logPath)

	adapter := grok.New(grok.Config{
		Executable: os.Args[0],
		Arguments:  []string{"-test.run=TestGrokMoveWorkspaceHelperProcess"},
		Workspace:  resolvedOld,
		Allowed:    []string{root},
		AgentID:    "local-grok",
	})
	defer adapter.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := adapter.Spawn(ctx, core.Request{
		SessionID: "grok-move-1",
		Fields:    map[string]any{"cwd": resolvedOld},
	}); err != nil {
		t.Fatalf("Spawn failed: %v", err)
	}

	// Disallowed target rejected before closing old session.
	if _, err := adapter.Command(ctx, core.Request{
		SessionID: "grok-move-1",
		Action:    "session.move_workspace",
		Fields:    map[string]any{"workspace": outsideDir},
	}); err == nil {
		t.Fatal("expected allowlist error")
	}
	if len(adapter.Snapshot()) != 1 {
		t.Fatal("rejected move must keep session attached")
	}

	res, err := adapter.Command(ctx, core.Request{
		SessionID: "grok-move-1",
		Action:    "session.move_workspace",
		Fields:    map[string]any{"workspace": resolvedNew},
	})
	if err != nil {
		t.Fatalf("session.move_workspace failed: %v", err)
	}
	if res.Payload["moved"] != true || res.Payload["closed"] != true || res.Payload["workspace"] != resolvedNew {
		t.Fatalf("unexpected payload: %+v", res.Payload)
	}
	if len(adapter.Snapshot()) != 0 {
		t.Fatalf("expected detached snapshot after move, got %+v", adapter.Snapshot())
	}

	rawLog, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	logText := string(rawLog)
	if !strings.Contains(logText, "load|cwd="+resolvedNew+"|process_cwd="+resolvedNew+"|sid=grok-move-1") {
		t.Fatalf("expected verification session/load at target cwd, log:\n%s", logText)
	}
}
