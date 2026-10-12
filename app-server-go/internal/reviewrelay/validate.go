package reviewrelay

import (
	"context"
	"net/http"
	"strings"

	"astrorder.dev/app-server/internal/targetfs"
)

// validateRequest mirrors the legacy Pydantic payload exactly: the workspace
// strings are required, and the four session identifiers are all-or-nothing.
type validateRequest struct {
	SourceWorkspace string  `json:"source_workspace"`
	ReviewWorkspace string  `json:"review_workspace"`
	SourceAgentID   *string `json:"source_agent_id"`
	SourceSessionID *string `json:"source_session_id"`
	ReviewAgentID   *string `json:"review_agent_id"`
	ReviewSessionID *string `json:"review_session_id"`
}

// validate compares the structured Git snapshot of the developer and reviewer
// workspaces. Snapshots are always captured through the session's own
// connection: a remote workspace is never resolved on the App Server, and a
// mismatch fails closed with both snapshots attached.
func (h *Handler) validate(response http.ResponseWriter, request *http.Request) error {
	var payload validateRequest
	if err := decodeJSON(response, request, &payload); err != nil {
		return err
	}
	sourceWorkspace, err := requiredText(&payload.SourceWorkspace, maxWorkspace)
	if err != nil {
		return err
	}
	reviewWorkspace, err := requiredText(&payload.ReviewWorkspace, maxWorkspace)
	if err != nil {
		return err
	}
	provided := 0
	for _, id := range []*string{payload.SourceAgentID, payload.SourceSessionID, payload.ReviewAgentID, payload.ReviewSessionID} {
		if id != nil && *id != "" {
			provided++
		}
	}
	if provided != 0 && provided != 4 {
		return &targetfs.Error{Status: http.StatusUnprocessableEntity, Code: "sessions_required", Detail: "必须同时指定开发与审查会话"}
	}
	connectionID := ""
	if provided == 4 {
		sourceAgentID, sourceSessionID, reviewAgentID, reviewSessionID, err := h.exactSessions(request, payload)
		if err != nil {
			return err
		}
		sourceSession, err := h.options.Store.GetSession(request.Context(), sourceAgentID, sourceSessionID)
		if err != nil {
			return storageError(err)
		}
		reviewSession, err := h.options.Store.GetSession(request.Context(), reviewAgentID, reviewSessionID)
		if err != nil {
			return storageError(err)
		}
		sourceAgent, err := h.options.Store.GetAgent(request.Context(), sourceAgentID)
		if err != nil {
			return storageError(err)
		}
		reviewAgent, err := h.options.Store.GetAgent(request.Context(), reviewAgentID)
		if err != nil {
			return storageError(err)
		}
		if sourceSession == nil || reviewSession == nil || sourceAgent == nil || reviewAgent == nil {
			return &targetfs.Error{Status: http.StatusNotFound, Code: "sessions_not_found", Detail: "开发或审查会话不存在"}
		}
		if mapText(sourceSession, "workspace") != sourceWorkspace || mapText(reviewSession, "workspace") != reviewWorkspace {
			return &targetfs.Error{Status: http.StatusConflict, Code: "workspace_changed", Detail: "会话工作区已变化，请刷新后重试"}
		}
		sourceConnection := firstNonEmpty(mapText(sourceSession, "connection_id"), mapText(sourceAgent, "connection_id"), targetfs.LocalConnectionID)
		reviewConnection := firstNonEmpty(mapText(reviewSession, "connection_id"), mapText(reviewAgent, "connection_id"), targetfs.LocalConnectionID)
		if sourceConnection != reviewConnection {
			return &targetfs.Error{Status: http.StatusConflict, Code: "connection_mismatch", Detail: "开发与审查会话不在同一个运行环境"}
		}
		connectionID = sourceConnection
	}

	source, review, err := h.capturePair(request, connectionID, sourceWorkspace, reviewWorkspace)
	if err != nil {
		return err
	}
	if !targetfs.SnapshotsMatch(source, review) {
		writeJSON(response, http.StatusConflict, map[string]any{
			"detail": map[string]any{"source": source, "review": review},
		})
		return nil
	}
	writeJSON(response, http.StatusOK, map[string]any{"valid": true, "source": source, "review": review})
	return nil
}

