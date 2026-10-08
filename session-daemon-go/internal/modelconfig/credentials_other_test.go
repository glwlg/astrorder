//go:build !windows

package modelconfig

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestUnixCredentialFileAndFutureNativeEnvironment(t *testing.T) {
	t.Setenv(EnvTokenKey, "fixture-previous-token")
	root := t.TempDir()
	s, err := New(Config{AllowedRoots: []string{root}, Credentials: NativeCredentialStore()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Execute(context.Background(), "model_config.apply", map[string]any{"files": map[string]any{}, "api_key": "fixture-next-token"}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(root, RelEnvConfig))
	if err != nil || string(data) != "OPENCODEX_API_AUTH_TOKEN=\"fixture-next-token\"\n" || os.Getenv(EnvTokenKey) != "fixture-next-token" {
		t.Fatal("credential file/environment were not both published", err)
	}
}
