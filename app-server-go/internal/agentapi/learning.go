package agentapi

import "context"

// SetLearningCallbacks wires the Bot-driven Learning Radar callbacks onto an initialized Handler.
func (h *Handler) SetLearningCallbacks(
	statusFn func(context.Context, map[string]any) (any, error),
	ingestFn func(context.Context, map[string]any) (any, error),
	updateFn func(context.Context, map[string]any) (any, error),
	scanFn func(context.Context, map[string]any) (any, error),
) {
	if h == nil {
		return
	}
	h.options.LearningStatus = statusFn
	h.options.LearningIngest = ingestFn
	h.options.LearningUpdate = updateFn
	h.options.LearningScan = scanFn
}

func (h *Handler) invokeLearningStatus(ctx context.Context, payload map[string]any) (any, error) {
	if h.options.LearningStatus == nil {
		return nil, NewAgentApiError("unavailable", "Learning Radar service is not configured")
	}
	return h.options.LearningStatus(ctx, payload)
}

func (h *Handler) invokeLearningIngest(ctx context.Context, payload map[string]any) (any, error) {
	if h.options.LearningIngest == nil {
		return nil, NewAgentApiError("unavailable", "Learning Radar service is not configured")
	}
	return h.options.LearningIngest(ctx, payload)
}

func (h *Handler) invokeLearningUpdate(ctx context.Context, payload map[string]any) (any, error) {
	if h.options.LearningUpdate == nil {
		return nil, NewAgentApiError("unavailable", "Learning Radar service is not configured")
	}
	return h.options.LearningUpdate(ctx, payload)
}

func (h *Handler) invokeLearningScan(ctx context.Context, payload map[string]any) (any, error) {
	if h.options.LearningScan == nil {
		return nil, NewAgentApiError("unavailable", "Learning Radar service is not configured")
	}
	return h.options.LearningScan(ctx, payload)
}
