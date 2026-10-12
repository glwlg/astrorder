package sessions

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"time"

	sessiondv1 "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
)

const (
	// A thread that Codex cannot read yet (its rollout is still being written)
	// is retried soon; one that is not a top-level conversation is not.
	nativeRetryUnreadable    = 15 * time.Second
	nativeRetryNoContent     = 2 * time.Second
	nativeRetryNotRegistered = 10 * time.Minute
	nativeRegisterTimeout    = 30 * time.Second
	nativePlaceholderTitle   = "New Session"
	nativeTitleLimit         = 160
)

// nativeRegisterSlots bounds concurrent discovery reads across all Agents.
var nativeRegisterSlots = make(chan struct{}, 2)

// nativeThread is the part of a Codex thread that Astrorder keeps. Messages are
// never copied: they stay in Codex and are read through on demand.
type nativeThread struct {
	ID        string      `json:"id"`
	Name      *string     `json:"name"`
	Preview   string      `json:"preview"`
	Cwd       string      `json:"cwd"`
	Source    any         `json:"source"`
	UpdatedAt json.Number `json:"updatedAt"`
	Ephemeral bool        `json:"ephemeral"`
}

func (t nativeThread) title() string {
	text := ""
	if t.Name != nil {
		text = strings.TrimSpace(*t.Name)
	}
	if text == "" {
		text = strings.TrimSpace(t.Preview)
	}
	if text == "" {
		return t.ID
	}
	if index := strings.IndexAny(text, "\r\n"); index > 0 {
		text = strings.TrimSpace(text[:index])
	}
	if runes := []rune(text); len(runes) > nativeTitleLimit {
		text = string(runes[:nativeTitleLimit])
	}
	return text
}

// kind returns the native source ("vscode", "cli", "exec"), or "" for a
// sub-agent thread, whose source is an object rather than a string.
func (t nativeThread) kind() string {
	source, _ := t.Source.(string)
	return source
}

// registrable reports whether the thread is a conversation the user starts and
// resumes. Sub-agent threads, ephemeral threads and the auto-review guardian
// helper are Codex internals and stay out of the session list.
func (t nativeThread) registrable() bool {
	if t.ID == "" || t.Ephemeral || t.kind() == "" {
		return false
	}
	return !strings.Contains(strings.ToLower(t.title()), "whose request action you are assessing")
}

func (t nativeThread) updatedTime() time.Time {
	if at, ok := unixInstant(t.UpdatedAt); ok {
		return at
	}
	return time.Now().UTC()
}

// sessionRow builds the same row shape Astrorder gives any other native session.
// The status is always idle: a native thread's live state belongs to the Codex
// that runs it, and a status copied here could never be cleared again.
func (t nativeThread) sessionRow(agent map[string]any) map[string]any {
	agentID := stringValue(agent, "id")
	row := map[string]any{
		"id":                t.ID,
		"agent_id":          agentID,
		"title":             t.title(),
		"workspace":         optionalText(t.Cwd),
		"status":            "idle",
		"source_id":         agentID,
		"source_session_id": t.ID,
		"history_state":     "available",
		"control_state":     "owned",
		"native_kind":       t.kind(),
		"updated_at":        t.updatedTime().Format(time.RFC3339Nano),
	}
	if connection := stringValue(agent, "connection_id"); connection != "" && connection != "local" {
		row["connection_id"] = connection
	}
	if name := workspaceBase(t.Cwd); name != "" {
		row["project_name"] = name
	}
	return row
}

