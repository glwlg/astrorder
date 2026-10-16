package hermes

import (
	"context"
	"crypto/rand"
	"fmt"
	"strings"
)

// Translate only the legacy verbs whose canonical contracts changed. Never
// retry mutations after a transport error or a revision conflict.
func (a *Adapter) canonicalRequest(ctx context.Context, method string, params map[string]any) (string, map[string]any, error) {
	p := make(map[string]any, len(params)+2)
	for k, v := range params {
		p[k] = v
	}
	sid, _ := p["session_id"].(string)
	switch method {
	case "session.history":
		// The shared authority exposes transcript rows in resume snapshots;
		// the legacy stdio-only history verb is not registered there.
		return "session.resume", p, nil
	case "session.resume":
		delete(p, "lazy")
	case "session.create":
		if p["source"] == "local" {
			p["source"] = "cli"
		}
		delete(p, "lazy")
		// Canonical title is a resume-or-create identity, not a display label.
		delete(p, "title")
		if effort, exists := p["reasoning_effort"]; exists {
			p["reasoning"] = effort
			delete(p, "reasoning_effort")
		}
		p["request_id"] = rand.Text()
	case "prompt.submit":
		if p["submission_id"] == nil {
			p["submission_id"] = rand.Text()
		}
		// HUD injects a screen-reading instruction; Astrorder is a normal chat.
		delete(p, "surface")
	case "session.title":
		if _, write := p["title"]; !write {
			return "session.info", p, nil
		}
		return a.canonicalMutation(ctx, sid, "rename", map[string]any{"title": p["title"]})
	case "session.branch":
		payload := map[string]any{}
		if title, ok := p["name"].(string); ok && title != "" {
			payload["title"] = title
		}
		if target, exists := p["target_message_id"]; exists {
			payload["through_message_id"] = target
		}
		return a.canonicalMutation(ctx, sid, "branch", payload)
	case "session.delete":
		return a.canonicalMutation(ctx, sid, "delete", map[string]any{})
	case "session.usage":
		return "session.stats", p, nil
	case "config.set":
		if p["key"] == "model" {
			value, _ := p["value"].(string)
			model, provider, _ := strings.Cut(value, " --provider ")
			provider = strings.TrimSuffix(provider, " --session")
			payload := map[string]any{"model": model}
			if provider != "" {
				payload["provider"] = provider
			}
			return a.canonicalMutation(ctx, sid, "model", payload)
		}
	case "session.interrupt", "session.steer", "approval.respond":
		snapshot, err := a.rpc(ctx, "session.resume", map[string]any{"session_id": sid, "omit_messages": true})
		if err != nil {
			return "", nil, err
		}
		p["execution_generation"] = snapshot["execution_generation"]
		if method == "approval.respond" {
			p["prompt_id"] = p["request_id"]
			delete(p, "request_id")
		}
	}
	return method, p, nil
}

func (a *Adapter) canonicalMutation(ctx context.Context, sid, operation string, payload map[string]any) (string, map[string]any, error) {
	snapshot, err := a.rpc(ctx, "session.resume", map[string]any{"session_id": sid, "omit_messages": true})
	if err != nil {
		return "", nil, err
	}
	if snapshot["revision"] == nil || snapshot["execution_generation"] == nil {
		return "", nil, fmt.Errorf("Hermes owner omitted mutation fence")
	}
	return "session.mutate", map[string]any{"session_id": sid, "request_id": rand.Text(), "expected_revision": snapshot["revision"], "expected_generation": snapshot["execution_generation"], "operation": operation, "payload": payload}, nil
}

func canonicalResult(method string, result map[string]any) map[string]any {
	if method == "session.branch" {
		if child, ok := result["branched_session_id"].(string); ok && child != "" {
			result["stored_session_id"] = child
			result["session_id"] = child
			result["id"] = child
		}
	}
	if method == "session.resume" || method == "session.create" {
		if sid, ok := result["session_id"].(string); ok {
			result["id"] = sid
		} else if ref, ok := result["ref"].(map[string]any); ok {
			result["id"] = ref["session_id"]
		}
	}
	return result
}
