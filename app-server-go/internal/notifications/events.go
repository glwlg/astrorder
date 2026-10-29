package notifications

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"
	"time"

	"astrorder.dev/app-server/internal/store"
)

const EventType = "notification.created"

type queryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

// Latest supplies unread identities without treating session metadata as messages.
func Latest(ctx context.Context, st *store.Store, through int64) ([]map[string]any, error) {
	rows, err := st.QueryRows(ctx, `SELECT e.cursor,e.data FROM events e JOIN (
		SELECT agent_id,session_id,max(cursor) AS cursor FROM events
		WHERE type=? AND cursor<=? GROUP BY agent_id,session_id
	) latest ON latest.cursor=e.cursor JOIN sessions s ON s.agent_id=e.agent_id AND s.id=e.session_id
	WHERE s.ephemeral=0`, EventType, through)
	if err != nil {
		return nil, err
	}
	result := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		data := object(row["data"])
		data["cursor"] = row["cursor"]
		result = append(result, data)
	}
	return result, nil
}

var secretLabel = regexp.MustCompile(`(?i)(api[_-]?key|access[_-]?token|refresh[_-]?token|token|password|passwd|secret|authorization)\s*[:=]\s*[^\s,;]+`)

func label(value string) string {
	value = secretLabel.ReplaceAllString(value, "$1=[REDACTED]")
	value = strings.Join(strings.Fields(value), " ")
	runes := []rune(value)
	if len(runes) > 100 {
		value = string(runes[:100])
	}
	return value
}

func text(data map[string]any, key string) string {
	value, _ := data[key].(string)
	return value
}

func object(value any) map[string]any {
	valueMap, _ := value.(map[string]any)
	return valueMap
}

// Events uses the same committed native outcome for desktop reminders and Web
// Push. Only notification metadata is persisted, never transcript/question text.
func Events(ctx context.Context, db queryer, events []store.Event) ([]store.Event, error) {
	var notices []store.Event
	for _, event := range events {
		data := event.Data
		id, kind, title, target, turn := "", "", "", "", ""
		switch event.Type {
		case "codex.notification":
			frame := object(data["frame"])
			if frame == nil {
				frame = data
			}
			params := object(frame["params"])
			if text(frame, "method") != "turn/completed" {
				continue
			}
			nativeTurn := object(params["turn"])
			turn = text(nativeTurn, "id")
			if turn == "" {
				turn = text(params, "turnId")
			}
			switch text(nativeTurn, "status") {
			case "completed":
				kind, title = "task_completed", "会话已完成"
			case "failed", "interrupted":
				kind, title = "task_failed", "会话未完成"
			default:
				continue
			}
			if turn != "" {
				id = "turn:" + turn
			}
		case "message.upsert":
			questions, _ := data["questions"].([]any)
			if text(data, "role") != "assistant" || text(data, "delivery") != "async" || len(questions) == 0 {
				continue
			}
			target, turn = text(data, "id"), text(data, "turn_id")
			if target != "" {
				id, kind, title = "question:"+target, "approval_pending", "Codex 等待你的回答"
			}
		case "approval.upsert":
			if text(data, "state") != "pending" {
				continue
			}
			target, turn = text(data, "id"), text(data, "turn_id")
			if target != "" {
				id, kind, title = "approval:"+target, "approval_pending", "操作等待审批"
				if text(data, "method") == "item/tool/requestUserInput" {
					title = "Codex 等待你的回答"
				}
			}
		case "task.upsert":
			target = text(data, "id")
			status := text(data, "status")
			if text(data, "kind") == "tool" || strings.HasPrefix(target, "codex:background:") ||
				(status == "cancelled" && text(object(data["progress"]), "type") == "goal") {
				continue
			}
			switch status {
			case "completed":
				kind, title = "task_completed", "任务已完成"
			case "failed", "cancelled":
				kind, title = "task_failed", "任务未完成"
			default:
				continue
			}
			if target != "" {
				id = "task:" + target + ":" + status
			}
		case "hermes.command_complete":
			turn = text(object(data["persisted_turn"]), "submission_id")
			if turn == "" {
				turn = text(data, "command_id")
			}
			if turn != "" {
				id, kind, title = "turn:"+turn, "task_completed", "会话已完成"
				if data["error"] != nil || text(data, "outcome") == "failed" || text(data, "outcome") == "cancelled" {
					kind, title = "task_failed", "会话未完成"
				}
			}
		case "grok.completed":
			turn = text(data, "command_id")
			if turn != "" {
				id, kind, title = "turn:"+turn, "task_completed", "会话已完成"
				if data["error"] != nil || data["cancelled"] == true {
					kind, title = "task_failed", "会话未完成"
				}
			}
		case "command.upsert":
			// Antigravity reports its terminal turn through this receipt. Codex
			// and Hermes also complete steering receipts before the turn ends.
			turn = text(data, "id")
			if turn == "" || (text(data, "action") != "send" && text(data, "action") != "enqueue") {
				continue
			}
			switch text(data, "state") {
			case "completed":
				kind, title = "task_completed", "会话已完成"
			case "failed", "cancelled":
				kind, title = "task_failed", "会话未完成"
			default:
				continue
			}
			id = "turn:" + turn
		default:
			continue
		}
		if id == "" || event.AgentID == "" || event.SessionID == "" {
			continue
		}
		var sessionTitle, status, agentKind string
		var ephemeral bool
		err := db.QueryRowContext(ctx, `SELECT s.title,s.status,s.ephemeral,a.kind FROM sessions s JOIN agents a ON a.id=s.agent_id WHERE s.agent_id=? AND s.id=?`,
			event.AgentID, event.SessionID).Scan(&sessionTitle, &status, &ephemeral, &agentKind)
		if err == sql.ErrNoRows {
			continue
		}
		if err != nil {
			return nil, err
		}
		if ephemeral || (event.Type == "command.upsert" && agentKind != "antigravity") ||
			(event.Type == "hermes.command_complete" && (status == "running" || status == "waiting_approval")) {
			continue
		}
		sum := sha256.Sum256([]byte(event.AgentID + "\x00" + event.SessionID + "\x00" + id))
		key := "notice-" + hex.EncodeToString(sum[:])
		message := label(sessionTitle)
		if message == "" {
			message = "会话"
		}
		message = fmt.Sprintf("%s · %s", message, title)
		notices = append(notices, store.Event{
			ID: key, Type: EventType, AgentID: event.AgentID, SessionID: event.SessionID,
			Data: map[string]any{
				"key": key, "kind": kind, "title": title, "message": message,
				"agent_id": event.AgentID, "session_id": event.SessionID,
				"created_at": time.Now().UTC().Format(time.RFC3339Nano),
				"target_id":  target, "turn_id": turn, "source": event.Type,
			},
		})
	}
	return notices, nil
}
