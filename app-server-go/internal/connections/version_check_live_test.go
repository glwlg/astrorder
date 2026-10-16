package connections

import (
	"context"
	"os"
	"testing"
)

func TestLiveUpgradeVersionReadOnly(t *testing.T) {
	if os.Getenv("ASTRORDER_TEST_UPGRADE_VERSIONS") != "1" {
		t.Skip("opt-in read-only installed CLI and release checks")
	}
	executor := &NativeVersionExecutor{}
	for _, kind := range []string{"codex", "grok"} {
		check := executor.CheckUpgrade(context.Background(), kind, "local", nil)
		t.Logf("%s: current=%s latest=%s has_update=%t error=%s", kind, check.Current, check.Latest, check.HasUpdate, check.Error)
		if check.Error != "" || check.Current == "" || check.Latest == "" || check.Latest == "latest" {
			t.Fatalf("version check failed: %+v", check)
		}
	}
}
