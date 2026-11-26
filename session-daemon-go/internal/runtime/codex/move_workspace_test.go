package codex

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
)

func runFakeCodexMoveHelper() {
	logPath := os.Getenv("FAKE_CODEX_MOVE_LOG")
	busyTurn := os.Getenv("FAKE_CODEX_MOVE_BUSY") == "1"
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
			_ = enc.Encode(map[string]any{"jsonrpc": "2.0", "id": id, "result": map[string]any{"serverInfo": map[string]any{"name": "fake-codex"}}})
		case "thread/resume":
			tid, _ := params["threadId"].(string)
			reqCwd, _ := params["cwd"].(string)
			if logPath != "" {
				if f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600); err == nil {
					_, _ = fmt.Fprintf(f, "resume|cwd=%s|process_cwd=%s|tid=%s\n", reqCwd, cwd, tid)
					_ = f.Close()
				}
			}
			_ = enc.Encode(map[string]any{
				"jsonrpc": "2.0",
				"id":      id,
				"result": map[string]any{
					"thread": map[string]any{"id": tid, "cwd": reqCwd},
				},
			})
		case "thread/read":
			tid, _ := params["threadId"].(string)
			turnStatus := "completed"
			if busyTurn {
				turnStatus = "inProgress"
			}
			_ = enc.Encode(map[string]any{
				"jsonrpc": "2.0",
				"id":      id,
				"result": map[string]any{
					"thread": map[string]any{
						"id": tid,
						"turns": []any{
							map[string]any{
								"status": turnStatus,
								"items": []any{
									// Historical completed collab item must NOT block user-initiated move.
									map[string]any{"type": "collabAgentToolCall", "status": "completed"},
								},
							},
						},
					},
				},
			})
		default:
			_ = enc.Encode(map[string]any{"jsonrpc": "2.0", "id": id, "result": map[string]any{}})
		}
	}
}

func TestCodexMoveWorkspaceHelperProcess(t *testing.T) {
	if os.Getenv("FAKE_CODEX_MOVE_MODE") != "1" {
		return
	}
	runFakeCodexMoveHelper()
	os.Exit(0)
}

func TestCodexMoveWorkspaceClosesAndVerifiesTargetCwd(t *testing.T) {
	root := t.TempDir()
	oldDir := filepath.Join(root, "proj-a")
	newDir := filepath.Join(root, "proj-b")
	if err := os.MkdirAll(oldDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(newDir, 0o755); err != nil {
		t.Fatal(err)
	}
	resolvedOld, _ := filepath.EvalSymlinks(oldDir)
	resolvedNew, _ := filepath.EvalSymlinks(newDir)
	logPath := filepath.Join(root, "move.log")

	adapter := NewAdapter(Config{
		AgentID:    "local-codex",
		Executable: os.Args[0],
		Arguments:  []string{"-test.run=TestCodexMoveWorkspaceHelperProcess"},
		Workspace:  resolvedOld,
		Allowed:    []string{root},
		Environment: []string{
			"FAKE_CODEX_MOVE_MODE=1",
			"FAKE_CODEX_MOVE_LOG=" + logPath,
		},
	}, nil)
	defer adapter.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := adapter.Spawn(ctx, core.Request{
		SessionID: "thread-move-1",
		Fields:    map[string]any{"cwd": resolvedOld},
	}); err != nil {
		t.Fatalf("Spawn failed: %v", err)
	}

	res, err := adapter.Command(ctx, core.Request{
		SessionID: "thread-move-1",
		Action:    "session.move_workspace",
		Fields:    map[string]any{"workspace": resolvedNew},
	})
	if err != nil {
		t.Fatalf("session.move_workspace failed: %v", err)
	}
	if res.Payload["moved"] != true || res.Payload["closed"] != true {
		t.Fatalf("unexpected payload: %+v", res.Payload)
	}
	if res.Payload["workspace"] != resolvedNew {
		t.Fatalf("workspace = %v, want %q", res.Payload["workspace"], resolvedNew)
	}
	if len(adapter.Snapshot()) != 0 {
		t.Fatalf("expected detached snapshot after move, got %+v", adapter.Snapshot())
	}

	rawLog, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	logText := string(rawLog)
	if !strings.Contains(logText, "resume|cwd="+resolvedNew+"|process_cwd="+resolvedNew+"|tid=thread-move-1") {
		t.Fatalf("expected verification resume at target cwd, log:\n%s", logText)
	}
}

func TestCodexMoveWorkspaceRejectsBusyAndDisallowedWorkspace(t *testing.T) {
	root := t.TempDir()
	oldDir := filepath.Join(root, "proj-a")
	newDir := filepath.Join(root, "proj-b")
	outsideDir := t.TempDir()
	_ = os.MkdirAll(oldDir, 0o755)
	_ = os.MkdirAll(newDir, 0o755)

	adapter := NewAdapter(Config{
		AgentID:    "local-codex",
		Executable: os.Args[0],
		Arguments:  []string{"-test.run=TestCodexMoveWorkspaceHelperProcess"},
		Workspace:  oldDir,
		Allowed:    []string{root},
		Environment: []string{
			"FAKE_CODEX_MOVE_MODE=1",
			"FAKE_CODEX_MOVE_BUSY=1",
		},
	}, nil)
	defer adapter.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := adapter.Spawn(ctx, core.Request{
		SessionID: "thread-busy-1",
		Fields:    map[string]any{"cwd": oldDir},
	}); err != nil {
		t.Fatalf("Spawn failed: %v", err)
	}

	// Outside allowlist rejected before dispatch.
	if _, err := adapter.Command(ctx, core.Request{
		SessionID: "thread-busy-1",
		Action:    "session.move_workspace",
		Fields:    map[string]any{"workspace": outsideDir},
	}); err == nil {
		t.Fatal("expected allowlist error")
	}

	// Active native turn rejected before dispatch.
	if _, err := adapter.Command(ctx, core.Request{
		SessionID: "thread-busy-1",
		Action:    "session.move_workspace",
		Fields:    map[string]any{"workspace": newDir},
	}); err == nil || !strings.Contains(err.Error(), "active native work") {
		t.Fatalf("expected active native work rejection, got %v", err)
	}
	if len(adapter.Snapshot()) != 1 {
		t.Fatal("busy session must remain attached after rejection")
	}
}
