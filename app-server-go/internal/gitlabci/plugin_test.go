package gitlabci

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"astrorder.dev/app-server/internal/pluginhost"
	"astrorder.dev/app-server/internal/store"
	"astrorder.dev/app-server/internal/targetfs"
)

func setupGitLabPluginFixture(t *testing.T) (*Service, string, func()) {
	t.Helper()

	glServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("PRIVATE-TOKEN") != "glpat-plugin-secret" {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"message":"401 Unauthorized"}`))
			return
		}
		switch {
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.EscapedPath(), "/projects/ops%2Fopscore/pipelines"):
			_, _ = w.Write([]byte(`[
				{"id":601,"iid":91,"status":"running","ref":"main","sha":"abcdef1234567890","web_url":"http://gitlab.test/ops/opscore/-/pipelines/601","updated_at":"2026-11-27T10:00:00Z"},
				{"id":600,"iid":90,"status":"failed","ref":"main","sha":"1111111122222222","web_url":"http://gitlab.test/ops/opscore/-/pipelines/600","updated_at":"2026-11-27T09:00:00Z"}
			]`))
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.EscapedPath(), "/pipelines/601"):
			_, _ = w.Write([]byte(`{"id":601,"iid":91,"status":"running","ref":"main","sha":"abcdef1234567890","web_url":"http://gitlab.test/ops/opscore/-/pipelines/601"}`))
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.EscapedPath(), "/pipelines/601/jobs"):
			_, _ = w.Write([]byte(`[{"id":7001,"name":"unit-test","stage":"test","status":"failed"}]`))
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.EscapedPath(), "/pipelines/601/bridges"):
			w.WriteHeader(http.StatusNotFound)
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.EscapedPath(), "/jobs/7001/trace"):
			_, _ = w.Write([]byte("\x1b[31mFAIL: broken test\x1b[0m\n"))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))

	tmp := t.TempDir()
	st, err := store.Open(filepath.Join(tmp, "app.db"))
	if err != nil {
		glServer.Close()
		t.Fatal(err)
	}

	repoWS := filepath.Join(tmp, "opscore")
	if err := os.MkdirAll(filepath.Join(repoWS, ".git", "refs", "heads"), 0o755); err != nil {
		st.Close()
		glServer.Close()
		t.Fatal(err)
	}
	gitConfig := "[core]\n\trepositoryformatversion = 0\n[remote \"origin\"]\n\turl = " + glServer.URL + "/ops/opscore.git\n"
	if err := os.WriteFile(filepath.Join(repoWS, ".git", "config"), []byte(gitConfig), 0o644); err != nil {
		st.Close()
		glServer.Close()
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repoWS, ".git", "HEAD"), []byte("ref: refs/heads/main\n"), 0o644); err != nil {
		st.Close()
		glServer.Close()
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repoWS, ".git", "refs", "heads", "main"), []byte("abcdef1234567890\n"), 0o644); err != nil {
		st.Close()
		glServer.Close()
		t.Fatal(err)
	}

	resolver := targetfs.NewResolver(targetfs.ResolverOptions{
		Store:            st,
		DefaultWorkspace: repoWS,
		AllowTempDirs:    true,
		AllowedRoots:     []string{tmp},
	})
	// Intentionally leave Authorizer nil to prove ServeExtension does not re-check browser auth.
	svc := NewService(Options{
		Store:      st,
		Resolver:   resolver,
		HTTPClient: glServer.Client(),
	})

	if _, err := svc.registry.Create(context.Background(), CreateInstanceRequest{
		Name:    "Self-Hosted GitLab",
		BaseURL: glServer.URL,
		Token:   "glpat-plugin-secret",
	}); err != nil {
		st.Close()
		glServer.Close()
		t.Fatal(err)
	}

	cleanup := func() {
		st.Close()
		glServer.Close()
	}
	return svc, repoWS, cleanup
}

func TestGitLabBuiltinPluginManifestValid(t *testing.T) {
	svc := NewService(Options{})
	m := svc.Manifest()
	m.Normalize()
	if err := m.Validate(); err != nil {
		t.Fatalf("manifest validation failed: %v", err)
	}
	if m.ID != PluginID || m.Backend.Type != pluginhost.BackendTypeBuiltin || m.Settings.Mode != pluginhost.SettingsModeCustomUI {
		t.Fatalf("unexpected manifest header: %+v", m)
	}
	if len(m.Contributes.UISlots) != 3 || len(m.Contributes.SidecarPanels) != 1 || len(m.Contributes.AgentCapabilities) != 3 || len(m.Contributes.SignalProviders) != 1 {
		t.Fatalf("unexpected manifest contributes: %+v", m.Contributes)
	}
}

func TestGitLabBuiltinPluginServeExtensionStatusPillAndRoutes(t *testing.T) {
	svc, repoWS, cleanup := setupGitLabPluginFixture(t)
	defer cleanup()

	// 1. Unmatched workspace returns { visible: false }
	emptyW := httptest.NewRecorder()
	svc.ServeExtension(emptyW, httptest.NewRequest(http.MethodGet, "/status-pill?workspace=/does/not/exist", nil), "/status-pill")
	if emptyW.Code != http.StatusOK {
		t.Fatalf("unmatched status-pill status=%d body=%s", emptyW.Code, emptyW.Body.String())
	}
	var emptyPill pluginhost.DeclarativePillPayload
	if err := json.Unmarshal(emptyW.Body.Bytes(), &emptyPill); err != nil {
		t.Fatal(err)
	}
	if emptyPill.Visible {
		t.Fatalf("expected invisible pill for unmatched workspace, got %+v", emptyPill)
	}

	// 2. Matched workspace returns running status pill with panel_params.pipeline_id
	pillW := httptest.NewRecorder()
	pillReq := httptest.NewRequest(http.MethodGet, "/status-pill?workspace="+url.QueryEscape(repoWS), nil)
	svc.ServeExtension(pillW, pillReq, "/status-pill")
	if pillW.Code != http.StatusOK {
		t.Fatalf("matched status-pill status=%d body=%s", pillW.Code, pillW.Body.String())
	}
	var pill pluginhost.DeclarativePillPayload
	if err := json.Unmarshal(pillW.Body.Bytes(), &pill); err != nil {
		t.Fatal(err)
	}
	if !pill.Visible || pill.Tone != "running" || !pill.Pulsing || pill.DotColor != "#228be6" || pill.Sublabel != "#601" {
		t.Fatalf("unexpected matched pill: %+v", pill)
	}
	if pid, _ := pill.PanelParams["pipeline_id"].(float64); int64(pid) != 601 {
		t.Fatalf("expected panel_params.pipeline_id=601, got %#v", pill.PanelParams)
	}

	// 3. Delegated routes (/instances, /pipelines) work via ServeExtension without re-running Authorizer
	instW := httptest.NewRecorder()
	svc.ServeExtension(instW, httptest.NewRequest(http.MethodGet, "/instances", nil), "/instances")
	if instW.Code != http.StatusOK || !strings.Contains(instW.Body.String(), "Self-Hosted GitLab") {
		t.Fatalf("delegated /instances failed: status=%d body=%s", instW.Code, instW.Body.String())
	}
	pipeW := httptest.NewRecorder()
	svc.ServeExtension(pipeW, httptest.NewRequest(http.MethodGet, "/pipelines?workspace="+url.QueryEscape(repoWS), nil), "/pipelines")
	if pipeW.Code != http.StatusOK || !strings.Contains(pipeW.Body.String(), `"matched":true`) {
		t.Fatalf("delegated /pipelines failed: status=%d body=%s", pipeW.Code, pipeW.Body.String())
	}
}

func TestGitLabBuiltinPluginInvokeCapabilityAndCollectSignals(t *testing.T) {
	svc, repoWS, cleanup := setupGitLabPluginFixture(t)
	defer cleanup()
	ctx := context.Background()

	// 1. InvokeCapability: pipelines
	res, err := svc.InvokeCapability(ctx, "pipelines", map[string]any{"workspace": repoWS, "limit": json.Number("5")})
	if err != nil {
		t.Fatalf("InvokeCapability(pipelines): %v", err)
	}
	listResp, ok := res.(*PipelinesResponse)
	if !ok || !listResp.Matched || len(listResp.Pipelines) != 2 {
		t.Fatalf("unexpected pipelines result: %#v", res)
	}

	// 2. InvokeCapability: pipeline
	detRes, err := svc.InvokeCapability(ctx, "pipeline", map[string]any{"workspace": repoWS, "pipeline_id": "601"})
	if err != nil {
		t.Fatalf("InvokeCapability(pipeline): %v", err)
	}
	detResp, ok := detRes.(*PipelineDetailResponse)
	if !ok || detResp.Pipeline.ID != 601 || len(detResp.Jobs) != 1 {
		t.Fatalf("unexpected pipeline detail result: %#v", detRes)
	}

	// 3. InvokeCapability: job_log
	logRes, err := svc.InvokeCapability(ctx, "job_log", map[string]any{"workspace": repoWS, "job_id": 7001, "tail": 50})
	if err != nil {
		t.Fatalf("InvokeCapability(job_log): %v", err)
	}
	traceResp, ok := logRes.(*JobTraceResponse)
	if !ok || !strings.Contains(traceResp.Trace, "FAIL: broken test") || strings.Contains(traceResp.Trace, "\x1b[") {
		t.Fatalf("unexpected job_log result: %#v", logRes)
	}

	// 4. Unknown capability fails cleanly
	if _, err := svc.InvokeCapability(ctx, "nope", nil); err == nil {
		t.Fatal("expected error for unknown capability")
	}

	// 5. CollectSignals emits failed pipelines as workspace.failure_signals
	signals, err := svc.CollectSignals(ctx, SignalKindWorkspaceFailures, pluginhost.SignalQuery{
		Workspace: repoWS,
		Limit:     8,
	})
	if err != nil {
		t.Fatalf("CollectSignals: %v", err)
	}
	if len(signals) != 1 {
		t.Fatalf("expected 1 failed pipeline signal, got %+v", signals)
	}
	sig := signals[0]
	if sig.SourcePlugin != PluginID || sig.Kind != "gitlab_ci" || sig.Severity != "error" || !strings.Contains(sig.Title, "#600") {
		t.Fatalf("unexpected signal item: %+v", sig)
	}

	// Non-matching signal kind returns empty
	other, err := svc.CollectSignals(ctx, "other.kind", pluginhost.SignalQuery{Workspace: repoWS})
	if err != nil || len(other) != 0 {
		t.Fatalf("expected empty signals for unrelated kind, got %+v err=%v", other, err)
	}
}