func (h *Handler) exactSessions(request *http.Request, payload validateRequest) (string, string, string, string, error) {
	sourceAgentID, err := requiredText(payload.SourceAgentID, maxIDLength)
	if err != nil {
		return "", "", "", "", err
	}
	sourceSessionID, err := requiredText(payload.SourceSessionID, maxIDLength)
	if err != nil {
		return "", "", "", "", err
	}
	reviewAgentID, err := requiredText(payload.ReviewAgentID, maxIDLength)
	if err != nil {
		return "", "", "", "", err
	}
	reviewSessionID, err := requiredText(payload.ReviewSessionID, maxIDLength)
	if err != nil {
		return "", "", "", "", err
	}
	return sourceAgentID, sourceSessionID, reviewAgentID, reviewSessionID, nil
}

// capturePair resolves each workspace through its explicit connection and
// captures both snapshots with the same typed read-only operation.
func (h *Handler) capturePair(request *http.Request, connectionID, sourceWorkspace, reviewWorkspace string) (targetfs.SnapshotWire, targetfs.SnapshotWire, error) {
	if h.options.Resolver == nil {
		return targetfs.SnapshotWire{}, targetfs.SnapshotWire{}, &targetfs.Error{Status: http.StatusServiceUnavailable, Code: "resolver_unavailable", Detail: "Target filesystem is not configured"}
	}
	// A WSL connection is a real connection id that the resolver builds a WSL
	// target command for; it is not an SSH row, and the workspace is resolved
	// inside the WSL target, never on the host.
	if connectionID != "" && connectionID != targetfs.LocalConnectionID && !strings.HasPrefix(connectionID, "wsl:") {
		connection, err := h.options.Store.GetSSHConnection(request.Context(), connectionID)
		if err != nil {
			return targetfs.SnapshotWire{}, targetfs.SnapshotWire{}, storageError(err)
		}
		if connection == nil {
			return targetfs.SnapshotWire{}, targetfs.SnapshotWire{}, &targetfs.Error{Status: http.StatusUnprocessableEntity, Code: "ssh_connection_missing", Detail: "审查环境的 SSH 连接不存在"}
		}
	}
	source, err := h.captureOne(request.Context(), connectionID, sourceWorkspace)
	if err != nil {
		return targetfs.SnapshotWire{}, targetfs.SnapshotWire{}, err
	}
	review, err := h.captureOne(request.Context(), connectionID, reviewWorkspace)
	if err != nil {
		return targetfs.SnapshotWire{}, targetfs.SnapshotWire{}, err
	}
	return source, review, nil
}

func (h *Handler) captureOne(ctx context.Context, connectionID, workspace string) (targetfs.SnapshotWire, error) {
	resolved, err := h.options.Resolver.Resolve(ctx, targetfs.ResolveRequest{Workspace: workspace, ConnectionID: connectionID})
	if err != nil {
		return targetfs.SnapshotWire{}, err
	}
	result, err := resolved.FileSystem.ExecGit(ctx, targetfs.GitRequest{Operation: targetfs.GitSnapshotReadonly, Workspace: resolved.Workspace})
	if err != nil {
		return targetfs.SnapshotWire{}, err
	}
	if result.ExitCode != 0 {
		return targetfs.SnapshotWire{}, &targetfs.Error{Status: http.StatusUnprocessableEntity, Code: "git_snapshot_failed", Detail: "无法读取该工作区的 Git 快照；请确认会话工作区目录。"}
	}
	return targetfs.ParseSnapshot(result.Stdout)
}

func mapText(row map[string]any, key string) string {
	if row == nil || row[key] == nil {
		return ""
	}
	if text, ok := row[key].(string); ok {
		return text
	}
	return ""
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
