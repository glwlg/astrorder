package agentapi

import (
	"context"
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
	return h.enrichExtensionPayload(ctx, payload)
}

func (h *Handler) invokeGitLabPipelines(ctx context.Context, payload map[string]any) (any, error) {
	enriched := h.enrichExtensionPayload(ctx, payload)
	if h.options.GitLabPipelines != nil {
		return h.options.GitLabPipelines(ctx, enriched)
	}
	if h.options.Extensions != nil {
		return h.options.Extensions.InvokeAgentCapability(ctx, "gitlab-ci-viewer", "pipelines", enriched)
	}
	return nil, NewAgentApiError("unavailable", "GitLab CI service is not configured")
}

func (h *Handler) invokeGitLabPipeline(ctx context.Context, payload map[string]any) (any, error) {
	enriched := h.enrichExtensionPayload(ctx, payload)
	if h.options.GitLabPipeline != nil {
		return h.options.GitLabPipeline(ctx, enriched)
	}
	if h.options.Extensions != nil {
		return h.options.Extensions.InvokeAgentCapability(ctx, "gitlab-ci-viewer", "pipeline", enriched)
	}
	return nil, NewAgentApiError("unavailable", "GitLab CI service is not configured")
}

func (h *Handler) invokeGitLabJobLog(ctx context.Context, payload map[string]any) (any, error) {
	enriched := h.enrichExtensionPayload(ctx, payload)
	if h.options.GitLabJobLog != nil {
		return h.options.GitLabJobLog(ctx, enriched)
	}
	if h.options.Extensions != nil {
		return h.options.Extensions.InvokeAgentCapability(ctx, "gitlab-ci-viewer", "job_log", enriched)
	}
	return nil, NewAgentApiError("unavailable", "GitLab CI service is not configured")
}
