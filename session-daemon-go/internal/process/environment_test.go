package process

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNativeEnvironmentDoesNotExposeDaemonIPCSecret(t *testing.T) {
	t.Setenv("ASTRORDER_SESSION_DAEMON_SECRET", "test-only-ipc-secret")
	t.Setenv("ASTRORDER_SESSION_DAEMON_CONNECTOR_SECRET", "test-connector-secret")
	t.Setenv("ASTRORDER_SESSION_DAEMON_GRPC_CREDENTIAL", "test-only-grpc-credential")
	t.Setenv("ASTRORDER_BROWSER_SECRET", "test-browser-secret")
	t.Setenv("NATIVE_ENV_TEST_KEEP", "preserved")
	env := NativeEnvironment([]string{"ASTRORDER_SESSION_DAEMON_SECRET=override-test-secret", "NATIVE_ENV_TEST_EXTRA=extra"})
	keep, extra := false, false
	for _, entry := range env {
		parts := strings.SplitN(entry, "=", 2)
		if len(parts) != 2 {
			continue
		}
		if strings.EqualFold(parts[0], "ASTRORDER_SESSION_DAEMON_SECRET") || strings.EqualFold(parts[0], "ASTRORDER_SESSION_DAEMON_CONNECTOR_SECRET") || strings.EqualFold(parts[0], "ASTRORDER_BROWSER_SECRET") {
			t.Fatal("daemon credential inherited by native child")
		}
		if strings.EqualFold(parts[0], "ASTRORDER_SESSION_DAEMON_GRPC_CREDENTIAL") {
			t.Fatal("gRPC daemon credential inherited by native child")
		}
		if entry == "NATIVE_ENV_TEST_KEEP=preserved" {
			keep = true
		}
		if entry == "NATIVE_ENV_TEST_EXTRA=extra" {
			extra = true
		}
	}
	if !keep || !extra {
		t.Fatal("native environment lost required non-IPC settings")
	}
}

func TestMergePathAndRefreshProcessPath(t *testing.T) {
	sep := string(os.PathListSeparator)
	dirA := filepath.Join(t.TempDir(), "managed-node")
	dirB := filepath.Join(t.TempDir(), "managed-uv")
	dirC := filepath.Join(t.TempDir(), "existing-bin")

	merged := MergePath(dirA+sep+dirB, dirB+sep+dirC+sep+""+sep+"bad\x00dir")
	parts := strings.Split(merged, sep)
	if len(parts) != 3 || parts[0] != dirA || parts[1] != dirB || parts[2] != dirC {
		t.Fatalf("unexpected merged path: %q (%v)", merged, parts)
	}

	t.Setenv("PATH", dirC)
	refreshed := RefreshProcessPath(dirA, dirB)
	if os.Getenv("PATH") != refreshed {
		t.Fatalf("process PATH was not updated: got %q want %q", os.Getenv("PATH"), refreshed)
	}
	if !strings.HasPrefix(refreshed, dirA+sep+dirB+sep+dirC) {
		t.Fatalf("managed dirs did not precede existing PATH: %q", refreshed)
	}

	// NativeEnvironment merges extra PATH with refreshed process PATH without losing either.
	dirExtra := filepath.Join(t.TempDir(), "override-bin")
	env := NativeEnvironment([]string{"PATH=" + dirExtra})
	var gotPath string
	for _, entry := range env {
		if k, v, ok := strings.Cut(entry, "="); ok && strings.EqualFold(k, "PATH") {
			gotPath = v
			break
		}
	}
	if !strings.HasPrefix(gotPath, dirExtra+sep+dirA+sep+dirB+sep+dirC) {
		t.Fatalf("NativeEnvironment did not merge PATH preserving priority: %q", gotPath)
	}
}
