package antigravity

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestIsolatedQuotaUsesSelectedSubscription(t *testing.T) {
	if _, err := exec.LookPath("bwrap"); err != nil {
		t.Skip("bubblewrap is not installed")
	}
	// The sandbox mounts an empty /tmp, so place the fixture binary next to this package.
	dir, err := os.MkdirTemp(".", ".agy-cli-test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	executable, err := filepath.Abs(filepath.Join(dir, "agy"))
	if err != nil {
		t.Fatal(err)
	}
	script := `#!/bin/sh
set -eu
if [ "$1" = '-p' ]; then
  printf '%s\n' '` + realUsageJSONSample + `'
  exit 0
fi
[ "$1" = acp ]
IFS= read -r init
printf '%s\n' '{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":1}}'
IFS= read -r session
case "$session" in *'session/new'*) ;; *) exit 1 ;; esac
credential=$(cat "$HOME/.gemini/antigravity-cli/antigravity-oauth-token")
case "$credential" in *example-free*) plan=Free ;; *example-ultra*) plan=Ultra ;; *) exit 1 ;; esac
printf '{"jsonrpc":"2.0","id":2,"result":{"_meta":{"antigravity":{"tier":{"name":"%s"}}}}}\n' "$plan"
cat >/dev/null
`
	if err := os.WriteFile(executable, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ credential, plan string }{{"example-free", "Free"}, {"example-ultra", "Ultra"}, {"unavailable", ""}} {
		quota, err := readIsolatedAgyQuota(context.Background(), t.TempDir(), executable, []byte(`{"token":{"access_token":"`+tc.credential+`"}}`))
		if err != nil {
			t.Fatal(err)
		}
		plan, _ := quota["plan"].(string)
		if plan != tc.plan || quota["groups"] == nil {
			t.Fatalf("plan %q, expected %q with quota groups", plan, tc.plan)
		}
	}
}
