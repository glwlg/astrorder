package agentapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"astrorder.dev/app-server/internal/auth"
	"astrorder.dev/app-server/internal/config"
	"astrorder.dev/app-server/internal/store"
)

func TestAgentUsageCountsHeadlessCallsWithoutPayloads(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if _, err := st.UpsertAgent(ctx, map[string]any{"id": "agent-a", "kind": "codex", "name": "Work Codex", "status": "ready"}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.UpsertSession(ctx, map[string]any{"id": "session-a", "agent_id": "agent-a", "title": "test", "status": "idle", "updated_at": time.Now().UTC().Format(time.RFC3339)}); err != nil {
		t.Fatal(err)
	}
	h, err := NewHandler(Options{Store: st, Authorizer: auth.New(config.Config{BrowserSecret: "browser-token", ConnectorSecret: "connector-token"}, nil)})
	if err != nil {
		t.Fatal(err)
	}
	caller := `"caller":{"agent_id":"agent-a","connection_id":"local","profile_name":"default","runtime":"codex","session_id":"session-a"}`
	invoke := func(token, extraHeader, body string) int {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/agent/invoke", strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+token)
		if extraHeader != "" {
			req.Header.Set("Sec-Fetch-Mode", extraHeader)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		return w.Code
	}
	for _, tt := range []struct {
		token, header, body string
		want                int
	}{
		{"connector-token", "", `{"capability":"sessions.list","input":{` + caller + `}}`, 200},
		{"connector-token", "", `{"capability":"sessions.read","input":{` + caller + `,"key":"missing::missing"}}`, 404},
		{"connector-token", "", `{"capability":"catalog.list","input":{}}`, 200},
		{"connector-token", "", `{"capability":"blackboard.get","input":{"caller":{"agent_id":"missing","connection_id":"local","profile_name":"default","runtime":"codex","session_id":"s"}}}`, 400},
		{"connector-token", "", `{"capability":"PRIVATE-CAPABILITY","input":{"secret":"PRIVATE-PAYLOAD"}}`, 404},
		{"browser-token", "cors", `{"capability":"catalog.list","input":{}}`, 200},
		{"invalid-token", "", `{"capability":"catalog.list","input":{}}`, 401},
	} {
		if got := invoke(tt.token, tt.header, tt.body); got != tt.want {
			t.Fatalf("status %d want %d for %s", got, tt.want, tt.body)
		}
	}
	get := func(token, period string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/agent/usage?range="+period, nil)
		req.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		return w
	}
	if got := get("connector-token", "7d").Code; got != 401 {
		t.Fatalf("connector accessed browser statistics: %d", got)
	}
	if got := get("browser-token", "invalid").Code; got != 400 {
		t.Fatalf("invalid range status: %d", got)
	}
	w := get("browser-token", "7d")
	if w.Code != 200 {
		t.Fatalf("usage status: %d %s", w.Code, w.Body.String())
	}
	var report store.AgentUsageReport
	if err := json.Unmarshal(w.Body.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if report.Summary.Calls != 5 || report.Summary.Succeeded != 2 || report.Summary.Failed != 3 || report.Summary.ActiveAgents != 1 {
		t.Fatalf("summary: %#v", report.Summary)
	}
	foundAgent, foundUnknown := false, false
	for _, agent := range report.Agents {
		if agent.AgentID == "agent-a" && agent.Calls == 2 && agent.Failed == 1 && agent.Name == "Work Codex" {
			foundAgent = true
		}
		if agent.AgentID == "" && agent.Calls == 3 {
			foundUnknown = true
		}
	}
	if !foundAgent || !foundUnknown {
		t.Fatalf("agent attribution: %#v", report.Agents)
	}
	rows, err := st.QueryRows(ctx, "SELECT * FROM agent_invocation_usage")
	if err != nil {
		t.Fatal(err)
	}
	if text := fmt.Sprint(rows); strings.Contains(text, "PRIVATE-") || strings.Contains(text, "session-a") || strings.Contains(text, "token") {
		t.Fatalf("private content recorded: %s", text)
	}
}

func TestAgentUsageRecordsAfterCallerCancellation(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	h := &Handler{options: Options{Store: st}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	h.recordAgentUsage(ctx, "catalog.list", nil, time.Now(), true)
	report, err := st.AgentUsage(context.Background(), "all", time.Now())
	if err != nil || report.Summary.Calls != 1 {
		t.Fatalf("cancelled request usage: %#v %v", report.Summary, err)
	}
}

func TestAgentUsageStorageFailureDoesNotChangeCompletedCall(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	handler, err := NewHandler(Options{
		Store: st, Authorizer: auth.New(config.Config{ConnectorSecret: "connector-token"}, nil),
		Logger: slog.New(slog.NewTextHandler(&logs, nil)),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/agent/invoke", strings.NewReader(`{"capability":"catalog.list","input":{"secret":"private-payload"}}`))
	req.Header.Set("Authorization", "Bearer connector-token")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK || !strings.Contains(logs.String(), "record agent usage failed") {
		t.Fatalf("completed call or logging changed: status=%d log=%s", w.Code, logs.String())
	}
	if strings.Contains(logs.String(), "private-payload") || strings.Contains(logs.String(), "connector-token") {
		t.Fatal("sensitive request data entered usage logs")
	}
}
