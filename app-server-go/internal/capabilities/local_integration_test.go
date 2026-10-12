package capabilities

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"astrorder.dev/app-server/internal/envtarget"
)

// Runs the whole write path against the real local filesystem under a temporary
// home, so the user's own agent configuration is never touched.
func TestLocalTargetWritesToTemporaryHome(t *testing.T) {
	home := t.TempDir()
	for _, key := range []string{"HOME", "USERPROFILE"} {
		t.Setenv(key, home)
	}
	t.Setenv("CODEX_HOME", filepath.Join(home, ".codex"))
	t.Setenv("HERMES_HOME", filepath.Join(home, ".hermes"))
	t.Setenv("LOCALAPPDATA", filepath.Join(home, "no-local-app-data"))

	codexConfig := filepath.Join(home, ".codex", "config.toml")
	if err := os.MkdirAll(filepath.Dir(codexConfig), 0o755); err != nil {
		t.Fatal(err)
	}
	original := "# my notes\r\nmodel = \"gpt-x\"\r\n\r\n[mcp_servers.keep]\r\nurl = \"http://127.0.0.1:9/mcp\"\r\n"
	if err := os.WriteFile(codexConfig, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}

	svc := New(&envtarget.Resolver{})
	ctx := context.Background()
	if _, err := svc.UpdateMCP(ctx, "local", "codex", "save", map[string]any{"name": "added", "url": "http://127.0.0.1:1/mcp"}); err != nil {
		t.Fatal(err)
	}
	saved, _ := os.ReadFile(codexConfig)
	if !strings.HasPrefix(string(saved), "# my notes\r\nmodel = \"gpt-x\"\r\n") || !strings.Contains(string(saved), "[mcp_servers.added]") || !strings.Contains(string(saved), "[mcp_servers.keep]") {
		t.Fatalf("config damaged:\n%q", saved)
	}
	if strings.Contains(strings.ReplaceAll(string(saved), "\r\n", ""), "\n") {
		t.Fatalf("CRLF mixed with LF:\n%q", saved)
	}
	summary, err := svc.Summary(ctx, "local", "codex")
	if err != nil || summary.Counts["mcp"] != 2 {
		t.Fatalf("%+v %v", summary, err)
	}
	if _, err := svc.UpdateMCP(ctx, "local", "codex", "delete", map[string]any{"name": "added"}); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(codexConfig)
	if string(after) != original {
		t.Fatalf("delete must restore the original file:\n%q\n%q", after, original)
	}
	backups, _ := filepath.Glob(codexConfig + ".*")
	if len(backups) == 0 {
		t.Error("no backup kept next to the config")
	}

	// Hermes: file did not exist, get created with the server.
	if _, err := svc.UpdateMCP(ctx, "local", "hermes", "save", map[string]any{"name": "h1", "command": "node", "args": []any{"srv.js"}}); err != nil {
		t.Fatal(err)
	}
	hermes, err := os.ReadFile(filepath.Join(home, ".hermes", "config.yaml"))
	if err != nil || !strings.Contains(string(hermes), "h1:") || !strings.Contains(string(hermes), "srv.js") {
		t.Fatalf("%q %v", hermes, err)
	}

	// Skill dispatch between agents on the same machine copies the directory.
	skill := filepath.Join(home, ".agents", "skills", "demo")
	if err := os.MkdirAll(filepath.Join(skill, "ref"), 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(skill, "SKILL.md"), []byte("---\nname: demo\ndescription: d\n---\nbody\n"), 0o644)
	os.WriteFile(filepath.Join(skill, "ref", "a.txt"), []byte("A"), 0o644)
	result, err := svc.Dispatch(ctx, "skill", "local", "codex", "demo", []DispatchTarget{{TargetID: "local", Agent: "grok"}})
	if err != nil {
		t.Fatal(err)
	}
	if result.SuccessCount != 1 {
		t.Fatalf("%+v", result)
	}
	copied, err := os.ReadFile(filepath.Join(home, ".grok", "skills", "demo", "ref", "a.txt"))
	if err != nil || string(copied) != "A" {
		t.Fatalf("%q %v", copied, err)
	}
}
