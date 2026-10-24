package files

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"astrorder.dev/app-server/internal/targetfs"
)

func init() {
	if os.Getenv("ASTRORDER_TEST_BROWSE_HELPER") != "1" {
		return
	}
	_ = targetfs.ServeHelper(context.Background(), os.Stdin, os.Stdout)
	os.Exit(0)
}

func TestRemoteProjectBrowserCanReturnToParent(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	child := filepath.Join(root, "child")
	if err := os.Mkdir(child, 0700); err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("ASTRORDER_TEST_BROWSE_HELPER", "1")
	resolver := targetfs.NewResolver(targetfs.ResolverOptions{Store: fakeStore{connection: map[string]any{"settings": map[string]any{"host": "example.test"}}}, SSHBinary: executable, HelperPath: "fixture"})
	handler := NewHandler(Options{Authorizer: allowBrowser{}, Resolver: resolver})
	request := httptest.NewRequest(http.MethodGet, "/api/v1/files/tree?depth=1&connection_id=remote&path="+queryEscape(child), nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != 200 {
		t.Fatalf("tree=%d %s", response.Code, response.Body.String())
	}
	var result targetfs.ListResult
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Parent == nil || *result.Parent != root {
		t.Fatalf("parent=%v, want %s", result.Parent, root)
	}
}
