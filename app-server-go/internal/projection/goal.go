package projection

import (
	"context"
	"database/sql"
	"fmt"

	"astrorder.dev/app-server/internal/store"
)

// projectNativeGoalTx keeps native goal metadata in the existing task API.
// Native progress is preserved; pending/failed must not imply a goal completed.
func projectNativeGoalTx(ctx context.Context, tx *sql.Tx, sid, aid, source string, goal map[string]any) ([]store.Event, error) {
	if goal == nil {
		// An empty native snapshot is normal for sessions without a goal.
		// Emit a tombstone only once, when clearing an existing projection.
		rows, err := queryRows(ctx, tx, `SELECT status FROM tasks WHERE agent_id=? AND session_id=? AND id=?`, aid, sid, source+":goal:current")
		if err != nil {
			return nil, err
		}
		if len(rows) == 0 || rows[0]["status"] == "cancelled" {
			return nil, nil
		}
	}
	status := "cancelled"
	title := "目标"
	progress := map[string]any{"source": source, "type": "goal", "blocking": false}
	if goal != nil {
		nativeStatus, _ := goal["status"].(string)
		progress["native_status"] = nativeStatus
		for key, value := range goal {
			progress[key] = value
		}
		// Reserved projection metadata must not be overridden by native fields.
		progress["source"], progress["type"], progress["blocking"] = source, "goal", false
		switch nativeStatus {
		case "active", "waiting":
			status = "running"
		case "paused", "usageLimited", "budgetLimited":
			status = "pending"
		case "complete", "done":
			status = "completed"
		case "blocked":
			status = "failed"
		case "cleared":
			status = "cancelled"
		default:
			return nil, fmt.Errorf("%w: unknown %s goal status %q", ErrCorruptedFrame, source, nativeStatus)
		}
		text, _ := goal["objective"].(string)
		if source == "hermes" {
			text, _ = goal["title"].(string)
		}
		if text == "" {
			return nil, fmt.Errorf("%w: native goal missing title", ErrCorruptedFrame)
		}
		title = "目标: " + text
	}
	return projectTaskUpsertTx(ctx, tx, sid, aid, map[string]any{
		"id": source + ":goal:current", "agent_id": aid, "session_id": sid,
		"kind": "todo", "title": title, "status": status, "progress": progress, "logs": []any{},
	})
}
