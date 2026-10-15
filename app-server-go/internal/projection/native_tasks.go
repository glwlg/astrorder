package projection

import (
	"astrorder.dev/app-server/internal/store"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
)

func nativeTaskStatus(state string) string {
	switch state {
	case "pendingInit", "pending", "queued":
		return "pending"
	case "running", "inProgress", "in_progress", "active":
		return "running"
	case "completed", "complete", "done":
		return "completed"
	case "errored", "failed", "error", "declined":
		return "failed"
	case "shutdown", "interrupted", "cancelled":
		return "cancelled"
	case "waiting_approval":
		return "waiting_approval"
	default:
		return "unknown"
	}
}

func projectCodexSubagentsTx(ctx context.Context, tx *sql.Tx, sid, aid string, item map[string]any) ([]store.Event, error) {
	states, _ := item["agentsStates"].(map[string]any)
	ids := []string{}
	if values, ok := item["receiverThreadIds"].([]any); ok {
		for _, v := range values {
			if id, ok := v.(string); ok && id != "" {
				ids = append(ids, id)
			}
		}
	}
	if item["type"] == "subAgentActivity" {
		if id, _ := item["agentThreadId"].(string); id != "" {
			ids = append(ids, id)
		}
	}
	seen := map[string]bool{}
	for id := range states {
		if id != "" {
			ids = append(ids, id)
		}
	}
	var events []store.Event
	for _, id := range ids {
		if seen[id] {
			continue
		}
		seen[id] = true
		state, _ := states[id].(map[string]any)
		status, _ := state["status"].(string)
		if status == "" && item["type"] == "subAgentActivity" {
			switch item["kind"] {
			case "started", "interacted":
				status = "running"
			case "completed":
				status = "completed"
			case "interrupted":
				status = "interrupted"
			}
		}
		progress := map[string]any{"source": "codex", "blocking": false, "child_session_id": id, "native_status": status, "tool": item["tool"], "model": item["model"], "reasoning_effort": item["reasoningEffort"], "summary": state["message"], "agent_path": item["agentPath"]}
		// A tool acknowledgement is not child completion. Preserve known child
		// state when a later interaction omits agentsStates.
		var previous string
		err := tx.QueryRowContext(ctx, `SELECT progress FROM tasks WHERE agent_id=? AND session_id=? AND id=?`, aid, sid, "codex:subagent:"+id).Scan(&previous)
		if err != nil && err != sql.ErrNoRows {
			return nil, err
		}
		if previous != "" {
			old := decodeTaskProgress(previous)
			for k, v := range old {
				if progress[k] == nil || progress[k] == "" {
					progress[k] = v
				}
			}
			if status == "" {
				status, _ = old["native_status"].(string)
			}
		}
		if status == "" && item["tool"] == "spawnAgent" {
			status = "pendingInit"
			progress["native_status"] = status
		}
		title, _ := item["prompt"].(string)
		if title == "" {
			title, _ = item["agentPath"].(string)
		}
		if title == "" {
			title = "子智能体 " + id
		}
		ev, err := projectTaskUpsertTx(ctx, tx, sid, aid, map[string]any{"id": "codex:subagent:" + id, "session_id": sid, "agent_id": aid, "kind": "subagent", "title": title, "status": nativeTaskStatus(status), "target_id": id, "progress": progress, "logs": []any{}})
		if err != nil {
			return nil, err
		}
		events = append(events, ev...)
	}
	return events, nil
}

func projectHermesSubagentTx(ctx context.Context, tx *sql.Tx, sid, aid, phase string, data map[string]any) ([]store.Event, error) {
	id, _ := data["subagent_id"].(string)
	if id == "" {
		id, _ = data["child_session_id"].(string)
	}
	if id == "" {
		if delegation, _ := data["delegation_id"].(string); delegation != "" {
			id = fmt.Sprint(delegation, ":", data["task_index"])
		}
	}
	if id == "" {
		return nil, nil
	}
	key := "hermes:subagent:" + id
	progress := map[string]any{}
	var previous string
	err := tx.QueryRowContext(ctx, `SELECT progress FROM tasks WHERE agent_id=? AND session_id=? AND id=?`, aid, sid, key).Scan(&previous)
	if err != nil && err != sql.ErrNoRows {
		return nil, err
	}
	if previous != "" {
		progress = decodeTaskProgress(previous)
	}
	for k, v := range data {
		if k != "text" || phase != "subagent.thinking" {
			progress[k] = v
		}
	}
	progress["source"], progress["blocking"] = "hermes", false
	status, _ := data["status"].(string)
	if status == "" {
		switch phase {
		case "subagent.spawn_requested":
			status = "pending"
		case "subagent.start":
			status = "running"
		case "subagent.complete":
			status = "completed"
		default:
			status, _ = progress["native_status"].(string)
		}
	}
	progress["native_status"] = status
	title, _ := progress["goal"].(string)
	if strings.TrimSpace(title) == "" {
		title = "子智能体 " + id
	}
	target, _ := progress["child_session_id"].(string)
	return projectTaskUpsertTx(ctx, tx, sid, aid, map[string]any{"id": key, "session_id": sid, "agent_id": aid, "kind": "subagent", "title": title, "status": nativeTaskStatus(status), "target_id": optionalTaskText(target), "progress": progress, "logs": []any{}})
}

func decodeTaskProgress(raw string) map[string]any {
	result := map[string]any{}
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.UseNumber()
	_ = decoder.Decode(&result)
	if result == nil {
		return map[string]any{}
	}
	return result
}
func optionalTaskText(s string) any {
	if s == "" {
		return nil
	}
	return s
}
