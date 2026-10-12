package modelgateway

import (
	"strings"
	"testing"
)

func TestUnifiedDiffTextMatchesUnifiedFormat(t *testing.T) {
	if got := unifiedDiffText("same", "same", "codex_config"); got != "" {
		t.Fatalf("expected empty diff for identical content, got %q", got)
	}

	got := unifiedDiffText("a\nb\nc", "a\nx\nc", "codex_config")
	want := "--- 当前/codex_config\n+++ 同步后/codex_config\n@@ -1,3 +1,3 @@\n a\n-b\n+x\n c"
	if got != want {
		t.Fatalf("unexpected diff:\n got: %q\nwant: %q", got, want)
	}

	// New file: empty before content must produce a 0,0 hunk header.
	got = unifiedDiffText("", "one\ntwo", "codex_catalog")
	want = "--- 当前/codex_catalog\n+++ 同步后/codex_catalog\n@@ -0,0 +1,2 @@\n+one\n+two"
	if got != want {
		t.Fatalf("unexpected new-file diff:\n got: %q\nwant: %q", got, want)
	}
}

func TestUnifiedDiffTextRedactsApiKeys(t *testing.T) {
	got := unifiedDiffText("api_key = \"sk-secret-123\"\nmodel = \"a\"", "api_key = \"sk-secret-456\"\nmodel = \"b\"", "codex_config")
	if strings.Contains(got, "sk-secret") {
		t.Fatalf("secret leaked in diff: %q", got)
	}
	if !strings.Contains(got, "[已配置]") {
		t.Fatalf("expected redaction marker, got %q", got)
	}
}
