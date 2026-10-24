package agentapi

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"astrorder.dev/app-server/internal/sessions"
	sessiondv1 "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
)

const unboundCursorPrefix = "items:"

type gatewayHistoryCursor struct {
	Version int    `json:"version"`
	Session string `json:"session"`
	Anchor  string `json:"anchor"`
}

func hermesRowAnchor(value any) (string, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", sha256.Sum256(raw)), nil
}

// readCodexHistoryUnbound reads a Codex thread's items via QueryNative without taking over the thread.
func (h *Handler) readCodexHistoryUnbound(ctx context.Context, session map[string]any, agent map[string]any, nativeID, before string, limit int) (items []map[string]any, next any, err error) {
	aid, _ := session["agent_id"].(string)
	sid, _ := session["id"].(string)
	source, _ := session["source_id"].(string)
	if source == "" {
		source = aid
	}

	kind, _ := agent["kind"].(string)
	if kind != "codex" || h.options.Client == nil || h.options.Client.Runtime == nil {
		return nil, nil, fmt.Errorf("unbound history is only available for codex")
	}

	target, err := h.resolveRuntimeTarget(ctx, agent)
	if err != nil {
		return nil, nil, err
	}

	query := &sessiondv1.CodexThreadItemsListQuery{
		ThreadId: nativeID,
		Limit:    uint32(limit),
	}
	descending := "desc"
	query.SortDirection = &descending
	if before != "" {
		if !strings.HasPrefix(before, unboundCursorPrefix) {
			return nil, nil, fmt.Errorf("history cursor belongs to another read path")
		}
		cursorVal := strings.TrimPrefix(before, unboundCursorPrefix)
		query.Cursor = &sessiondv1.NullableString{
			State: &sessiondv1.NullableString_Value{Value: cursorVal},
		}
	}

	response, err := h.options.Client.Runtime.QueryNative(ctx, &sessiondv1.NativeQueryRequest{
		Target: target,
		Query:  &sessiondv1.NativeQueryRequest_CodexThreadItemsList{CodexThreadItemsList: query},
	})
	if err != nil {
		return nil, nil, err
	}
	if response == nil || response.GetResult() == nil || len(response.GetResult().GetUtf8Json()) == 0 {
		return nil, nil, fmt.Errorf("empty native query response")
	}

	var page struct {
		Data       []map[string]any `json:"data"`
		NextCursor *string          `json:"nextCursor"`
	}
	decoder := json.NewDecoder(bytes.NewReader(response.GetResult().GetUtf8Json()))
	decoder.UseNumber()
	if err := decoder.Decode(&page); err != nil {
		return nil, nil, err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return nil, nil, fmt.Errorf("trailing native json")
	}

	for i := len(page.Data) - 1; i >= 0; i-- {
		for _, projected := range sessions.ProjectNativeHistoryItem(page.Data[i], aid, sid, nativeID, source, "codex") {
			items = append(items, publicMessage(projected))
		}
	}
	if page.NextCursor != nil && *page.NextCursor != "" {
		next = unboundCursorPrefix + *page.NextCursor
	}
	return items, next, nil
}

