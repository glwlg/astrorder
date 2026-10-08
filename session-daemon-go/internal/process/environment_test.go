package process

import (
	"strings"
	"testing"
)

func TestNativeEnvironmentDoesNotExposeDaemonIPCSecret(t *testing.T) {
	t.Setenv("ASTRORDER_SESSION_DAEMON_SECRET", "test-only-ipc-secret")
	t.Setenv("ASTRORDER_SESSION_DAEMON_CONNECTOR_SECRET", "test-connector-secret")
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
