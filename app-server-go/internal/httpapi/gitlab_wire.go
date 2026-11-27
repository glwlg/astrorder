package httpapi

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"

	"astrorder.dev/app-server/internal/gitlabci"
)

func bindGitLabCallbacks(agentHandler any, svc *gitlabci.Service) {
	if svc == nil {
		return
	}
	binder, ok := agentHandler.(interface {
		SetGitLabCallbacks(
			func(context.Context, map[string]any) (any, error),
			func(context.Context, map[string]any) (any, error),
			func(context.Context, map[string]any) (any, error),
		)
	})
	if !ok {
		return
	}
	binder.SetGitLabCallbacks(
		func(ctx context.Context, payload map[string]any) (any, error) {
			return svc.InvokeCapability(ctx, "pipelines", payload)
		},
		func(ctx context.Context, payload map[string]any) (any, error) {
			return svc.InvokeCapability(ctx, "pipeline", payload)
		},
		func(ctx context.Context, payload map[string]any) (any, error) {
			return svc.InvokeCapability(ctx, "job_log", payload)
		},
	)
}

func payloadString(payload map[string]any, key string) string {
	v, ok := payload[key].(string)
	if !ok {
		return ""
	}
	return strings.TrimSpace(v)
}

func payloadInt64(payload map[string]any, key string) int64 {
	raw, exists := payload[key]
	if !exists || raw == nil {
		return 0
	}
	switch v := raw.(type) {
	case int:
		return int64(v)
	case int64:
		return v
	case float64:
		return int64(v)
	case json.Number:
		if n, err := v.Int64(); err == nil {
			return n
		}
		if f, err := v.Float64(); err == nil {
			return int64(f)
		}
	case string:
		if n, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64); err == nil {
			return n
		}
	}
	return 0
}
