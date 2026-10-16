package sessions

import (
	pb "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
	"context"
)

// A metadata-only thread/read has turns:[] even for nonempty conversations.
// Only includeTurns:true on the independent native catalog proves emptiness.
// Never infer it from age, title, idle status, or the App's message tables.
func (h *Handler) nativeCodexThreadEmpty(ctx context.Context, row map[string]any, nativeID string) bool {
	if ctx.Err() != nil || h.client == nil || h.client.Runtime == nil {
		return false
	}
	agent, err := h.store.GetAgent(ctx, stringValue(row, "agent_id"))
	if err != nil || stringValue(agent, "kind") != "codex" {
		return false
	}
	target, err := h.resolveRuntimeTarget(ctx, agent)
	if err != nil {
		return false
	}
	include := true
	response, err := h.client.Runtime.QueryNative(ctx, &pb.NativeQueryRequest{Target: target, Query: &pb.NativeQueryRequest_CodexThreadRead{CodexThreadRead: &pb.CodexThreadReadQuery{ThreadId: nativeID, IncludeTurns: &include}}})
	if err != nil || response == nil {
		return false
	}
	var payload struct {
		Thread map[string]any `json:"thread"`
	}
	if parseNativeJSON(response.Result, &payload) != nil || stringValue(payload.Thread, "id") != nativeID {
		return false
	}
	turns, ok := payload.Thread["turns"].([]any)
	return ok && len(turns) == 0
}
