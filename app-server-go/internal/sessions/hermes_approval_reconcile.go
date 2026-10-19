package sessions

import (
	pb "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
	"context"
	"fmt"
)

// Reconcile only requests captured before the native snapshot. A request that
// arrives during the read must not be retired by an older snapshot.
func (h *Handler) refreshHermesApprovals(ctx context.Context, session map[string]any) error {
	aid, sid := stringValue(session, "agent_id"), stringValue(session, "id")
	agent, err := h.store.GetAgent(ctx, aid)
	if err != nil {
		return err
	}
	if stringValue(agent, "kind") != "hermes" {
		return nil
	}
	pending, err := h.store.PendingApprovals(ctx)
	if err != nil {
		return err
	}
	var captured []map[string]any
	for _, p := range pending {
		if p["agent_id"] == aid && p["session_id"] == sid {
			captured = append(captured, p)
		}
	}
	if len(captured) == 0 {
		return nil
	}
	nativeID := stringValue(session, "source_session_id")
	if nativeID == "" {
		nativeID = sid
	}
	target, err := h.resolveRuntimeTarget(ctx, agent)
	if err != nil {
		return err
	}
	response, err := h.client.Runtime.QueryNative(ctx, &pb.NativeQueryRequest{Target: target, Query: &pb.NativeQueryRequest_HermesSessionHistory{HermesSessionHistory: &pb.HermesSessionHistoryQuery{SessionId: nativeID, Options: &pb.NativeJson{Schema: "astrorder.hermes.snapshot.options.v1", Utf8Json: []byte(`{"omit_messages":true}`)}}}})
	if err != nil {
		return err
	}
	var snapshot map[string]any
	if err = parseNativeJSON(response.Result, &snapshot); err != nil {
		return err
	}
	if result, ok := snapshot["result"].(map[string]any); ok {
		snapshot = result
	}
	return h.reconcileHermesApprovals(ctx, aid, sid, nativeID, captured, snapshot)
}

func (h *Handler) reconcileHermesApprovals(ctx context.Context, aid, sid, nativeID string, captured []map[string]any, snapshot map[string]any) error {
	if snapshot["session_id"] != nativeID || snapshot["revision"] == nil || snapshot["execution_generation"] == nil {
		return fmt.Errorf("Hermes approval snapshot identity is unconfirmed")
	}
	prompts, ok := snapshot["prompts"].([]any)
	if !ok {
		return fmt.Errorf("Hermes approval snapshot omitted prompts")
	}
	live := map[string]bool{}
	for _, raw := range prompts {
		p, ok := raw.(map[string]any)
		if !ok {
			return fmt.Errorf("invalid Hermes prompt")
		}
		id, ok := p["prompt_id"].(string)
		if !ok || id == "" {
			return fmt.Errorf("Hermes prompt identity is unconfirmed")
		}
		live[id] = true
	}
	for _, p := range captured {
		if p["agent_id"] != aid || p["session_id"] != sid || p["state"] != "pending" {
			continue
		}
		id := stringValue(p, "id")
		native := stringValue(p, "native_id")
		if native == "" {
			native = id
		}
		if id == "" || live[native] {
			continue
		}
		if err := h.publisher.Publish(ctx, "approval.upsert", map[string]any{"id": id, "agent_id": aid, "session_id": sid, "state": "resolved"}); err != nil {
			return err
		}
	}
	return nil
}
