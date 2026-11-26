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
			return svc.QueryPipelines(ctx, buildGitLabQueryTarget(payload))
		},
		func(ctx context.Context, payload map[string]any) (any, error) {
			pid := payloadInt64(payload, "pipeline_id")
			return svc.QueryPipelineDetail(ctx, buildGitLabQueryTarget(payload), pid)
		},
		func(ctx context.Context, payload map[string]any) (any, error) {
			jid := payloadInt64(payload, "job_id")
			tail := int(payloadInt64(payload, "tail"))
			if tail <= 0 {
				tail = int(payloadInt64(payload, "tail_lines"))
			}
			return svc.QueryJobTrace(ctx, buildGitLabQueryTarget(payload), jid, tail, true)
		},
	)
}

func buildGitLabQueryTarget(payload map[string]any) gitlabci.QueryTarget {
	if payload == nil {
		return gitlabci.QueryTarget{}
	}
	project := payloadString(payload, "project")
	if project == "" {
		project = payloadString(payload, "project_path")
	}
	instanceID := payloadString(payload, "instance_id")
	if instanceID == "" {
		instanceID = payloadString(payload, "instance")
	}
	return gitlabci.QueryTarget{
		Workspace:    payloadString(payload, "workspace"),
		ConnectionID: payloadString(payload, "connection_id"),
		SessionID:    payloadString(payload, "session_id"),
		InstanceID:   instanceID,
		ProjectPath:  project,
		Ref:          payloadString(payload, "ref"),
		SHA:          payloadString(payload, "sha"),
		Limit:        int(payloadInt64(payload, "limit")),
	}
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
