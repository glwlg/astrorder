package httpapi

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"astrorder.dev/app-server/internal/config"
	"astrorder.dev/app-server/internal/store"
)

// Opt-in real HTTP/SQLite boundary for the browser acceptance runner.
func TestDraftShelfBrowserHarness(t *testing.T) {
	root := os.Getenv("ASTRORDER_DRAFT_BROWSER_FIXTURE")
	if root == "" {
		t.Skip("browser acceptance only")
	}
	st, err := store.Open(filepath.Join(root, "browser.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	server := httptest.NewServer(NewHandler(Options{Config: config.Config{BrowserSecret: "draft-browser-fixture"}, Store: st}))
	defer server.Close()
	if err := os.WriteFile(filepath.Join(root, "ready.json"), []byte(`{"url":"`+server.URL+`"}`), 0600); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Minute)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(filepath.Join(root, "stop")); err == nil {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("browser harness timed out")
}
