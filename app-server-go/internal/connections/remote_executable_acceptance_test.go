package connections

import (
	"testing"

	pb "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
)

// Target construction must preserve native executable identities supplied by
// the remote connection resolver before crossing the formal gRPC contract.
func TestAcceptanceRemoteNativeTargetPreservesExecutablePaths(t *testing.T) {
	connID := "private-target-fixture"
	settings := map[string]any{
		"host": "fixture.invalid", "port": 2222,
		"workspace": "/srv/fixture", "user": "fixture",
		"codex_executable":  "/opt/native/codex",
		"grok_executable":   "/opt/native/grok",
		"hermes_executable": "/opt/native/hermes",
	}
	for _, row := range []struct {
		kind       string
		runtime    pb.RuntimeKind
		executable string
	}{
		{"codex", pb.RuntimeKind_RUNTIME_KIND_CODEX_SSH, "/opt/native/codex"},
		{"grok", pb.RuntimeKind_RUNTIME_KIND_GROK_SSH, "/opt/native/grok"},
		{"hermes", pb.RuntimeKind_RUNTIME_KIND_SSH_HERMES, "/opt/native/hermes"},
	} {
		t.Run(row.kind, func(t *testing.T) {
			target := runtimeTarget(row.kind, &connID, map[string]any{"id": connID, "settings": settings})
			if target.Runtime != row.runtime || target.GetConnectionId() != connID || target.Ssh == nil {
				t.Fatalf("runtime target identity changed: %v", target)
			}
			var got string
			switch row.kind {
			case "codex":
				got = target.Ssh.GetCodexExecutable()
			case "grok":
				got = target.Ssh.GetGrokExecutable()
			case "hermes":
				got = target.Ssh.GetHermesExecutable()
			}
			if got != row.executable {
				t.Fatalf("remote %s native executable omitted or changed: got=%q want=%q", row.kind, got, row.executable)
			}
		})
	}
}
