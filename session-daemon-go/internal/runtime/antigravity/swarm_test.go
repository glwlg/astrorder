package antigravity

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	core "astrorder.dev/session-daemon/internal/runtime"
)

func seedSwarmFixture(t *testing.T, home string) (coordID, workerAID, workerBID, killedID string) {
	t.Helper()
	coordID = "11111111-1111-4111-8111-111111111111"
	workerAID = "22222222-2222-4222-8222-222222222222"
	workerBID = "33333333-3333-4333-8333-333333333333"
	killedID = "44444444-4444-4444-8444-444444444444"

	cliDir := filepath.Join(home, ".gemini", "antigravity-cli")
	if err := os.MkdirAll(filepath.Join(cliDir, "conversations"), 0o755); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", filepath.Join(cliDir, "conversation_summaries.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	_, err = db.Exec(`
		CREATE TABLE conversation_summaries (
			conversation_id TEXT PRIMARY KEY,
			title TEXT DEFAULT '',
			parent_conversation_id TEXT DEFAULT '',
			nesting_depth INTEGER DEFAULT 0,
			agent_name TEXT DEFAULT '',
			group_id TEXT DEFAULT '',
			status TEXT DEFAULT '',
			not_fully_idle INTEGER DEFAULT 0,
			killed INTEGER DEFAULT 0,
			last_modified_time TEXT DEFAULT ''
		);
		CREATE TABLE conversation_parents (
			conversation_id TEXT PRIMARY KEY,
			parent_id TEXT NOT NULL
		);
	`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`
		INSERT INTO conversation_summaries VALUES
		(?, 'Refactor Auth Swarm', '', 0, 'coordinator', 'grp-1', 'RUNNING', 0, 0, '2026-11-22 10:00:00Z'),
		(?, 'Backend Worker', ?, 1, 'backend', 'grp-1', 'RUNNING', 1, 0, '2026-11-22 10:02:00Z'),
		(?, 'Frontend Worker', '', 1, 'frontend', 'grp-1', 'IDLE', 0, 0, '2026-11-22 10:01:00Z'),
		(?, 'Temp Researcher', ?, 1, 'scout', 'grp-1', 'IDLE', 0, 1, '2026-11-22 10:03:00Z');
	`, coordID, workerAID, coordID, workerBID, killedID, coordID)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`INSERT INTO conversation_parents VALUES (?, ?);`, workerBID, coordID)
	if err != nil {
		t.Fatal(err)
	}

	writeTranscript := func(convID string, lines []map[string]any) {
		logDir := filepath.Join(cliDir, "brain", convID, ".system_generated", "logs")
		if err := os.MkdirAll(logDir, 0o755); err != nil {
			t.Fatal(err)
		}
		f, err := os.Create(filepath.Join(logDir, "transcript.jsonl"))
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		for _, line := range lines {
			b, err := json.Marshal(line)
			if err != nil {
				t.Fatal(err)
			}
			_, _ = f.Write(append(b, '\n'))
		}
	}

	writeTranscript(coordID, []map[string]any{
		{
			"step_index": 1,
			"type":       "PLANNER_RESPONSE",
			"created_at": "2026-11-22T10:00:01Z",
			"model":      "claude-sonnet-4-6",
			"content":    "Starting swarm to split backend and frontend tasks.",
			"tool_calls": []map[string]any{
				{
					"name": "swarm",
					"args": map[string]any{
						"action": "\"title\"",
						"title":  "\"Auth Overhaul Team\"",
					},
				},
				{
					"name": "swarm",
					"args": map[string]any{
						"action":    "\"add\"",
						"name":      "\"backend\"",
						"workspace": "\"share\"",
						"model":     "\"gemini-3-pro\"",
						"brief":     "\"Update auth endpoints and tests.\"",
					},
				},
				{
					"name": "swarm",
					"args": map[string]any{
						"action":    "\"add\"",
						"name":      "\"frontend\"",
						"workspace": "\"inherit\"",
						"brief":     "\"Wire login state to new API.\"",
					},
				},
				{
					"name": "swarm",
					"args": map[string]any{
						"action": "\"add\"",
						"name":   "\"scout\"",
						"brief":  "\"Audit existing auth files.\"",
					},
				},
			},
		},
		{
			"step_index": 5,
			"type":       "PLANNER_RESPONSE",
			"created_at": "2026-11-22T10:02:30Z",
			"model":      "claude-sonnet-4-6",
			"tool_calls": []map[string]any{
				{
					"name": "swarm",
					"args": map[string]any{
						"action": "\"model\"",
						"name":   "\"frontend\"",
						"model":  "\"claude-opus-4-6\"",
					},
				},
				{
					"name": "swarm",
					"args": map[string]any{
						"action": "\"kill\"",
						"name":   "\"scout\"",
					},
				},
			},
		},
	})

	writeTranscript(workerAID, []map[string]any{
		{
			"step_index": 0,
			"type":       "USER_INPUT",
			"created_at": "2026-11-22T10:00:05Z",
			"content":    "Update auth endpoints and tests.",
		},
		{
			"step_index": 1,
			"type":       "PLANNER_RESPONSE",
			"created_at": "2026-11-22T10:01:30Z",
			"model":      "gemini-3-pro",
			"content":    "Implemented token refresh handler and running unit tests now.",
			"tool_calls": []map[string]any{
				{
					"name": "send_message",
					"args": map[string]any{
						"Recipient": "\"" + coordID + "\"",
						"Message":   "\"Token refresh endpoint is ready for frontend integration.\"",
					},
				},
			},
		},
	})

	writeTranscript(workerBID, []map[string]any{
		{
			"step_index": 0,
			"type":       "USER_INPUT",
			"created_at": "2026-11-22T10:00:08Z",
			"content":    "Wire login state to new API.",
		},
		{
			"step_index": 1,
			"type":       "PLANNER_RESPONSE",
			"created_at": "2026-11-22T10:01:50Z",
			"model":      "claude-opus-4-6",
			"content":    "Waiting for backend refresh endpoint confirmation.",
		},
	})

	return coordID, workerAID, workerBID, killedID
}

