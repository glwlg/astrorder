package httpapi

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"astrorder.dev/app-server/internal/store"
)

func TestIntegrationsDispatchRouteWithBrowserAuth(t *testing.T) {
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "app.sqlite3")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	cfg := testConfig()
	cfg.DatabasePath = dbPath
	cfg.DefaultWorkspace = tmp
	h := NewHandler(Options{
		Config: cfg,
		Store:  st,
	})

	unauthReq := httptest.NewRequest(http.MethodGet, "/api/v1/integrations", nil)
	unauthRec := httptest.NewRecorder()
	h.ServeHTTP(unauthRec, unauthReq)
	if unauthRec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 without browser auth, got %d %s", unauthRec.Code, unauthRec.Body.String())
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/integrations", nil)
	req.Header.Set("Authorization", "Bearer browser-secret")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"items":[]`) {
		t.Fatalf("unexpected integrations dispatch response: %d %s", w.Code, w.Body.String())
	}
}
