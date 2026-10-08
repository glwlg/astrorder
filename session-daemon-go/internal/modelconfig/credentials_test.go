package modelconfig

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

type credentialFixture struct {
	value    string
	fail     bool
	restored bool
}

func (c *credentialFixture) Set(_ context.Context, key string) (func() error, error) {
	prior := c.value
	c.value = key
	undo := func() error { c.value = prior; c.restored = true; return nil }
	if c.fail {
		return undo, errors.New("credential write rejected")
	}
	return undo, nil
}
func TestCredentialFailureRollsBackManagedFiles(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, ".codex", "config.toml")
	os.MkdirAll(filepath.Dir(path), 0700)
	original := []byte("model=\"original\"\n")
	os.WriteFile(path, original, 0600)
	store := &credentialFixture{value: "fixture-old", fail: true}
	s, err := New(Config{AllowedRoots: []string{root}, Credentials: store})
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.Execute(context.Background(), "model_config.apply", map[string]any{"files": map[string]any{FileCodexConfig: "model=\"replacement\"\n"}, "api_key": "fixture-new"})
	if err == nil {
		t.Fatal("credential failure claimed success")
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != string(original) || store.value != "fixture-old" || !store.restored {
		t.Fatal("file/credential transaction rollback failed", err)
	}
	if _, err := os.Stat(filepath.Join(root, RelEnvConfig)); !os.IsNotExist(err) {
		t.Fatal("native credential backend also wrote an environment file", err)
	}
}
