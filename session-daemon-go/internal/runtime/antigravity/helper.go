package antigravity

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

func intVal(v any) (int, bool) {
	switch n := v.(type) {
	case int:
		return n, true
	case int64:
		return int(n), true
	case float64:
		if n == float64(int(n)) {
			return int(n), true
		}
	case json.Number:
		if i, err := n.Int64(); err == nil {
			return int(i), true
		}
		if f, err := n.Float64(); err == nil && f == float64(int(f)) {
			return int(f), true
		}
	case string:
		if i, err := strconv.Atoi(n); err == nil {
			return i, true
		}
	}
	return 0, false
}

func normalizeMapSlice(raw any) []map[string]any {
	switch typed := raw.(type) {
	case []map[string]any:
		return typed
	case []any:
		out := make([]map[string]any, 0, len(typed))
		for _, elem := range typed {
			if m, ok := elem.(map[string]any); ok {
				out = append(out, m)
			}
		}
		return out
	default:
		return []map[string]any{}
	}
}

func resolveHelperHome(req map[string]any) (string, error) {
	if override := strings.TrimSpace(str(req, "home")); override != "" {
		if strings.ContainsRune(override, 0) || !filepath.IsAbs(override) {
			return "", fmt.Errorf("invalid Antigravity helper home")
		}
		return filepath.Clean(override), nil
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "", fmt.Errorf("unable to determine Antigravity home directory")
	}
	return home, nil
}

// ExecuteHelper handles remote Antigravity storage operations and isolated
// account quota reads inside cmd/astrorder-model-helper.
func ExecuteHelper(ctx context.Context, req map[string]any) (map[string]any, error) {
	home, err := resolveHelperHome(req)
	if err != nil {
		return nil, &RemoteHelperError{Message: err.Error(), BeforeDispatch: true}
	}
	action := str(req, "action")
	switch action {
	case "account.vault.state", "account.vault.capture", "account.vault.quota", "account.vault.check", "account.vault.apply":
		credential, err := credentialField(req["credential"])
		if err != nil {
			return nil, err
		}
		return executeAgyAccount(ctx, home, str(req, "executable"), action, str(req, "account_id"), credential)
	case "session.list":
		items, err := listSessionsForHome(ctx, home, str(req, "agent_id"), str(req, "connection_id"), true)
		if err != nil {
			return nil, err
		}
		return map[string]any{"items": items}, nil

	case "inspect_conversation":
		id := str(req, "conversation_id")
		if !validID(id) {
			return nil, &RemoteHelperError{Message: "invalid Antigravity conversation ID", BeforeDispatch: true}
		}
		exitedOwner, _ := req["exited_owner"].(bool)
		readonly, err := inspectResumeForHome(ctx, home, id, exitedOwner)
		if err != nil {
			return nil, &RemoteHelperError{Message: err.Error(), BeforeDispatch: true}
		}
		return map[string]any{"desktop_readonly": readonly, "resumable": !readonly}, nil

	case "conversation_active":
		id := str(req, "conversation_id")
		if !validID(id) {
			return nil, &RemoteHelperError{Message: "invalid Antigravity conversation ID", BeforeDispatch: true}
		}
		active, known := isConversationNotFullyIdleForHome(ctx, home, id)
		return map[string]any{"active": active, "known": known}, nil

	case "history_page":
		id := str(req, "conversation_id")
		if !validID(id) {
			return nil, &RemoteHelperError{Message: "invalid Antigravity conversation ID", BeforeDispatch: true}
		}
		limit := 50
		if parsed, ok := intVal(req["limit"]); ok {
			limit = parsed
		}
		readonly, _ := req["readonly"].(bool)
		dir := filepath.Join(home, ".gemini", "antigravity-cli", "conversations")
		if readonly {
			dir = filepath.Join(home, ".gemini", "antigravity", "conversations")
		}
		items, next, err := readHistory(ctx, dir, id, str(req, "before"), limit)
		if err != nil {
			return nil, err
		}
		return map[string]any{
			"items":       items,
			"next_cursor": next,
			"has_more":    next != nil,
		}, nil

	case "confirm_deleted":
		id := str(req, "conversation_id")
		if !validID(id) {
			return nil, &RemoteHelperError{Message: "invalid Antigravity conversation ID", BeforeDispatch: true}
		}
		if err := confirmDeletedForHome(ctx, home, id); err != nil {
			return nil, err
		}
		removeConversationFilesForHome(home, id)
		return map[string]any{"confirmed": true}, nil

	case "delete_unbound":
		id := str(req, "conversation_id")
		if !validID(id) {
			return nil, &RemoteHelperError{Message: "invalid Antigravity conversation ID", BeforeDispatch: true}
		}
		allowJustClosed, _ := req["allow_just_closed"].(bool)
		if err := deleteConversationForHome(ctx, home, id, allowJustClosed); err != nil {
			return nil, err
		}
		return map[string]any{"confirmed": true}, nil

	case "session_usage":
		id := str(req, "conversation_id")
		if !validID(id) {
			return nil, &RemoteHelperError{Message: "invalid Antigravity conversation ID", BeforeDispatch: true}
		}
		readonly, _ := req["readonly"].(bool)
		usage, err := readNativeSessionUsage(ctx, home, id, readonly)
		if err != nil {
			return nil, err
		}
		if defModel := readDefaultModelForHome(home); defModel != "" {
			usage["default_model"] = defModel
		}
		return usage, nil

	case "usage_summary":
		sinceMs, _ := int64FromAny(req["since_ms"])
		untilMs, _ := int64FromAny(req["until_ms"])
		return readHelperUsageSummary(ctx, home, sinceMs, untilMs)

	default:
		return nil, &RemoteHelperError{Message: fmt.Sprintf("unsupported Antigravity helper action %q", action), BeforeDispatch: true}
	}
}
