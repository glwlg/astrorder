package projection

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strconv"

	"astrorder.dev/app-server/internal/store"
)

// Plans are full snapshots. Keep removed rows cancelled so replay and open
// detail panels remain stable, and retain a revision even for an empty list.
func projectNativePlanTx(ctx context.Context, tx *sql.Tx, sid, aid, source string, data map[string]any) ([]store.Event, error) {
	field, titleField, prefix := "todos", "content", source+":todo:"
	if source == "codex" {
		field, titleField, prefix = "plan", "step", "codex:plan:"
	}
	steps, ok := data[field].([]any)
	if !ok {
		return nil, nil
	}
	encoded, _ := json.Marshal([]string{aid, sid, source})
	watermark := "native_plan_revision:" + string(encoded)
	revision, versioned := data["revision"]
	if source == "hermes" && versioned {
		var old string
		err := tx.QueryRowContext(ctx, `SELECT value FROM workspace_preferences WHERE "key"=?`, watermark).Scan(&old)
		if err != nil && err != sql.ErrNoRows {
			return nil, err
		}
		incoming, err := strconv.ParseInt(fmt.Sprint(revision), 10, 64)
		if err != nil {
			return nil, nil
		}
		previous, _ := strconv.ParseInt(old, 10, 64)
		if incoming < previous {
			return nil, nil
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO workspace_preferences("key",value) VALUES(?,?) ON CONFLICT("key") DO UPDATE SET value=excluded.value`, watermark, strconv.FormatInt(incoming, 10)); err != nil {
			return nil, err
		}
	}
	rows, err := queryRows(ctx, tx, `SELECT * FROM tasks WHERE agent_id=? AND session_id=? AND kind='todo'`, aid, sid)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var events []store.Event
	for index, value := range steps {
		step, ok := value.(map[string]any)
		if !ok {
			continue
		}
		title, _ := step[titleField].(string)
		if title == "" {
			continue
		}
		id, _ := step["id"].(string)
		if source == "codex" || id == "" {
			id = strconv.Itoa(index)
		}
		key := prefix + id
		seen[key] = true
		status, _ := step["status"].(string)
		progress := map[string]any{"source": source, "native_status": status, "order": index, "parent": step["parent"], "revision": revision, "turn_id": data["turnId"], "explanation": data["explanation"]}
		ev, err := projectTaskUpsertTx(ctx, tx, sid, aid, map[string]any{"id": key, "kind": "todo", "title": title, "status": nativeTaskStatus(status), "progress": progress, "logs": []any{}})
		if err != nil {
			return nil, err
		}
		events = append(events, ev...)
	}
	for _, row := range rows {
		id, _ := row["id"].(string)
		if len(id) < len(prefix) || id[:len(prefix)] != prefix || seen[id] || row["status"] == "cancelled" {
			continue
		}
		row["status"] = "cancelled"
		if raw, ok := row["progress"].(string); ok {
			row["progress"] = decodeTaskProgress(raw)
		}
		row["logs"] = []any{}
		ev, err := projectTaskUpsertTx(ctx, tx, sid, aid, row)
		if err != nil {
			return nil, err
		}
		events = append(events, ev...)
	}
	return events, nil
}

func projectCodexToolTx(ctx context.Context, tx *sql.Tx, sid, aid, method string, item map[string]any) ([]store.Event, error) {
	id, _ := item["id"].(string)
	if id == "" {
		return nil, nil
	}
	kind, _ := item["type"].(string)
	status, _ := item["status"].(string)
	if status == "" {
		status = "running"
		if method == "item/completed" {
			status = "completed"
		}
	}
	progress := map[string]any{"source": "codex", "native_status": status, "output": item["aggregatedOutput"], "exit_code": item["exitCode"], "arguments": item["arguments"], "result": item["result"], "error": item["error"], "changes": item["changes"]}
	title := kind
	if tool, _ := item["tool"].(string); tool != "" {
		title = tool
	}
	if query, _ := item["query"].(string); query != "" {
		title = query
	}
	return projectTaskUpsertTx(ctx, tx, sid, aid, map[string]any{"id": id, "kind": "tool", "title": title, "status": nativeTaskStatus(status), "command": item["command"], "progress": progress, "logs": []any{}})
}

// RestoreNativeTaskItemTx restores historical child metadata only when absent.
// Reading an older page must never roll a live child back to an earlier state.
func RestoreNativeTaskItemTx(ctx context.Context, tx *sql.Tx, sid, aid string, item map[string]any) ([]store.Event, error) {
	if nested, ok := item["item"].(map[string]any); ok {
		item = nested
	}
	if item["type"] != "collabAgentToolCall" && item["type"] != "subAgentActivity" {
		return nil, nil
	}
	states, _ := item["agentsStates"].(map[string]any)
	filtered := map[string]any{}
	ids := []any{}
	candidates := map[string]bool{}
	for id := range states {
		candidates[id] = true
	}
	if values, ok := item["receiverThreadIds"].([]any); ok {
		for _, v := range values {
			if id, ok := v.(string); ok {
				candidates[id] = true
			}
		}
	}
	if id, ok := item["agentThreadId"].(string); ok {
		candidates[id] = true
	}
	for id := range candidates {
		var count int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM tasks WHERE agent_id=? AND session_id=? AND id=?`, aid, sid, "codex:subagent:"+id).Scan(&count); err != nil {
			return nil, err
		}
		if count == 0 {
			ids = append(ids, id)
			if states[id] != nil {
				filtered[id] = states[id]
			}
		}
	}
	if len(ids) == 0 {
		return nil, nil
	}
	copyItem := map[string]any{}
	for k, v := range item {
		copyItem[k] = v
	}
	copyItem["receiverThreadIds"], copyItem["agentsStates"] = ids, filtered
	if item["type"] == "subAgentActivity" {
		copyItem["agentThreadId"] = ids[0]
	}
	return projectCodexSubagentsTx(ctx, tx, sid, aid, copyItem)
}
