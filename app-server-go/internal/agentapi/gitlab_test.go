package agentapi

import (
	"context"
	"testing"
)

func TestGitLabCapabilitiesEnrichCallerWorkspaceAndConnection(t *testing.T) {
	h, st, _ := setupDelegationTestEnv(t)
	defer st.Close()
	ctx := context.Background()

	_, err := st.UpsertSession(ctx, map[string]any{
		"id":            "parent-sess-gl",
		"agent_id":      "parent-hermes-default",
		"title":         "gitlab-work",
		"workspace":     "/home/dev/acme/app",
		"connection_id": "ssh-devbox",
		"status":        "idle",
		"updated_at":    "2026-11-26T10:00:00Z",
	})
	if err != nil {
		t.Fatalf("upsert session: %v", err)
	}

	var gotPipelines, gotPipeline, gotJobLog map[string]any
	h.SetGitLabCallbacks(
		func(_ context.Context, payload map[string]any) (any, error) {
			gotPipelines = payload
			return map[string]any{"matched": true}, nil
		},
		func(_ context.Context, payload map[string]any) (any, error) {
			gotPipeline = payload
			return map[string]any{"pipeline": map[string]any{"id": 101}}, nil
		},
		func(_ context.Context, payload map[string]any) (any, error) {
			gotJobLog = payload
			return map[string]any{"job_id": 501, "trace": "ok"}, nil
		},
	)

	caller := map[string]any{
		"agent_id":      "parent-hermes-default",
		"connection_id": "local",
		"profile_name":  "default",
		"runtime":       "hermes",
		"session_id":    "parent-sess-gl",
	}

	if _, err := h.Invoke(ctx, "gitlab.pipelines", map[string]any{"caller": caller, "limit": 5}); err != nil {
		t.Fatalf("gitlab.pipelines failed: %v", err)
	}
	if gotPipelines["workspace"] != "/home/dev/acme/app" || gotPipelines["connection_id"] != "ssh-devbox" {
		t.Fatalf("unexpected enriched pipelines payload: %#v", gotPipelines)
	}

	if _, err := h.Invoke(ctx, "gitlab.pipeline", map[string]any{"caller": caller}); err != nil {
		t.Fatalf("gitlab.pipeline failed: %v", err)
	}
	if gotPipeline["workspace"] != "/home/dev/acme/app" || gotPipeline["session_id"] != "parent-sess-gl" {
		t.Fatalf("unexpected enriched pipeline payload: %#v", gotPipeline)
	}

	if _, err := h.Invoke(ctx, "gitlab.job_log", map[string]any{"caller": caller, "job_id": 501, "tail": 100}); err != nil {
		t.Fatalf("gitlab.job_log failed: %v", err)
	}
	if gotJobLog["workspace"] != "/home/dev/acme/app" || gotJobLog["job_id"] != 501 {
		t.Fatalf("unexpected enriched job_log payload: %#v", gotJobLog)
	}
}
