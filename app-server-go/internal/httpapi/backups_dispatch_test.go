package httpapi

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"astrorder.dev/app-server/internal/store"
)

func TestBackupsRoutesDispatchWithBrowserAuth(t *testing.T) {
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
	handler := NewHandler(Options{
		Config: cfg,
		Store:  st,
	})

	unauthReq := httptest.NewRequest(http.MethodGet, "/api/v1/backups/targets", nil)
	unauthRec := httptest.NewRecorder()
	handler.ServeHTTP(unauthRec, unauthReq)
	if unauthRec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 without browser auth, got %d %s", unauthRec.Code, unauthRec.Body.String())
	}

	authReq := httptest.NewRequest(http.MethodGet, "/api/v1/backups/targets", nil)
	authReq.Header.Set("Authorization", "Bearer browser-secret")
	authRec := httptest.NewRecorder()
	handler.ServeHTTP(authRec, authReq)
	if authRec.Code != http.StatusOK {
		t.Fatalf("expected 200 with browser auth, got %d %s", authRec.Code, authRec.Body.String())
	}
}