func TestReadNativeSessionSwarmFromCoordinatorAndChild(t *testing.T) {
	home := t.TempDir()
	coordID, workerAID, workerBID, killedID := seedSwarmFixture(t, home)

	// 1. Query from coordinator conversation ID.
	res, err := readNativeSessionSwarm(context.Background(), home, coordID, false, "", 0)
	if err != nil {
		t.Fatalf("readNativeSessionSwarm(coord) failed: %v", err)
	}
	if res["supported"] != true || res["available"] != true || res["active"] != true {
		t.Fatalf("unexpected flags: %+v", res)
	}
	if res["kind"] != "swarm" || res["title"] != "Auth Overhaul Team" || res["is_coordinator"] != true {
		t.Fatalf("unexpected metadata: %+v", res)
	}
	if res["coordinator_session_id"] != coordID {
		t.Fatalf("expected coordinator_session_id=%s, got %v", coordID, res["coordinator_session_id"])
	}

	summary := res["summary"].(map[string]int)
	if summary["total"] != 4 || summary["working"] != 1 || summary["waiting"] != 2 || summary["killed"] != 1 {
		t.Fatalf("unexpected summary counts: %+v", summary)
	}

	members := res["members"].([]map[string]any)
	byName := map[string]map[string]any{}
	for _, m := range members {
		byName[m["name"].(string)] = m
	}
	if byName["backend"]["state"] != "working" || byName["backend"]["workspace_mode"] != "share" || byName["backend"]["conversation_id"] != workerAID {
		t.Fatalf("unexpected backend member: %+v", byName["backend"])
	}
	if byName["frontend"]["state"] != "waiting" || byName["frontend"]["model"] != "claude-opus-4-6" || byName["frontend"]["conversation_id"] != workerBID {
		t.Fatalf("unexpected frontend member: %+v", byName["frontend"])
	}
	if byName["scout"]["state"] != "killed" || byName["scout"]["conversation_id"] != killedID {
		t.Fatalf("unexpected scout member: %+v", byName["scout"])
	}

	// Verify recipient UUID in send_message was resolved to "coordinator".
	board := res["board"].([]swarmBoardEntry)
	foundResolvedMsg := false
	for _, entry := range board {
		if entry.Kind == "message" && entry.Author == "backend" && len(entry.Targets) == 1 && entry.Targets[0] == "coordinator" {
			foundResolvedMsg = true
		}
	}
	if !foundResolvedMsg {
		t.Fatalf("expected resolved send_message to coordinator on board, got: %+v", board)
	}

	// 2. Query from child worker conversation ID resolves the same Swarm root!
	childRes, err := readNativeSessionSwarm(context.Background(), home, workerBID, false, workerAID, 10)
	if err != nil {
		t.Fatalf("readNativeSessionSwarm(child) failed: %v", err)
	}
	if childRes["coordinator_session_id"] != coordID || childRes["is_coordinator"] != false || childRes["current_session_id"] != workerBID {
		t.Fatalf("child view did not resolve root coordinator: %+v", childRes)
	}
	if childRes["member_conversation_id"] != workerAID {
		t.Fatalf("expected member_conversation_id=%s, got %v", workerAID, childRes["member_conversation_id"])
	}
	msgs := childRes["member_messages"].([]map[string]any)
	if len(msgs) != 2 || msgs[0]["role"] != "user" || msgs[1]["role"] != "assistant" {
		t.Fatalf("expected transcript fallback messages for workerA, got: %+v", msgs)
	}

	// 3. Reject unrelated member_conversation_id outside this Swarm.
	_, err = readNativeSessionSwarm(context.Background(), home, coordID, false, "99999999-9999-4999-8999-999999999999", 10)
	if err == nil {
		t.Fatal("expected error for unrelated member_conversation_id")
	}
}

