package search

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"astrorder.dev/app-server/internal/auth"
	"astrorder.dev/app-server/internal/store"
)

type allowBrowser struct{}

func (allowBrowser) AuthorizeBrowser(*http.Request) *auth.Error { return nil }

func TestSearchHandler(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "s.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()

	_, _ = st.UpsertAgent(ctx, map[string]any{"id": "a1", "kind": "codex", "name": "Codex", "status": "ready", "capabilities": []string{"chat"}})
	_, _ = st.UpsertSession(ctx, map[string]any{"id": "s1", "agent_id": "a1", "title": "支付网关", "status": "idle", "updated_at": "2026-11-23T10:00:00Z"})
	if _, err := st.DB().ExecContext(ctx, `INSERT INTO messages(id, session_id, agent_id, role, kind, text, attachments, created_at) VALUES(?,?,?,?,?,?,?,?)`, "m1", "s1", "a1", "user", "message", "排查 nginx 502 超时配置", "[]", "2026-11-23T10:00:00Z"); err != nil {
		t.Fatal(err)
	}

	h := NewHandler(Options{Store: st, Authorizer: allowBrowser{}})
	if !Matches(http.MethodGet, "/api/v1/sessions/search") || Matches(http.MethodPost, "/api/v1/sessions/search") {
		t.Fatal("Matches contract failed")
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/sessions/search?q=nginx+502&limit=5", nil)
	h.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var payload struct {
		Query string                   `json:"query"`
		Items []store.MessageSearchHit `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Items) != 1 || payload.Items[0].SessionID != "s1" {
		t.Fatalf("unexpected payload: %+v", payload)
	}
}
