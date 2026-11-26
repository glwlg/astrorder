package antigravity

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	core "astrorder.dev/session-daemon/internal/runtime"
)

func TestMoveWorkspaceRequiresIdleAndUpdatesCatalog(t *testing.T) {
	home := t.TempDir()
	wsA := filepath.Join(home, "project-a")
	wsB := filepath.Join(home, "project-b with space")
	if err := os.MkdirAll(wsA, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(wsB, 0o755); err != nil {
		t.Fatal(err)
	}
	cliDir := filepath.Join(home, ".gemini", "antigravity-cli")
	if err := os.MkdirAll(cliDir, 0o700); err != nil {
		t.Fatal(err)
	}

	id := "11111111-1111-4111-8111-111111111111"
	db, err := sql.Open("sqlite", filepath.Join(cliDir, "conversation_summaries.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	_, wsAURI, err := workspaceToFileURI(wsA, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`CREATE TABLE conversation_summaries(
		conversation_id TEXT PRIMARY KEY,
		title TEXT DEFAULT '',
		last_modified_time TEXT DEFAULT '2026-01-01 00:00:00Z',
		workspace_uris TEXT DEFAULT '[]',
		not_fully_idle INTEGER DEFAULT 0,
		killed INTEGER DEFAULT 0
	); INSERT INTO conversation_summaries(conversation_id, title, workspace_uris, not_fully_idle, killed) VALUES(?, 'Move Me', ?, 0, 0)`,
		id, `["`+wsAURI+`"]`); err != nil {
		t.Fatal(err)
	}

	a := New(Config{Home: home, Workspace: wsA, Allowed: []string{home}})
	s := newSession(id, wsA, nil)
	s.status = "running"
	s.promptRunning = true
	a.sessions[id] = s

	// Running session must be rejected before dispatch.
	req := core.Request{SessionID: id, Action: "session.move_workspace", Fields: map[string]any{"workspace": wsB}}
	if _, err := a.Command(context.Background(), req); err == nil {
		t.Fatal("expected active session workspace move to be rejected")
	}

	// Non-existent target directory must be rejected.
	s.status = "idle"
	s.promptRunning = false
	badReq := core.Request{SessionID: id, Action: "session.move_workspace", Fields: map[string]any{"workspace": filepath.Join(home, "does-not-exist")}}
	if _, err := a.Command(context.Background(), badReq); err == nil {
		t.Fatal("expected missing target directory to be rejected")
	}

	// Idle move succeeds, detaches old handle, and updates conversation_summaries.db.
	result, err := a.Command(context.Background(), req)
	if err != nil {
		t.Fatalf("move workspace failed: %v", err)
	}
	if result.Payload["moved"] != true || result.Payload["workspace"] != wsB {
		t.Fatalf("unexpected move payload: %+v", result.Payload)
	}
	if a.sessions[id] != nil {
		t.Fatal("expected live session handle to be released after workspace move")
	}

	// Verify listSessionsForHome reads back the updated target workspace cleanly.
	items, err := listSessionsForHome(context.Background(), home, "agy-local", "", false)
	if err != nil {
		t.Fatalf("listSessionsForHome: %v", err)
	}
	if len(items) != 1 || items[0]["workspace"] != wsB {
		t.Fatalf("expected workspace %q after move, got %+v", wsB, items)
	}
}

func TestMoveWorkspaceRejectsExternallyActiveUnboundConversation(t *testing.T) {
	home := t.TempDir()
	wsA := filepath.Join(home, "project-a")
	wsB := filepath.Join(home, "project-b")
	_ = os.MkdirAll(wsA, 0o755)
	_ = os.MkdirAll(wsB, 0o755)
	cliDir := filepath.Join(home, ".gemini", "antigravity-cli")
	_ = os.MkdirAll(cliDir, 0o700)

	id := "22222222-2222-4222-8222-222222222222"
	db, err := sql.Open("sqlite", filepath.Join(cliDir, "conversation_summaries.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err = db.Exec(`CREATE TABLE conversation_summaries(
		conversation_id TEXT PRIMARY KEY,
		workspace_uris TEXT DEFAULT '[]',
		not_fully_idle INTEGER DEFAULT 1,
		killed INTEGER DEFAULT 0
	); INSERT INTO conversation_summaries VALUES(?, '[]', 1, 0)`, id); err != nil {
		t.Fatal(err)
	}

	a := New(Config{Home: home, Workspace: wsA, Allowed: []string{home}})
	a.sessions[id] = &session{id: id, cwd: wsA, status: "idle", deleteOnly: true}
	if _, err := a.Command(context.Background(), core.Request{
		SessionID: id,
		Action:    "session.move_workspace",
		Fields:    map[string]any{"workspace": wsB},
	}); err == nil {
		t.Fatal("expected externally active conversation move to be rejected")
	}
}

func TestExecuteHelperMoveWorkspaceAndWindowsURIFormat(t *testing.T) {
	home := t.TempDir()
	wsTarget := filepath.Join(home, "target-proj")
	_ = os.MkdirAll(wsTarget, 0o755)
	cliDir := filepath.Join(home, ".gemini", "antigravity-cli")
	_ = os.MkdirAll(cliDir, 0o700)

	id := "33333333-3333-4333-8333-333333333333"
	db, err := sql.Open("sqlite", filepath.Join(cliDir, "conversation_summaries.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err = db.Exec(`CREATE TABLE conversation_summaries(
		conversation_id TEXT PRIMARY KEY,
		workspace_uris TEXT DEFAULT '[]',
		not_fully_idle INTEGER DEFAULT 0,
		killed INTEGER DEFAULT 0
	); INSERT INTO conversation_summaries VALUES(?, '["file:///old"]', 0, 0)`, id); err != nil {
		t.Fatal(err)
	}

	res, err := ExecuteHelper(context.Background(), map[string]any{
		"action":          "move_workspace",
		"home":            home,
		"conversation_id": id,
		"workspace":       wsTarget,
	})
	if err != nil {
		t.Fatalf("ExecuteHelper move_workspace: %v", err)
	}
	if res["confirmed"] != true || res["workspace"] != wsTarget {
		t.Fatalf("unexpected helper response: %+v", res)
	}

	// Verify Windows drive path formatting.
	_, winURI, err := workspaceToFileURI(`P:\DevApp\My Project`, false)
	if err != nil {
		t.Fatalf("windows URI: %v", err)
	}
	if winURI != "file:///P:/DevApp/My%20Project" {
		t.Fatalf("unexpected windows URI: %q", winURI)
	}
}