func TestReadNativeSessionSwarmShardedTasksAndSubagents(t *testing.T) {
	home := t.TempDir()
	coordID := "55555555-5555-4555-8555-555555555555"
	subID := "66666666-6666-4666-8666-666666666666"

	logDir := filepath.Join(home, ".gemini", "antigravity-cli", "brain", coordID, ".system_generated", "logs")
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(filepath.Join(logDir, "transcript.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	lines := []map[string]any{
		{
			"step_index": 1,
			"type":       "PLANNER_RESPONSE",
			"created_at": "2026-11-22T11:00:00Z",
			"tool_calls": []map[string]any{
				{
					"name": "swarm",
					"args": map[string]any{
						"action": "\"add\"",
						"name":   "\"reviewer\"",
						"tasks":  "[\"Audit API\",\"Audit UI\"]",
						"brief":  "\"Report findings concisely.\"",
					},
				},
				{
					"name": "invoke_subagent",
					"args": map[string]any{
						"Role":     "\"Codebase Researcher\"",
						"TypeName": "\"research\"",
						"Prompt":   "\"Find all auth handlers\"",
					},
				},
			},
		},
		{
			"step_index": 2,
			"type":       "GENERIC",
			"created_at": "2026-11-22T11:00:02Z",
			"content":    "Launched subagent conversationId=" + subID,
		},
	}
	for _, l := range lines {
		b, _ := json.Marshal(l)
		_, _ = f.Write(append(b, '\n'))
	}
	f.Close()

	res, err := readNativeSessionSwarm(context.Background(), home, coordID, false, "", 0)
	if err != nil {
		t.Fatalf("readNativeSessionSwarm failed: %v", err)
	}
	members := res["members"].([]map[string]any)
	if len(members) != 4 {
		t.Fatalf("expected coordinator + 2 sharded workers + 1 subagent = 4 members, got %d: %+v", len(members), members)
	}
	names := map[string]map[string]any{}
	for _, m := range members {
		names[m["name"].(string)] = m
	}
	if names["reviewer-1"] == nil || names["reviewer-2"] == nil {
		t.Fatalf("expected sharded reviewer-1 and reviewer-2, got: %+v", members)
	}
	if names["Codebase Researcher"] == nil || names["Codebase Researcher"]["conversation_id"] != subID {
		t.Fatalf("expected subagent conversationId to match %s, got: %+v", subID, names["Codebase Researcher"])
	}
}

func TestAdapterQueryAndHelperSessionSwarm(t *testing.T) {
	home := t.TempDir()
	coordID, _, _, _ := seedSwarmFixture(t, home)

	// Adapter.Query local path.
	ad := New(Config{Home: home})
	res, err := ad.Query(context.Background(), core.Request{
		Fields: map[string]any{
			"method": "session.swarm",
			"request_params": map[string]any{
				"conversation_id": coordID,
			},
		},
	})
	if err != nil {
		t.Fatalf("Adapter.Query(session.swarm) failed: %v", err)
	}
	if res["available"] != true || res["title"] != "Auth Overhaul Team" {
		t.Fatalf("unexpected query result: %+v", res)
	}

	// ExecuteHelper SSH remote path.
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	helperRes, err := ExecuteHelper(context.Background(), map[string]any{
		"domain":          "antigravity",
		"action":          "session_swarm",
		"conversation_id": coordID,
	})
	if err != nil {
		t.Fatalf("ExecuteHelper(session_swarm) failed: %v", err)
	}
	if helperRes["available"] != true || helperRes["title"] != "Auth Overhaul Team" {
		t.Fatalf("unexpected helper result: %+v", helperRes)
	}
}

func TestListSessionsMarksSwarmAndSubagentChildrenEphemeral(t *testing.T) {
	home := t.TempDir()
	coordID, workerAID, workerBID, killedID := seedSwarmFixture(t, home)

	// Add workspace_uris column (present in real DB) and clear workerAID title to verify fallback to "@agent_name".
	dbPath := filepath.Join(home, ".gemini", "antigravity-cli", "conversation_summaries.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`ALTER TABLE conversation_summaries ADD COLUMN workspace_uris TEXT DEFAULT '[]'`); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE conversation_summaries SET title='' WHERE conversation_id=?`, workerAID); err != nil {
		db.Close()
		t.Fatal(err)
	}
	db.Close()

	items, err := listSessionsForHome(context.Background(), home, "local-antigravity", "", false)
	if err != nil {
		t.Fatalf("listSessionsForHome failed: %v", err)
	}
	byID := map[string]map[string]any{}
	for _, item := range items {
		byID[item["id"].(string)] = item
	}
	if byID[coordID]["ephemeral"] == true {
		t.Fatalf("coordinator must not be ephemeral: %+v", byID[coordID])
	}
	for _, childID := range []string{workerAID, workerBID} {
		row := byID[childID]
		if row == nil || row["ephemeral"] != true {
			t.Fatalf("child %s must be ephemeral: %+v", childID, row)
		}
		if row["parent_session_id"] != coordID {
			t.Fatalf("child %s expected parent_session_id=%s, got %+v", childID, coordID, row)
		}
	}
	if byID[killedID] != nil {
		t.Fatalf("killed child should not be listed by listSessionsForHome: %+v", byID[killedID])
	}
	if byID[workerAID]["title"] != "@backend" {
		t.Fatalf("blank child title should fallback to @agent_name, got %+v", byID[workerAID])
	}
}
