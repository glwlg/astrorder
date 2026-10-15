package lifecycle

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestDaemonUpdateRejectsInvalidBinaryBeforeLifecycle(t *testing.T) {
	executable := filepath.Join(t.TempDir(), "astrorder-sessiond.exe")
	if err := os.WriteFile(executable, []byte("existing daemon"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(executable+".new", []byte("not an executable"), 0600); err != nil {
		t.Fatal(err)
	}
	result, err := UpdateDaemonWithLifecycle(context.Background(), DaemonOptions{ExecutablePath: executable}, false)
	if err == nil || result.Updated {
		t.Fatalf("invalid candidate accepted: %+v err=%v", result, err)
	}
	body, err := os.ReadFile(executable)
	if err != nil || string(body) != "existing daemon" {
		t.Fatalf("invalid update changed existing executable: %v", err)
	}
}
