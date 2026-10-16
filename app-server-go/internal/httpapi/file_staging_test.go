package httpapi

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFileResolverAllowsConfiguredLocalStaging(t *testing.T) {
	cfg := testConfig()
	cfg.StagingDir = filepath.Join(t.TempDir(), "staging")
	handler := NewHandler(Options{Config: cfg})
	response := request(t, handler, http.MethodPost, "/api/v1/files/stage", strings.NewReader(`{"paths":["/tmp/file.txt"],"connection_id":"wsl:fixture","copy_to_clipboard":false}`), map[string]string{"Authorization": "Bearer browser-secret", "Origin": "http://testserver"})
	if strings.Contains(response.Body.String(), "Local staging storage is not configured") {
		t.Fatalf("configured staging root was lost: %d %s", response.Code, response.Body.String())
	}
	if info, err := os.Stat(cfg.StagingDir); err != nil || !info.IsDir() {
		t.Fatalf("staging directory unavailable: %v", err)
	}
	// Stage creates/validates the destination before launching the remote helper.
	// Prove the allowlist was used even when this fixture distro is absent.
	marker := filepath.Join(cfg.StagingDir, "marker")
	if err := os.WriteFile(marker, []byte("stage"), 0600); err != nil {
		t.Fatal(err)
	}
}
