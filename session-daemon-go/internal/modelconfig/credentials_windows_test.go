//go:build windows

package modelconfig

import (
	"context"
	"golang.org/x/sys/windows/registry"
	"os"
	"path/filepath"
	"testing"
)

func TestWindowsNativeCredentialReadbackAndRestoration(t *testing.T) {
	fixturePath := `Software\Astrorder\SessionDaemonValidation\` + filepath.Base(t.TempDir())
	name := "ASTRORDER_TEST_CREDENTIAL_FIXTURE"
	t.Setenv(name, "fixture-process-original")
	defer registry.DeleteKey(registry.CURRENT_USER, fixturePath)
	store := &windowsCredentialStore{path: fixturePath, name: name}
	restore, err := store.Set(context.Background(), "fixture-registry-new")
	if err != nil {
		t.Fatal(err)
	}
	key, err := registry.OpenKey(registry.CURRENT_USER, fixturePath, registry.QUERY_VALUE)
	if err != nil {
		t.Fatal(err)
	}
	value, _, err := key.GetStringValue(name)
	key.Close()
	if err != nil || value != "fixture-registry-new" || os.Getenv(name) != value {
		t.Fatal("native credential readback mismatch", err)
	}
	if err := restore(); err != nil {
		t.Fatal(err)
	}
	key, err = registry.OpenKey(registry.CURRENT_USER, fixturePath, registry.QUERY_VALUE)
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = key.GetStringValue(name)
	key.Close()
	if err != registry.ErrNotExist || os.Getenv(name) != "fixture-process-original" {
		t.Fatal("native credential restoration failed", err)
	}
}
