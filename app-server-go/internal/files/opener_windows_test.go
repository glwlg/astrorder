//go:build windows

package files

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// The child substitutes only Explorer's executable. NativeOpener still creates
// and starts the real OS process, so request cancellation can kill it.
func init() {
	if marker := os.Getenv("ASTRORDER_TEST_EXPLORER_MARKER"); marker != "" {
		time.Sleep(150 * time.Millisecond)
		if err := os.WriteFile(marker, []byte("opened"), 0o600); err != nil {
			os.Exit(2)
		}
		os.Exit(0)
	}
}

func TestNativeOpenerSurvivesRequestCompletion(t *testing.T) {
	root := t.TempDir()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(executable)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "explorer.exe"), data, 0o700); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(root, "opened.txt")
	t.Setenv("PATH", root+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("ASTRORDER_TEST_EXPLORER_MARKER", marker)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := (NativeOpener{}).Open(ctx, root, "reveal"); err != nil {
		t.Fatal(err)
	}
	cancel() // net/http cancels the request context when ServeHTTP returns.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if data, err := os.ReadFile(marker); err == nil && string(data) == "opened" {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("Explorer was terminated when the HTTP request completed")
}

func TestNativeOpenerRejectsAlreadyCancelledRequest(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := (NativeOpener{}).Open(ctx, t.TempDir(), "reveal"); err != context.Canceled {
		t.Fatalf("cancelled request returned %v", err)
	}
}
