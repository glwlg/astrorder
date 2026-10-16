package sessions

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"time"

	sessiondv1 "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
)

const (
	activityPageSize = 200
	activityMaxPages = 25
)

// handleRefreshActivity reconciles last-activity times with the native stores.
// It is the manual and start-up fallback for the event-driven path: one batch
// native query per Agent, diffed in memory, writing only sessions whose native
// time is newer. It never changes the status of an existing session. For Codex
// it also lists threads that were started directly in Codex and are not in
// Astrorder yet.
func (h *Handler) handleRefreshActivity(w http.ResponseWriter, r *http.Request) {
	if h.client == nil || h.client.Runtime == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "Daemon client is not configured")
		return
	}
	result, err := h.RefreshActivity(r.Context(), r.URL.Query().Get("agent_id"))
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, result)
}

// RefreshActivity refreshes one Agent, or every Codex/Hermes/Grok Agent when agentID
// is empty. A failing Agent is reported in the result and does not stop the others.
func (h *Handler) RefreshActivity(ctx context.Context, agentID string) (map[string]any, error) {
	agents, err := h.store.ListAgents(ctx)
	if err != nil {
		return nil, fmt.Errorf("agent storage is unavailable")
	}
	report := []map[string]any{}
	updated := 0
	for _, agent := range agents {
		id, _ := agent["id"].(string)
		kind, _ := agent["kind"].(string)
		if id == "" || (agentID != "" && id != agentID) || (kind != "codex" && kind != "hermes" && kind != "grok") {
			continue
		}
		entry := map[string]any{"agent_id": id, "kind": kind}
		count, err := h.refreshAgentActivity(ctx, agent, kind)
		if err != nil {
			entry["error"] = err.Error()
		} else {
			entry["updated"] = count
			updated += count
		}
		report = append(report, entry)
	}
	return map[string]any{"updated": updated, "agents": report}, nil
}

func (h *Handler) refreshAgentActivity(ctx context.Context, agent map[string]any, kind string) (int, error) {
	target, err := h.resolveRuntimeTarget(ctx, agent)
	if err != nil {
		return 0, err
	}
	if kind == "grok" {
		return h.refreshGrokActivity(ctx, agent, target)
	}
	var native map[string]time.Time
	var threads []nativeThread
	if kind == "codex" {
		threads, err = h.codexThreads(ctx, target)
		native = make(map[string]time.Time, len(threads))
		for _, thread := range threads {
			if at, ok := unixInstant(thread.UpdatedAt); ok && thread.ID != "" {
				native[thread.ID] = at
			}
		}
	} else {
		native, err = h.hermesActivity(ctx, target)
	}
	if err != nil {
		return 0, err
	}
	agentID, _ := agent["id"].(string)
	count := 0
	// Threads started directly in Codex are listed like any other session. A
	// failure on one thread is not allowed to hide the rest.
	for _, thread := range threads {
		added, regErr := h.ensureNativeSession(ctx, agent, thread)
		if regErr != nil {
			return count, regErr
		}
		if added {
			count++
		}
	}
	rows, err := h.store.ListSessions(ctx, agentID)
	if err != nil {
		return 0, fmt.Errorf("session storage is unavailable")
	}
	for _, row := range rows {
		id := stringValue(row, "id")
		at, ok := native[id]
		if !ok {
			if source := stringValue(row, "source_session_id"); source != "" {
				at, ok = native[source]
			}
		}
		if !ok {
			continue
		}
		updated, changed, err := h.store.AdvanceSessionActivity(ctx, agentID, id, at)
		if err != nil {
			return count, err
		}
		if changed {
			h.publishEvent(ctx, "session.upsert", updated)
			count++
		}
	}
	return count, nil
}

// codexThreadQueries are the thread/list option sets whose union is every
// listed conversation. Codex lists only the threads of the configured model
// provider unless asked for all of them, and its default source filter leaves
// out `codex exec` threads; asking for them explicitly in turn drops a few that
// the default returns, so neither query alone is complete.
var codexThreadQueries = []string{
	`{"modelProviders":[]}`,
	`{"modelProviders":[],"sourceKinds":["cli","vscode","exec"]}`,
	`{"modelProviders":[],"sourceKinds":["appServer","subAgent","subAgentReview","subAgentCompact","subAgentThreadSpawn","subAgentOther","unknown"]}`,
}

