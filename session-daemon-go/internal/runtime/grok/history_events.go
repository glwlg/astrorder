package grok

import (
	"context"
	"fmt"
	"strconv"
	"time"
)

// Native stream and tool identities are shared by live projection and replay.
func annotateGrokIdentity(sessionID string, frame map[string]any) {
	params, _ := frame["params"].(map[string]any)
	update, _ := params["update"].(map[string]any)
	if update == nil {
		return
	}
	meta, _ := params["_meta"].(map[string]any)
	if meta == nil {
		meta = map[string]any{}
		params["_meta"] = meta
	}
	if id, _ := meta["astrorderItemId"].(string); id != "" {
		return
	}
	kind, _ := update["sessionUpdate"].(string)
	if callID, _ := update["toolCallId"].(string); callID != "" {
		meta["astrorderItemId"] = "grok-tool:" + sessionID + ":" + callID
		return
	}
	prompt, _ := meta["promptId"].(string)
	if prompt != "" && meta["streamStartMs"] != nil && (kind == "agent_message_chunk" || kind == "agent_thought_chunk") {
		meta["astrorderItemId"] = fmt.Sprintf("grok-stream:%s:%s:%s:%v", sessionID, prompt, kind, meta["streamStartMs"])
		return
	}
	if id, _ := meta["eventId"].(string); id != "" {
		meta["astrorderItemId"] = id
	}
}

func grokText(value any) string {
	switch v := value.(type) {
	case string:
		return v
	case map[string]any:
		if text, ok := v["text"].(string); ok {
			return text
		}
		return grokText(v["content"])
	case []any:
		result := ""
		for _, part := range v {
			result += grokText(part)
		}
		return result
	}
	return ""
}

func grokReplayItem(sessionID string, frame map[string]any) map[string]any {
	annotateGrokIdentity(sessionID, frame)
	params, _ := frame["params"].(map[string]any)
	update, _ := params["update"].(map[string]any)
	if update == nil {
		return nil
	}
	// Retain compatibility with explicit native item snapshots.
	if item, ok := update["item"].(map[string]any); ok {
		return normalizeHistoryItem(item, 0)
	}
	if item, ok := update["message"].(map[string]any); ok {
		return normalizeHistoryItem(item, 0)
	}
	meta, _ := params["_meta"].(map[string]any)
	id, _ := meta["astrorderItemId"].(string)
	if id == "" {
		return nil
	}
	kind, _ := update["sessionUpdate"].(string)
	item := map[string]any{"id": id, "_meta": meta, "role": "assistant", "kind": "message", "text": grokText(update["content"])}
	if ms, err := strconv.ParseInt(fmt.Sprint(meta["agentTimestampMs"]), 10, 64); err == nil {
		item["created_at"] = time.UnixMilli(ms).UTC().Format(time.RFC3339Nano)
	}
	switch kind {
	case "user_message_chunk":
		item["role"] = "user"
	case "agent_thought_chunk":
		item["kind"] = "thinking"
	case "agent_message_chunk":
	case "tool_call", "tool_call_update":
		item["role"] = "tool"
		item["kind"] = "tool"
		tool := map[string]any{"id": update["toolCallId"]}
		for source, target := range map[string]string{"title": "name", "kind": "kind", "status": "status", "rawInput": "input", "rawOutput": "result", "locations": "locations"} {
			if value, present := update[source]; present && value != nil {
				tool[target] = value
			}
		}
		if m, _ := update["_meta"].(map[string]any); m != nil {
			if x, _ := m["x.ai/tool"].(map[string]any); x != nil {
				for _, key := range []string{"name", "kind", "input"} {
					if x[key] != nil {
						tool[key] = x[key]
					}
				}
			}
		}
		if _, present := update["content"]; present {
			tool["output"] = grokText(update["content"])
		}
		item["tool"] = tool
	default:
		return nil
	}
	return item
}

func mergeGrokReplayItem(old, item map[string]any) {
	if item["kind"] == "tool" {
		a, _ := old["tool"].(map[string]any)
		b, _ := item["tool"].(map[string]any)
		for k, v := range b {
			a[k] = v
		}
		if text, _ := item["text"].(string); text != "" {
			old["text"] = text
		}
	} else {
		old["text"] = grokText(old["text"]) + grokText(item["text"])
	}
}

// A response can arrive before queued notifications finish processing.
func (c *client) drainEvents(ctx context.Context) error {
	done := make(chan struct{})
	if !c.queueEvent(func() { close(done) }) {
		return fmt.Errorf("Grok replay event queue is unavailable")
	}
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-c.eventStop:
		return fmt.Errorf("Grok replay transport stopped")
	}
}
