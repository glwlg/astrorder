package agentapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"

	sessiondv1 "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
)

// Caller represents the explicit caller identity passed on wire.
type Caller struct {
	AgentID      string `json:"agent_id"`
	ConnectionID string `json:"connection_id"`
	ProfileName  string `json:"profile_name"`
	Runtime      string `json:"runtime"`
	SessionID    string `json:"session_id"`
}

// NormalizeConnectionID normalizes empty or "local" to "local".
func NormalizeConnectionID(connID string) string {
	c := strings.TrimSpace(connID)
	if c == "" || c == "local" {
		return "local"
	}
	return c
}

// NormalizeProfileName normalizes empty/null profile to "default".
func NormalizeProfileName(profile string) string {
	p := strings.TrimSpace(profile)
	if p == "" || strings.EqualFold(p, "null") {
		return "default"
	}
	return p
}

func hasWhitespace(s string) bool {
	return strings.IndexFunc(s, unicode.IsSpace) >= 0
}

// ParseCaller extracts and validates the Caller struct from wire input.
// All identity fields must be explicit non-empty strings without illegal whitespace.
// Native session ID must not be trimmed or guessed.
// Missing or null fields are strictly rejected.
func ParseCaller(raw any) (*Caller, error) {
	if raw == nil {
		return nil, NewAgentApiError("invalid_caller", "caller object is required and cannot be null")
	}

	callerMap, ok := raw.(map[string]any)
	if !ok {
		return nil, NewAgentApiError("invalid_caller", "caller must be an object")
	}

	rawAgentID, hasAgent := callerMap["agent_id"]
	rawConnID, hasConn := callerMap["connection_id"]
	rawProfile, hasProfile := callerMap["profile_name"]
	rawRuntime, hasRuntime := callerMap["runtime"]
	rawSessionID, hasSession := callerMap["session_id"]

	if !hasAgent || !hasConn || !hasProfile || !hasRuntime || !hasSession {
		return nil, NewAgentApiError("invalid_caller", "caller requires explicit fields: agent_id, connection_id, profile_name, runtime, session_id")
	}

	// Validate each field is a non-empty string without illegal whitespace (including internal whitespace)
	agentID, ok := rawAgentID.(string)
	if !ok || agentID == "" || hasWhitespace(agentID) {
		return nil, NewAgentApiError("invalid_caller", "caller agent_id must be a non-empty string without whitespace")
	}

	connID, ok := rawConnID.(string)
	if !ok || connID == "" || hasWhitespace(connID) {
		return nil, NewAgentApiError("invalid_caller", "caller connection_id must be a non-empty string without whitespace")
	}

	profile, ok := rawProfile.(string)
	if !ok || profile == "" || hasWhitespace(profile) {
		return nil, NewAgentApiError("invalid_caller", "caller profile_name must be a non-empty string without whitespace")
	}

	runtime, ok := rawRuntime.(string)
	if !ok || runtime == "" || hasWhitespace(runtime) {
		return nil, NewAgentApiError("invalid_caller", "caller runtime must be a non-empty string without whitespace")
	}

	sessionID, ok := rawSessionID.(string)
	if !ok || sessionID == "" || hasWhitespace(sessionID) {
		return nil, NewAgentApiError("invalid_caller", "caller session_id must be a non-empty string without whitespace")
	}

	return &Caller{
		AgentID:      agentID,
		ConnectionID: connID,
		ProfileName:  profile,
		Runtime:      runtime,
		SessionID:    sessionID,
	}, nil
}

