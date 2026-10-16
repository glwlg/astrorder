package sessions

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	sessiondv1 "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
)

// unboundCursorPrefix marks a history cursor issued by the read-only path so it
// is never confused with a cursor from the bound daemon path.
const unboundCursorPrefix = "items:"

// readCodexHistoryUnbound reads a Codex thread's items without binding the
// thread to the daemon. Binding needs thread/resume, which Codex refuses while
// another client (for example Codex Desktop) is the active writer or the thread
// is archived. Reading history needs neither, so it must not depend on binding.
// The transcript remains native; missing child metadata is restored separately.
func (h *Handler) readCodexHistoryUnbound(ctx context.Context, row map[string]any, nativeID, before string, limit int) (items []map[string]any, next any, err error) {
	aid, sid := stringValue(row, "agent_id"), stringValue(row, "id")
	agent, err := h.store.GetAgent(ctx, aid)
	if err != nil {
		return nil, nil, err
	}
	if stringValue(agent, "kind") != "codex" || h.client == nil || h.client.Runtime == nil {
		return nil, nil, fmt.Errorf("unbound history is only available for codex")
	}
	target, err := h.resolveRuntimeTarget(ctx, agent)
	if err != nil {
		return nil, nil, err
	}
	query := &sessiondv1.CodexThreadItemsListQuery{ThreadId: nativeID, Limit: uint32(limit)}
	descending := "desc"
	query.SortDirection = &descending
	if before != "" {
		if !strings.HasPrefix(before, unboundCursorPrefix) {
			return nil, nil, fmt.Errorf("history cursor belongs to another read path")
		}
		query.Cursor = &sessiondv1.NullableString{State: &sessiondv1.NullableString_Value{Value: strings.TrimPrefix(before, unboundCursorPrefix)}}
	}
	response, err := h.client.Runtime.QueryNative(ctx, &sessiondv1.NativeQueryRequest{
		Target: target,
		Query:  &sessiondv1.NativeQueryRequest_CodexThreadItemsList{CodexThreadItemsList: query},
	})
	if err != nil {
		if before == "" && h.nativeCodexThreadEmpty(ctx, row, nativeID) {
			return []map[string]any{}, nil, nil
		}
		return nil, nil, err
	}
	var page struct {
		Data       []map[string]any `json:"data"`
		NextCursor *string          `json:"nextCursor"`
	}
	if err := parseNativeJSON(response.Result, &page); err != nil {
		return nil, nil, err
	}
	source := stringValue(row, "source_id")
	if source == "" {
		source = aid
	}
	// Codex returns newest first; the transcript reads oldest first.
	for _, item := range page.Data {
		if err := h.restoreNativeTaskItem(ctx, aid, sid, item); err != nil {
			return nil, nil, err
		}
	}
	for i := len(page.Data) - 1; i >= 0; i-- {
		items = append(items, projectHistoryItem(page.Data[i], aid, sid, nativeID, source, "codex")...)
	}
	if page.NextCursor != nil && *page.NextCursor != "" {
		next = unboundCursorPrefix + *page.NextCursor
	}
	return items, next, nil
}

func logUnboundHistory(row map[string]any, bindErr, readErr error) {
	slog.Warn("native history read without binding",
		"agent_id", stringValue(row, "agent_id"), "session_id", stringValue(row, "id"),
		"binding_error", bindErr, "unbound_read_error", readErr)
}
