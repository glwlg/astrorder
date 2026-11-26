package agentapi

import (
	"context"
	"strings"
)

// SetGitLabCallbacks wires the GitLab CI query service callbacks onto an initialized Handler.
func (h *Handler) SetGitLabCallbacks(
	pipelinesFn func(context.Context, map[string]any) (any, error),
	pipelineFn func(context.Context, map[string]any) (any, error),
	jobLogFn func(context.Context, map[string]any) (any, error),
) {
	if h == nil {
		return
	}
	h.options.GitLabPipelines = pipelinesFn
	h.options.GitLabPipeline = pipelineFn
	h.options.GitLabJobLog = jobLogFn
}

func (h *Handler) enrichGitLabPayload(ctx context.Context, payload map[string]any) map[string]any {
	enriched := make(map[string]any, len(payload)+4)
	for k, v := range payload {
		enriched[k] = v
	}

	callerAID, _ := payload["caller_agent_id"].(string)
	callerSID, _ := payload["caller_session_id"].(string)
	callerConn, _ := payload["caller_connection_id"].(string)

	wsStr, _ := enriched["workspace"].(string)
	connStr, _ := enriched["connection_id"].(string)
	sessStr, _ := enriched["session_id"].(string)
	if strings.TrimSpace(sessStr) == "" && strings.TrimSpace(callerSID) != "" {
		enriched["session_id"] = strings.TrimSpace(callerSID)
	}

	if callerAID != "" && callerSID != "" && h.options.Store != nil {
		if sess, err := h.options.Store.GetSession(ctx, callerAID, callerSID); err == nil && sess != nil {
			if strings.TrimSpace(wsStr) == "" {
				if sws, ok := sess["workspace"].(string); ok && strings.TrimSpace(sws) != "" {
					enriched["workspace"] = strings.TrimSpace(sws)
				}
			}
			if strings.TrimSpace(connStr) == "" {
				if cid, ok := sess["connection_id"].(string); ok && strings.TrimSpace(cid) != "" {
					enriched["connection_id"] = strings.TrimSpace(cid)
				}
			}
		}
	}

	if cid, _ := enriched["connection_id"].(string); strings.TrimSpace(cid) == "" && strings.TrimSpace(callerConn) != "" && !strings.EqualFold(strings.TrimSpace(callerConn), "local") {
		enriched["connection_id"] = strings.TrimSpace(callerConn)
	}
	if ws, _ := enriched["workspace"].(string); strings.TrimSpace(ws) == "" && strings.TrimSpace(h.options.DefaultWorkspace) != "" {
		// Only fallback to DefaultWorkspace when no explicit project was specified.
		proj, _ := enriched["project"].(string)
		projPath, _ := enriched["project_path"].(string)
		if strings.TrimSpace(proj) == "" && strings.TrimSpace(projPath) == "" {
			enriched["workspace"] = strings.TrimSpace(h.options.DefaultWorkspace)
		}
	}
	return enriched
}

func (h *Handler) invokeGitLabPipelines(ctx context.Context, payload map[string]any) (any, error) {
	if h.options.GitLabPipelines == nil {
		return nil, NewAgentApiError("unavailable", "GitLab CI service is not configured")
	}
	return h.options.GitLabPipelines(ctx, h.enrichGitLabPayload(ctx, payload))
}

func (h *Handler) invokeGitLabPipeline(ctx context.Context, payload map[string]any) (any, error) {
	if h.options.GitLabPipeline == nil {
		return nil, NewAgentApiError("unavailable", "GitLab CI service is not configured")
	}
	return h.options.GitLabPipeline(ctx, h.enrichGitLabPayload(ctx, payload))
}

func (h *Handler) invokeGitLabJobLog(ctx context.Context, payload map[string]any) (any, error) {
	if h.options.GitLabJobLog == nil {
		return nil, NewAgentApiError("unavailable", "GitLab CI service is not configured")
	}
	return h.options.GitLabJobLog(ctx, h.enrichGitLabPayload(ctx, payload))
}