// ValidateCaller verifies the caller against configured agents, connection, profile,
// runtime kind, and verifies session registration / daemon runtime state.
func (h *Handler) ValidateCaller(ctx context.Context, caller *Caller) (map[string]any, error) {
	if caller == nil {
		return nil, NewAgentApiError("invalid_caller", "caller cannot be nil")
	}

	if h.options.Store == nil {
		return nil, errors.New("store is not available")
	}

	agent, err := h.options.Store.GetAgent(ctx, caller.AgentID)
	if err != nil {
		return nil, fmt.Errorf("failed to get caller agent '%s': %w", caller.AgentID, err)
	}
	if agent == nil {
		return nil, NewAgentApiError("invalid_caller", fmt.Sprintf("caller agent '%s' not found", caller.AgentID))
	}

	// 1. Validate runtime kind matches agent kind
	agentKind, _ := agent["kind"].(string)
	agentKindClean := strings.TrimSpace(strings.ToLower(agentKind))
	if caller.Runtime != agentKindClean {
		return nil, NewAgentApiError("invalid_caller", fmt.Sprintf("caller runtime '%s' does not match agent kind '%s'", caller.Runtime, agentKind))
	}

	// 2. Validate connection_id
	agentConn, _ := agent["connection_id"].(string)
	agentConnNorm := NormalizeConnectionID(agentConn)
	if caller.ConnectionID != agentConnNorm {
		return nil, NewAgentApiError("invalid_caller", fmt.Sprintf("caller connection_id '%s' does not match agent connection '%s'", caller.ConnectionID, agentConnNorm))
	}

	// 3. Validate profile_name
	agentProfile, _ := agent["profile_name"].(string)
	agentProfileNorm := NormalizeProfileName(agentProfile)
	if caller.ProfileName != agentProfileNorm {
		return nil, NewAgentApiError("invalid_caller", fmt.Sprintf("caller profile_name '%s' does not match agent profile '%s'", caller.ProfileName, agentProfileNorm))
	}

	// 4. Verify session identity and registration
	session, err := h.options.Store.GetSession(ctx, caller.AgentID, caller.SessionID)
	if err != nil {
		return nil, fmt.Errorf("failed to lookup caller session: %w", err)
	}

	if session != nil {
		// Verify native alias / ownership matches
		sessAgentID, _ := session["agent_id"].(string)
		if sessAgentID != caller.AgentID {
			return nil, NewAgentApiError("invalid_caller", fmt.Sprintf("caller session belongs to agent '%s', not '%s'", sessAgentID, caller.AgentID))
		}
		return session, nil
	}

	// Session not in local store: external parent session encountered for the first time.
	// Query real daemon runtime to verify existence and register metadata.
	// Do NOT guess from sole active, most recent, or working directory!
	externalSession, err := h.resolveAndRegisterExternalSession(ctx, agent, caller)
	if err != nil {
		return nil, NewAgentApiError("invalid_caller", fmt.Sprintf("failed to verify external caller session via runtime daemon: %v", err))
	}

	return externalSession, nil
}

