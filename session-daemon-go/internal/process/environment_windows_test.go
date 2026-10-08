//go:build windows

package process

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"golang.org/x/sys/windows/registry"
)

func TestNativeEnvironmentLoadsCurrentWindowsUserSettings(t *testing.T) {
	name := fmt.Sprintf("ASTRORDER_TEST_NATIVE_ENV_%d", os.Getpid())
	key, err := registry.OpenKey(registry.CURRENT_USER, `Environment`, registry.SET_VALUE)
	if err != nil {
		t.Fatal(err)
	}
	defer key.Close()
	if err = key.SetStringValue(name, "from-registry"); err != nil {
		t.Fatal(err)
	}
	defer key.DeleteValue(name)
	value := func(env []string) string {
		for _, entry := range env {
			k, v, _ := strings.Cut(entry, "=")
			if strings.EqualFold(k, name) {
				return v
			}
		}
		return ""
	}
	if got := value(NativeEnvironment()); got != "from-registry" {
		t.Fatalf("current user environment was not loaded: %q", got)
	}
	t.Setenv(name, "process-override")
	if got := value(NativeEnvironment()); got != "process-override" {
		t.Fatal("explicit process value lost precedence")
	}
	if got := value(NativeEnvironment([]string{name + "=runtime-override"})); got != "runtime-override" {
		t.Fatal("explicit runtime value lost precedence")
	}
}
