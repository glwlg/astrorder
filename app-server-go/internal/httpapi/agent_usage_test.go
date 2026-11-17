package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"astrorder.dev/app-server/internal/agentcli"
	"astrorder.dev/app-server/internal/config"
	"astrorder.dev/app-server/internal/store"
)

func TestCLIInvocationAppearsInBrowserUsageEndpoint(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	if _, err := st.UpsertAgent(ctx, map[string]any{"id": "agent-a", "kind": "codex", "name": "Test Codex", "status": "ready"}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.UpsertSession(ctx, map[string]any{"id": "session-a", "agent_id": "agent-a", "title": "Test", "status": "idle", "updated_at": time.Now().UTC().Format(time.RFC3339)}); err != nil {
		t.Fatal(err)
	}
	handler, err := NewHandlerChecked(Options{Store: st, Config: config.Config{BrowserSecret: "browser-secret", ConnectorSecret: "agent-secret", DefaultWorkspace: t.TempDir()}})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()
	env := map[string]string{
		"ASTRORDER_AGENT_URL": server.URL, "ASTRORDER_AGENT_TOKEN": "agent-secret",
		"ASTRORDER_CALLER_AGENT_ID": "agent-a", "ASTRORDER_CALLER_CONNECTION_ID": "local",
		"ASTRORDER_CALLER_PROFILE_NAME": "default", "ASTRORDER_CALLER_RUNTIME": "codex", "ASTRORDER_CALLER_SESSION_ID": "session-a",
	}
	lookup := func(key string) (string, bool) { value, ok := env[key]; return value, ok }
	var output, errors bytes.Buffer
	if code := agentcli.RunWithConfig(ctx, []string{"sessions", "list"}, strings.NewReader(""), &output, &errors, lookup, agentcli.Config{HTTPClient: server.Client()}); code != 0 {
		t.Fatalf("CLI failed: %s", errors.String())
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+"/api/v1/agent/usage?range=7d", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer browser-secret")
	resp, err := server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("usage endpoint: %d", resp.StatusCode)
	}
	var report store.AgentUsageReport
	if err := json.NewDecoder(resp.Body).Decode(&report); err != nil {
		t.Fatal(err)
	}
	if report.Summary.Calls != 1 || len(report.Agents) != 1 || report.Agents[0].Name != "Test Codex" || len(report.Capabilities) != 1 || report.Capabilities[0].Capability != "sessions.list" {
		t.Fatalf("CLI usage not attributed: %#v", report)
	}
}