// readHermesHistoryUnbound reads Hermes session messages via QueryNative without acquiring a conversation handle.
func (h *Handler) readHermesHistoryUnbound(ctx context.Context, session map[string]any, agent map[string]any, nativeID, before string, limit int) (items []map[string]any, next any, err error) {
	aid, _ := session["agent_id"].(string)
	sid, _ := session["id"].(string)
	source, _ := session["source_id"].(string)
	if source == "" {
		source = aid
	}

	kind, _ := agent["kind"].(string)
	if kind != "hermes" || h.options.Client == nil || h.options.Client.Runtime == nil {
		return nil, nil, fmt.Errorf("unbound history is only available for hermes")
	}

	target, err := h.resolveRuntimeTarget(ctx, agent)
	if err != nil {
		return nil, nil, err
	}

	// Raw session.history returns full messages snapshot; do not pass limit/before to query
	query := &sessiondv1.HermesSessionHistoryQuery{
		SessionId: nativeID,
	}

	response, err := h.options.Client.Runtime.QueryNative(ctx, &sessiondv1.NativeQueryRequest{
		Target: target,
		Query:  &sessiondv1.NativeQueryRequest_HermesSessionHistory{HermesSessionHistory: query},
	})
	if err != nil {
		return nil, nil, err
	}
	if response == nil || response.GetResult() == nil || len(response.GetResult().GetUtf8Json()) == 0 {
		return nil, nil, fmt.Errorf("empty native query response")
	}

	var parsed struct {
		Result struct {
			SessionID       string         `json:"session_id"`
			StoredSessionID string         `json:"stored_session_id"`
			Messages        []any          `json:"messages"`
			Info            map[string]any `json:"info"`
		} `json:"result"`
		SessionID       string         `json:"session_id"`
		StoredSessionID string         `json:"stored_session_id"`
		Messages        []any          `json:"messages"`
		Info            map[string]any `json:"info"`
	}
	decoder := json.NewDecoder(bytes.NewReader(response.GetResult().GetUtf8Json()))
	decoder.UseNumber()
	if err := decoder.Decode(&parsed); err != nil {
		return nil, nil, fmt.Errorf("failed to parse hermes session query response: %w", err)
	}
	if decoder.Decode(new(any)) != io.EOF {
		return nil, nil, fmt.Errorf("trailing native json")
	}
	info := parsed.Result.Info
	if info == nil {
		info = parsed.Info
	}
	profile, _ := info["profile_name"].(string)
	expectedProfile, _ := agent["profile_name"].(string)
	if profile == "" || profile != NormalizeProfileName(expectedProfile) {
		return nil, nil, NewAgentApiError("runtime_error", "Native history profile mismatch")
	}
	items = []map[string]any{}

	retSID := parsed.Result.SessionID
	if retSID == "" {
		retSID = parsed.SessionID
	}
	retStoredID := parsed.Result.StoredSessionID
	if retStoredID == "" {
		retStoredID = parsed.StoredSessionID
	}

	// Strictly verify session identity
	if retSID == "" && retStoredID == "" {
		return nil, nil, NewAgentApiError("runtime_error", fmt.Sprintf("hermes session '%s' returned empty session identity", nativeID))
	}
	if retSID != nativeID && retStoredID != nativeID {
		return nil, nil, NewAgentApiError("runtime_error", fmt.Sprintf("Native history identity mismatch: runtime session '%s' (stored '%s') != '%s'", retSID, retStoredID, nativeID))
	}

	rawMsgs := parsed.Result.Messages
	if rawMsgs == nil {
		rawMsgs = parsed.Messages
	}
	if rawMsgs == nil {
		return nil, nil, NewAgentApiError("runtime_error", fmt.Sprintf("Native history is unavailable: hermes session '%s' returned no messages in history snapshot", nativeID))
	}

	end := len(rawMsgs)
	if before != "" {
		if !strings.HasPrefix(before, "hermes-rpc:") || len(before) > 512 {
			return nil, nil, NewAgentApiError("invalid_input", "invalid gateway history cursor")
		}
		raw, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(before, "hermes-rpc:"))
		if err != nil {
			return nil, nil, NewAgentApiError("invalid_input", "invalid gateway history cursor")
		}
		var cursor gatewayHistoryCursor
		cDec := json.NewDecoder(bytes.NewReader(raw))
		cDec.DisallowUnknownFields()
		if cDec.Decode(&cursor) != nil || cDec.Decode(&struct{}{}) != io.EOF || cursor.Version != 1 || (cursor.Session != nativeID && cursor.Session != sid) || cursor.Anchor == "" {
			return nil, nil, NewAgentApiError("invalid_input", "foreign gateway history cursor")
		}
		end = -1
		for i, item := range rawMsgs {
			anchor, err := hermesRowAnchor(item)
			if err != nil {
				return nil, nil, err
			}
			if anchor == cursor.Anchor {
				if end != -1 {
					return nil, nil, NewAgentApiError("invalid_input", "ambiguous gateway history anchor")
				}
				end = i
			}
		}
		if end < 0 {
			return nil, nil, NewAgentApiError("invalid_input", "gateway history anchor is unavailable")
		}
	}

	start := end - limit
	if start < 0 {
		start = 0
	}
	for i := start; i < end; i++ {
		var itemMap map[string]any
		switch m := rawMsgs[i].(type) {
		case map[string]any:
			itemMap = m
		default:
			rawBytes, _ := json.Marshal(m)
			_ = json.Unmarshal(rawBytes, &itemMap)
		}
		if itemMap != nil {
			for _, projected := range sessions.ProjectNativeHistoryItem(itemMap, aid, sid, nativeID, source, "hermes") {
				items = append(items, publicMessage(projected))
			}
		}
	}

	if start > 0 {
		anchor, err := hermesRowAnchor(rawMsgs[start])
		if err != nil {
			return nil, nil, err
		}
		raw, _ := json.Marshal(gatewayHistoryCursor{Version: 1, Session: nativeID, Anchor: anchor})
		token := "hermes-rpc:" + base64.RawURLEncoding.EncodeToString(raw)
		if len(token) > 512 {
			return nil, nil, NewAgentApiError("runtime_error", "gateway history cursor exceeds limit")
		}
		next = token
	}
	return items, next, nil
}
