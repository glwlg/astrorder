package projection

import (
	"context"
	"database/sql"
	"testing"

	_ "modernc.org/sqlite"
)

func TestProjectGrokNotification_AgentMessageChunk(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer db.Close()

	schema := `
	CREATE TABLE sessions (
		id TEXT PRIMARY KEY,
		agent_id TEXT,
		title TEXT,
		status TEXT,
		updated_at TEXT
	);
	CREATE TABLE tasks (
		id TEXT PRIMARY KEY,
		session_id TEXT,
		agent_id TEXT,
		kind TEXT,
		title TEXT,
		status TEXT,
		logs TEXT,
		created_at TEXT,
		updated_at TEXT
	);
	CREATE TABLE commands (
		id TEXT PRIMARY KEY,
		session_id TEXT,
		agent_id TEXT,
		state TEXT,
		error TEXT,
		created_at TEXT,
		updated_at TEXT
	);
	CREATE TABLE messages (
		id TEXT,
		session_id TEXT,
		agent_id TEXT,
		role TEXT,
		kind TEXT,
		text TEXT,
		attachments TEXT,
		created_at TEXT,
		command_id TEXT,
		PRIMARY KEY (agent_id, session_id, id)
	);`
	if _, err := db.Exec(schema); err != nil {
		t.Fatalf("create schema: %v", err)
	}

	ctx := context.Background()
	_, _ = db.Exec("INSERT INTO sessions(id, agent_id, status) VALUES('s-1', 'grok-1', 'running')")
	_, _ = db.Exec("INSERT INTO commands(id, session_id, agent_id, state) VALUES('cmd-100', 's-1', 'grok-1', 'running')")

	// 1. 发送第一段 chunk
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	payload1 := map[string]any{
		"agent_id":   "grok-1",
		"session_id": "s-1",
		"frame": map[string]any{
			"params": map[string]any{
				"update": map[string]any{
					"sessionUpdate": "agent_message_chunk",
					"content": map[string]any{
						"text": "Hello ",
					},
				},
			},
		},
	}
	events1, err := projectGrokNotificationTx(ctx, tx, "s-1", payload1)
	if err != nil {
		_ = tx.Rollback()
		t.Fatalf("project chunk 1: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit chunk 1: %v", err)
	}

	foundUpsert := false
	for _, ev := range events1 {
		if ev.Type == "message.delta" {
			foundUpsert = true
			data := ev.Data
			if data["text"] != "Hello " {
				t.Fatalf("expected 'Hello ', got %v", data["text"])
			}
			if data["role"] != "assistant" {
				t.Fatalf("expected role assistant, got %v", data["role"])
			}
			if data["command_id"] != "cmd-100" {
				t.Fatalf("expected command_id cmd-100, got %v", data["command_id"])
			}
		}
	}
	if !foundUpsert {
		t.Fatalf("expected message.delta event in chunk 1")
	}

	// 2. 发送第二段 chunk 追加
	tx2, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin tx 2: %v", err)
	}
	payload2 := map[string]any{
		"agent_id":   "grok-1",
		"session_id": "s-1",
		"frame": map[string]any{
			"params": map[string]any{
				"update": map[string]any{
					"sessionUpdate": "agent_message_chunk",
					"content":       "world!",
				},
			},
		},
	}
	events2, err := projectGrokNotificationTx(ctx, tx2, "s-1", payload2)
	if err != nil {
		_ = tx2.Rollback()
		t.Fatalf("project chunk 2: %v", err)
	}
	if err := tx2.Commit(); err != nil {
		t.Fatalf("commit chunk 2: %v", err)
	}

	foundUpsert2 := false
	for _, ev := range events2 {
		if ev.Type == "message.delta" {
			foundUpsert2 = true
			data := ev.Data
			if data["text"] != "world!" {
				t.Fatalf("expected delta 'world!', got %v", data["text"])
			}
		}
	}
	if !foundUpsert2 {
		t.Fatalf("expected message.delta event in chunk 2")
	}

	// Streaming must not create App transcript records.
	var count int
	err = db.QueryRow("SELECT COUNT(*) FROM messages").Scan(&count)
	if err != nil {
		t.Fatalf("query db: %v", err)
	}
	if count != 0 {
		t.Fatalf("transcript persisted: %d", count)
	}
}

func TestUpsertSessionTxPreservesEphemeralBoolean(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer db.Close()

	schema := `CREATE TABLE sessions (
		id TEXT,
		agent_id TEXT,
		title TEXT,
		workspace TEXT,
		status TEXT,
		source_id TEXT,
		connection_id TEXT,
		source_session_id TEXT,
		project_id TEXT,
		project_name TEXT,
		history_state TEXT,
		native_kind TEXT,
		ephemeral BOOLEAN DEFAULT '0' NOT NULL,
		control_state TEXT,
		runtime_owner TEXT,
		selected_model_provider TEXT,
		selected_model TEXT,
		selected_reasoning_effort TEXT,
		selected_approval_mode TEXT,
		handoff_from_agent_id TEXT,
		handoff_from_session_id TEXT,
		handoff_context TEXT,
		handoff_context_consumed BOOLEAN DEFAULT '0' NOT NULL,
		updated_at TEXT,
		PRIMARY KEY (agent_id, id)
	);
	CREATE TABLE workspace_preferences (
		"key" TEXT PRIMARY KEY,
		value TEXT
	);`
	if _, err := db.Exec(schema); err != nil {
		t.Fatalf("create schema: %v", err)
	}

	ctx := context.Background()
	tx1, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	row1, err := upsertSessionTx(ctx, tx1, map[string]any{
		"id":        "fork-1",
		"agent_id":  "codex-1",
		"title":     "转交摘要",
		"status":    "idle",
		"ephemeral": true,
	})
	if err != nil {
		_ = tx1.Rollback()
		t.Fatal(err)
	}
	if err := tx1.Commit(); err != nil {
		t.Fatal(err)
	}
	if row1["ephemeral"] != true {
		t.Fatalf("expected bool(true) on first upsert, got %T (%v)", row1["ephemeral"], row1["ephemeral"])
	}

	// Second upsert from daemon catalog omit/false must preserve ephemeral = true!
	tx2, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	row2, err := upsertSessionTx(ctx, tx2, map[string]any{
		"id":       "fork-1",
		"agent_id": "codex-1",
		"title":    "转交摘要",
		"status":   "running",
	})
	if err != nil {
		_ = tx2.Rollback()
		t.Fatal(err)
	}
	if err := tx2.Commit(); err != nil {
		t.Fatal(err)
	}
	if row2["ephemeral"] != true {
		t.Fatalf("expected ephemeral=true preserved after subsequent upsert, got %T (%v)", row2["ephemeral"], row2["ephemeral"])
	}
}