// resolveAndRegisterExternalSession queries the daemon via QueryNative to verify
// the external session existence and registers its metadata in the store (no message copying).
func (h *Handler) resolveAndRegisterExternalSession(ctx context.Context, agent map[string]any, caller *Caller) (map[string]any, error) {
	if h.options.Client == nil || h.options.Client.Runtime == nil {
		return nil, errors.New("daemon client runtime query is unavailable to verify external session")
	}

	target, err := h.resolveRuntimeTarget(ctx, agent)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve runtime target: %w", err)
	}

	workspace := h.options.DefaultWorkspace
	title := fmt.Sprintf("External %s", caller.SessionID)

	switch caller.Runtime {
	case "codex":
		includeTurns := false
		resp, qErr := h.options.Client.Runtime.QueryNative(ctx, &sessiondv1.NativeQueryRequest{
			Target: target,
			Query: &sessiondv1.NativeQueryRequest_CodexThreadRead{
				CodexThreadRead: &sessiondv1.CodexThreadReadQuery{
					ThreadId:     caller.SessionID,
					IncludeTurns: &includeTurns,
				},
			},
		})
		if qErr != nil {
			return nil, fmt.Errorf("codex thread/read failed for session '%s': %w", caller.SessionID, qErr)
		}
		if resp == nil || resp.GetResult() == nil {
			return nil, fmt.Errorf("codex thread '%s' returned empty response from runtime", caller.SessionID)
		}

		var threadResp struct {
			Thread struct {
				ID    string `json:"id"`
				Title string `json:"title"`
				Cwd   string `json:"cwd"`
			} `json:"thread"`
			ID    string `json:"id"`
			Title string `json:"title"`
			Cwd   string `json:"cwd"`
		}
		if err := json.Unmarshal(resp.GetResult().GetUtf8Json(), &threadResp); err != nil {
			return nil, fmt.Errorf("failed to parse codex thread query response: %w", err)
		}
		thID := threadResp.Thread.ID
		if thID == "" {
			thID = threadResp.ID
		}
		if thID != caller.SessionID {
			return nil, fmt.Errorf("codex thread id '%s' does not match caller session '%s'", thID, caller.SessionID)
		}
		if threadResp.Thread.Cwd != "" {
			workspace = threadResp.Thread.Cwd
		} else if threadResp.Cwd != "" {
			workspace = threadResp.Cwd
		}
		if threadResp.Thread.Title != "" {
			title = threadResp.Thread.Title
		} else if threadResp.Title != "" {
			title = threadResp.Title
		}

	case "hermes":
		// Query Hermes session history with omit_messages: true
		// Canonical daemon maps this to session.resume, returning session_id, stored_session_id, info
		resp, qErr := h.options.Client.Runtime.QueryNative(ctx, &sessiondv1.NativeQueryRequest{
			Target: target,
			Query: &sessiondv1.NativeQueryRequest_HermesSessionHistory{
				HermesSessionHistory: &sessiondv1.HermesSessionHistoryQuery{
					SessionId: caller.SessionID,
					Options: &sessiondv1.NativeJson{
						Schema:   "astrorder.hermes.snapshot.options.v1",
						Utf8Json: []byte(`{"omit_messages":true}`),
					},
				},
			},
		})
		if qErr != nil {
			return nil, fmt.Errorf("hermes session query failed for session '%s': %w", caller.SessionID, qErr)
		}
		if resp == nil || resp.GetResult() == nil {
			return nil, fmt.Errorf("hermes session '%s' returned empty response from runtime", caller.SessionID)
		}

		var parsed struct {
			Result struct {
				SessionID       string         `json:"session_id"`
				StoredSessionID string         `json:"stored_session_id"`
				Info            map[string]any `json:"info"`
			} `json:"result"`
			// Flat fallback
			SessionID       string         `json:"session_id"`
			StoredSessionID string         `json:"stored_session_id"`
			Info            map[string]any `json:"info"`
		}
		if err := json.Unmarshal(resp.GetResult().GetUtf8Json(), &parsed); err != nil {
			return nil, fmt.Errorf("failed to parse hermes session query response: %w", err)
		}

		retSID := parsed.Result.SessionID
		if retSID == "" {
			retSID = parsed.SessionID
		}
		retStoredID := parsed.Result.StoredSessionID
		if retStoredID == "" {
			retStoredID = parsed.StoredSessionID
		}
		info := parsed.Result.Info
		if info == nil {
			info = parsed.Info
		}

		if info == nil {
			return nil, fmt.Errorf("hermes session '%s' returned no native info from runtime", caller.SessionID)
		}

		// Strictly verify profile_name from native info: exact string match without NormalizeProfileName
		rawProf, _ := info["profile_name"].(string)
		if rawProf == "" {
			return nil, fmt.Errorf("hermes session '%s' missing profile_name in native info", caller.SessionID)
		}
		if rawProf != caller.ProfileName {
			return nil, fmt.Errorf("caller profile_name '%s' does not match hermes runtime profile '%s'", caller.ProfileName, rawProf)
		}

		// Exact ID and alias verification
		if retSID != caller.SessionID && retStoredID != caller.SessionID {
			return nil, fmt.Errorf("hermes runtime returned session_id '%s' (stored '%s') not matching caller '%s'", retSID, retStoredID, caller.SessionID)
		}

		if rawCwd, ok := info["cwd"].(string); ok && rawCwd != "" {
			workspace = rawCwd
		}
		if rawTitle, ok := info["title"].(string); ok && rawTitle != "" {
			title = rawTitle
		}

	case "grok":
		return nil, NewAgentApiError("unsupported", "external session verification for grok runtime is not supported; session must be created through Astrorder")

	default:
		return nil, fmt.Errorf("unsupported runtime for external session verification: %s", caller.Runtime)
	}

	// Register metadata in store (metadata only, no copying messages)
	metaSession := map[string]any{
		"id":            caller.SessionID,
		"agent_id":      caller.AgentID,
		"title":         title,
		"workspace":     workspace,
		"status":        "idle",
		"control_state": "external",
		"ephemeral":     false,
		"updated_at":    time.Now().UTC().Format(time.RFC3339Nano),
	}

	saved, saveErr := h.options.Store.UpsertSession(ctx, metaSession)
	if saveErr != nil {
		return nil, fmt.Errorf("failed to register external caller session metadata: %w", saveErr)
	}

	if h.options.Publisher != nil {
		_ = h.options.Publisher.Publish(ctx, "session.upsert", saved)
	}

	return saved, nil
}