// codexThreads reads every thread/list page of every query and merges them by id.
func (h *Handler) codexThreads(ctx context.Context, target *sessiondv1.RuntimeTarget) ([]nativeThread, error) {
	var result []nativeThread
	seen := map[string]bool{}
	for _, query := range codexThreadQueries {
		threads, err := h.codexThreadPages(ctx, target, query)
		if err != nil {
			return nil, err
		}
		for _, thread := range threads {
			if thread.ID != "" && !seen[thread.ID] {
				seen[thread.ID] = true
				result = append(result, thread)
			}
		}
	}
	return result, nil
}

func (h *Handler) codexThreadPages(ctx context.Context, target *sessiondv1.RuntimeTarget, options string) ([]nativeThread, error) {
	var result []nativeThread
	var cursor *sessiondv1.NullableString
	native := &sessiondv1.NativeJson{Schema: "astrorder.codex.thread-list.options.v1", Utf8Json: []byte(options)}
	for page := 0; page < activityMaxPages; page++ {
		limit := uint32(activityPageSize)
		archived := false
		response, err := h.client.Runtime.QueryNative(ctx, &sessiondv1.NativeQueryRequest{
			Target: target,
			Query: &sessiondv1.NativeQueryRequest_CodexThreadList{
				CodexThreadList: &sessiondv1.CodexThreadListQuery{Limit: &limit, Cursor: cursor, Archived: &archived, Options: native},
			},
		})
		if err != nil {
			return nil, fmt.Errorf("native thread list failed: %w", err)
		}
		var parsed struct {
			Data       []nativeThread `json:"data"`
			NextCursor *string        `json:"nextCursor"`
		}
		if err := parseNativeJSON(response.Result, &parsed); err != nil {
			return nil, fmt.Errorf("native thread list is invalid: %w", err)
		}
		result = append(result, parsed.Data...)
		if parsed.NextCursor == nil || *parsed.NextCursor == "" {
			break
		}
		cursor = &sessiondv1.NullableString{State: &sessiondv1.NullableString_Value{Value: *parsed.NextCursor}}
	}
	return result, nil
}

// hermesActivity reads projects.tree and collects every object that carries an
// id and last_active, so it does not depend on the tree's grouping layout.
func (h *Handler) hermesActivity(ctx context.Context, target *sessiondv1.RuntimeTarget) (map[string]time.Time, error) {
	response, err := h.client.Runtime.QueryNative(ctx, &sessiondv1.NativeQueryRequest{
		Target: target,
		Query:  &sessiondv1.NativeQueryRequest_HermesProjectsTree{HermesProjectsTree: &sessiondv1.HermesProjectsTreeQuery{}},
	})
	if err != nil {
		return nil, fmt.Errorf("native project tree failed: %w", err)
	}
	var tree any
	if err := parseNativeJSON(response.Result, &tree); err != nil {
		return nil, fmt.Errorf("native project tree is invalid: %w", err)
	}
	result := map[string]time.Time{}
	collectLastActive(tree, result)
	return result, nil
}

func collectLastActive(node any, out map[string]time.Time) {
	switch value := node.(type) {
	case map[string]any:
		if id, _ := value["id"].(string); id != "" {
			if number, ok := value["last_active"].(json.Number); ok {
				if at, ok := unixInstant(number); ok {
					if previous, exists := out[id]; !exists || at.After(previous) {
						out[id] = at
					}
				}
			}
		}
		for _, child := range value {
			collectLastActive(child, out)
		}
	case []any:
		for _, child := range value {
			collectLastActive(child, out)
		}
	}
}

// unixInstant converts Unix seconds (integer or fractional) to a time, rejecting
// zero, negative and non-finite values.
func unixInstant(number json.Number) (time.Time, bool) {
	seconds, err := number.Float64()
	if err != nil || seconds <= 0 || math.IsNaN(seconds) || math.IsInf(seconds, 0) {
		return time.Time{}, false
	}
	whole := int64(seconds)
	return time.Unix(whole, int64((seconds-float64(whole))*1e9)).UTC(), true
}
