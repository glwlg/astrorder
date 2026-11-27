package agentapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

type stubExtensionDispatcher struct {
	caps        []ExtensionCapability
	lastPlugin  string
	lastCap     string
	lastPayload map[string]any
	resp        any
	err         error
}

func (s *stubExtensionDispatcher) ListAgentCapabilities(ctx context.Context) []ExtensionCapability {
	return s.caps
}

func (s *stubExtensionDispatcher) InvokeAgentCapability(ctx context.Context, pluginID, capID string, payload map[string]any) (any, error) {
	s.lastPlugin = pluginID
	s.lastCap = capID
	s.lastPayload = payload
	if s.err != nil {
		return nil, s.err
	}
	return s.resp, nil
}

func (s *stubExtensionDispatcher) ResolveCLIAlias(ctx context.Context, alias []string) (string, string, bool) {
	return "", "", false
}

func TestExtensionsDynamicCatalogAndInvoke(t *testing.T) {
	h, st, _ := setupDelegationTestEnv(t)
	defer st.Close()
	h.options.Authorizer = &mockTestAuthorizer{}
	ctx := context.Background()

	_, err := st.UpsertSession(ctx, map[string]any{
		"id":            "parent-sess-ext",
		"agent_id":      "parent-hermes-default",
		"title":         "ext-work",
		"workspace":     "/home/dev/acme/service",
		"connection_id": "ssh-build01",
		"status":        "idle",
		"updated_at":    "2026-11-27T10:00:00Z",
	})
	if err != nil {
		t.Fatalf("upsert session: %v", err)
	}

	disp := &stubExtensionDispatcher{
		caps: []ExtensionCapability{
			{
				PluginID:       "com.acme.hello",
				PluginName:     "Hello Plugin",
				CapabilityID:   "echo",
				FullID:         "ext.com.acme.hello.echo",
				Summary:        "Echo payload with workspace git info",
				InputSchema:    map[string]any{"message": "text to echo"},
				RequiresCaller: true,
			},
			{
				PluginID:       "gitlab-ci-viewer",
				PluginName:     "GitLab CI 流水线",
				CapabilityID:   "pipelines",
				FullID:         "ext.gitlab-ci-viewer.pipelines",
				CLIAlias:       []string{"gitlab", "pipelines"},
				Summary:        "List GitLab CI pipelines",
				RequiresCaller: true,
			},
		},
		resp: map[string]any{"ok": true, "echoed": "hi"},
	}
	h.SetExtensions(disp)

	// 1. GET /api/v1/agent/catalog includes ext.list + dynamic extension capabilities
	catW := httptest.NewRecorder()
	h.ServeHTTP(catW, httptest.NewRequest(http.MethodGet, "/api/v1/agent/catalog", nil))
	if catW.Code != http.StatusOK {
		t.Fatalf("catalog status=%d body=%s", catW.Code, catW.Body.String())
	}
	var catBody struct {
		Items []Capability `json:"items"`
	}
	if err := json.Unmarshal(catW.Body.Bytes(), &catBody); err != nil {
		t.Fatalf("decode catalog: %v", err)
	}
	seen := map[string]bool{}
	for _, item := range catBody.Items {
		seen[item.ID] = true
	}
	for _, want := range []string{"ext.list", "ext.com.acme.hello.echo", "ext.gitlab-ci-viewer.pipelines"} {
		if !seen[want] {
			t.Fatalf("catalog missing %q", want)
		}
	}

	// 2. ext.list returns registered extension capabilities
	listRes, err := h.Invoke(ctx, "ext.list", nil)
	if err != nil {
		t.Fatalf("ext.list failed: %v", err)
	}
	listMap, _ := listRes.(map[string]any)
	items, _ := listMap["items"].([]ExtensionCapability)
	if len(items) != 2 || items[0].FullID != "ext.com.acme.hello.echo" {
		t.Fatalf("unexpected ext.list items: %#v", listRes)
	}

	// 3. ext.<dotted.plugin.id>.<cap_id> splits on last dot and enriches caller workspace/connection/session
	caller := map[string]any{
		"agent_id":      "parent-hermes-default",
		"connection_id": "local",
		"profile_name":  "default",
		"runtime":       "hermes",
		"session_id":    "parent-sess-ext",
	}
	invokeBody, _ := json.Marshal(map[string]any{
		"capability": "ext.com.acme.hello.echo",
		"input":      map[string]any{"caller": caller, "message": "hi"},
	})
	invW := httptest.NewRecorder()
	h.ServeHTTP(invW, httptest.NewRequest(http.MethodPost, "/api/v1/agent/invoke", bytes.NewReader(invokeBody)))
	if invW.Code != http.StatusOK {
		t.Fatalf("invoke ext status=%d body=%s", invW.Code, invW.Body.String())
	}
	if disp.lastPlugin != "com.acme.hello" || disp.lastCap != "echo" {
		t.Fatalf("dispatched (%q, %q), want (com.acme.hello, echo)", disp.lastPlugin, disp.lastCap)
	}
	if disp.lastPayload["workspace"] != "/home/dev/acme/service" || disp.lastPayload["connection_id"] != "ssh-build01" || disp.lastPayload["session_id"] != "parent-sess-ext" {
		t.Fatalf("expected caller enrichment, got: %#v", disp.lastPayload)
	}

	// 4. Backward-compatible gitlab.* routes through Extensions when legacy callbacks are nil
	if _, err := h.Invoke(ctx, "gitlab.pipelines", map[string]any{"caller": caller, "limit": 5}); err != nil {
		t.Fatalf("gitlab.pipelines via extensions failed: %v", err)
	}
	if disp.lastPlugin != "gitlab-ci-viewer" || disp.lastCap != "pipelines" || disp.lastPayload["workspace"] != "/home/dev/acme/service" {
		t.Fatalf("unexpected gitlab.pipelines fallback dispatch: plugin=%q cap=%q payload=%#v", disp.lastPlugin, disp.lastCap, disp.lastPayload)
	}
	if _, err := h.Invoke(ctx, "gitlab.pipeline", map[string]any{"caller": caller, "pipeline_id": 101}); err != nil {
		t.Fatalf("gitlab.pipeline via extensions failed: %v", err)
	}
	if disp.lastPlugin != "gitlab-ci-viewer" || disp.lastCap != "pipeline" {
		t.Fatalf("unexpected gitlab.pipeline fallback dispatch: plugin=%q cap=%q", disp.lastPlugin, disp.lastCap)
	}
	if _, err := h.Invoke(ctx, "gitlab.job_log", map[string]any{"caller": caller, "job_id": 9002}); err != nil {
		t.Fatalf("gitlab.job_log via extensions failed: %v", err)
	}
	if disp.lastPlugin != "gitlab-ci-viewer" || disp.lastCap != "job_log" {
		t.Fatalf("unexpected gitlab.job_log fallback dispatch: plugin=%q cap=%q", disp.lastPlugin, disp.lastCap)
	}

	// 5. Invalid ext capability format returns invalid_input
	if _, err := h.Invoke(ctx, "ext.onlyonepart", nil); err == nil {
		t.Fatal("expected error for malformed ext capability id")
	}
}
