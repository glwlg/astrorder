package projection

import (
	"astrorder.dev/app-server/internal/store"
	"fmt"
	"time"
)

// Live transcript payloads travel to browsers only. History belongs to the runtime.
func liveMessageEvent(aid, sid, id, role, kind, text string, commandID any, delta bool) store.Event {
	eventType := "message.upsert"
	if delta {
		eventType = "message.delta"
	}
	return store.Event{ID: fmt.Sprintf("live-%s-%d", id, time.Now().UnixNano()), Type: eventType, AgentID: aid, SessionID: sid, Data: map[string]any{
		"id": id, "agent_id": aid, "session_id": sid, "role": role, "kind": kind, "text": text, "attachments": []any{}, "tool": nil, "created_at": time.Now().UTC().Format(time.RFC3339Nano), "command_id": commandID,
	}}
}