func optionalText(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func workspaceBase(path string) string {
	path = strings.TrimRight(strings.ReplaceAll(path, "\\", "/"), "/")
	if index := strings.LastIndex(path, "/"); index >= 0 {
		path = path[index+1:]
	}
	return path
}

// ensureNativeSession adds a discovered thread to the session list when it is
// not there yet, and gives a session that still has Astrorder's placeholder
// title the name Codex shows. It reports whether a new row was added.
func (h *Handler) ensureNativeSession(ctx context.Context, agent map[string]any, thread nativeThread) (bool, error) {
	if !thread.registrable() {
		return false, nil
	}
	agentID := stringValue(agent, "id")
	row, inserted, err := h.store.InsertSessionIfAbsent(ctx, thread.sessionRow(agent))
	if err != nil {
		return false, err
	}
	if inserted {
		h.publishEvent(ctx, "session.upsert", row)
		return true, nil
	}
	existing, err := h.store.GetSession(ctx, agentID, thread.ID)
	if err != nil || existing == nil {
		return false, err
	}
	current := stringValue(existing, "title")
	if (current == nativePlaceholderTitle || current == thread.ID) && thread.title() != thread.ID {
		updated, err := h.store.UpdateSession(ctx, agentID, thread.ID, map[string]any{"title": thread.title()})
		if err != nil {
			return false, err
		}
		if updated != nil {
			h.publishEvent(ctx, "session.upsert", updated)
		}
	}
	return false, nil
}

// RegisterNativeSession is called when a native hook reports activity for a
// session Astrorder does not know. It reads that one thread and lists it, off
// the caller's goroutine: the caller sits inside an event-projection
// transaction and must not wait on a runtime query.
func (h *Handler) RegisterNativeSession(agentID, nativeID string) {
	if agentID == "" || nativeID == "" || h.client == nil || h.client.Runtime == nil {
		return
	}
	key := agentID + "\x00" + nativeID
	if until, ok := h.nativeRetry.Load(key); ok && time.Now().Before(until.(time.Time)) {
		return
	}
	if _, busy := h.nativeBusy.LoadOrStore(key, struct{}{}); busy {
		return
	}
	go func() {
		defer h.nativeBusy.Delete(key)
		select {
		case nativeRegisterSlots <- struct{}{}:
			defer func() { <-nativeRegisterSlots }()
		default:
			return // saturated: the next event for this session tries again
		}
		ctx, cancel := context.WithTimeout(context.Background(), nativeRegisterTimeout)
		defer cancel()
		retry := h.registerNativeByID(ctx, agentID, nativeID)
		h.nativeRetry.Store(key, time.Now().Add(retry))
	}()
}

// registerNativeByID returns how long the same session should not be tried again.
func (h *Handler) registerNativeByID(ctx context.Context, agentID, nativeID string) time.Duration {
	agent, err := h.store.GetAgent(ctx, agentID)
	if err != nil || agent == nil || stringValue(agent, "kind") != "codex" {
		return nativeRetryNotRegistered
	}
	if existing, err := h.store.GetSession(ctx, agentID, nativeID); err != nil || existing != nil {
		return nativeRetryNotRegistered
	}
	target, err := h.resolveRuntimeTarget(ctx, agent)
	if err != nil {
		return nativeRetryUnreadable
	}
	includeTurns := false
	response, err := h.client.Runtime.QueryNative(ctx, &sessiondv1.NativeQueryRequest{
		Target: target,
		Query: &sessiondv1.NativeQueryRequest_CodexThreadRead{
			CodexThreadRead: &sessiondv1.CodexThreadReadQuery{ThreadId: nativeID, IncludeTurns: &includeTurns},
		},
	})
	if err != nil {
		return nativeRetryUnreadable
	}
	var parsed struct {
		Thread nativeThread `json:"thread"`
	}
	if err := parseNativeJSON(response.Result, &parsed); err != nil || parsed.Thread.ID != nativeID {
		return nativeRetryUnreadable
	}
	// A thread that was just opened has no prompt yet, so it has nothing to be
	// named by; the hook for its first prompt arrives moments later.
	if parsed.Thread.title() == parsed.Thread.ID {
		return nativeRetryNoContent
	}
	if _, err := h.ensureNativeSession(ctx, agent, parsed.Thread); err != nil {
		return nativeRetryUnreadable
	}
	return nativeRetryNotRegistered
}

// nativeGuard keeps the discovery state next to the Handler without widening its
// constructor: zero values are ready to use.
type nativeGuard struct {
	nativeRetry sync.Map // agent\x00native id -> time.Time before which not to retry
	nativeBusy  sync.Map // agent\x00native id -> in-flight marker
}
