package sessions

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"astrorder.dev/app-server/internal/application"
	"astrorder.dev/app-server/internal/auth"
	"astrorder.dev/app-server/internal/config"
	"astrorder.dev/app-server/internal/store"
)

func TestBootstrapRetainsExactPendingApprovalIdentity(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	hub := application.NewEventHub(st)
	if err = hub.Publish(context.Background(), "approval.upsert", map[string]any{"id": "codex:9007199254740993", "agent_id": "a", "session_id": "native", "state": "pending", "title": "授权"}); err != nil {
		t.Fatal(err)
	}
	handler, err := NewHandler(Options{Store: st, Authorizer: auth.New(config.Config{BrowserSecret: "fixture"}, nil), Publisher: hub})
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodGet, "/api/v1/bootstrap", nil)
	r.Header.Set("Authorization", "Bearer fixture")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != 200 || !stringsContains(w.Body.String(), "codex:9007199254740993") {
		t.Fatalf("exact approval lost: %d %s", w.Code, w.Body.String())
	}
}
func stringsContains(value, part string) bool {
	for i := 0; i+len(part) <= len(value); i++ {
		if value[i:i+len(part)] == part {
			return true
		}
	}
	return false
}
