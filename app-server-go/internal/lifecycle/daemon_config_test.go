package lifecycle

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestEnsureDaemonConfigPreservesExistingConfig(t *testing.T) {
	state := t.TempDir()
	path := filepath.Join(state, "session-daemon.json")
	original := []byte("{\"runtimes\":[],\"model_config\":{\"home\":\"existing-user-home\"},\"journal\":{\"total_bytes\":123}}\n")
	if err := os.WriteFile(path, original, 0600); err != nil {
		t.Fatal(err)
	}
	actual, err := EnsureDaemonConfig(state, t.TempDir())
	if err != nil || actual != path {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(data, original) {
		t.Fatal("provision replaced existing daemon configuration", err)
	}
}
