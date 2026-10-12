package capabilities

import (
	"strings"
	"testing"
)

const codexSample = `model = "gpt-5"

# keep this comment
[mcp_servers.astrorder]
url = "http://127.0.0.1:30001/api/v1/agent/mcp"
default_tools_approval_mode = "approve"

[mcp_servers.astrorder.http_headers]
Authorization = "Bearer top-secret"
X-Astrorder-Agent-Id = "local-codex"

[mcp_servers.node_repl]
command = "node"
args = ["a.js"]

[mcp_servers.node_repl.env]
NODE_PATH = "/x"
API_TOKEN = "t0ken"

[plugins."github@openai-curated"]
enabled = true

[hooks.state."/a/b"]
trusted_hash = "sha256:abc"
`

func mustInput(t *testing.T, raw map[string]any) mcpInput {
	t.Helper()
	in, err := parseMCPInput(raw)
	if err != nil {
		t.Fatal(err)
	}
	return in
}

func TestCodexSaveKeepsEverythingElseAndRedactedSecrets(t *testing.T) {
	in := mustInput(t, map[string]any{
		"name": "astrorder", "url": "http://127.0.0.1:30002/mcp",
		"headers": map[string]any{"Authorization": redactedValue, "X-Astrorder-Agent-Id": "renamed"},
	})
	out, err := applyMCPTOML([]byte(codexSample), "codex", in)
	if err != nil {
		t.Fatal(err)
	}
	text := string(out)
	for _, keep := range []string{"# keep this comment", `model = "gpt-5"`, `default_tools_approval_mode = "approve"`,
		"[hooks.state.\"/a/b\"]", `trusted_hash = "sha256:abc"`, `command = "node"`, `API_TOKEN = "t0ken"`, `[plugins."github@openai-curated"]`} {
		if !strings.Contains(text, keep) {
			t.Errorf("lost %q:\n%s", keep, text)
		}
	}
	cfg, err := parseTOML(out)
	if err != nil {
		t.Fatalf("output is not valid TOML: %v\n%s", err, text)
	}
	entry := asMap(asMap(cfg["mcp_servers"])["astrorder"])
	if asString(entry["url"]) != "http://127.0.0.1:30002/mcp" {
		t.Errorf("url not updated: %v", entry["url"])
	}
	headers := asStringMap(entry["http_headers"])
	if headers["Authorization"] != "Bearer top-secret" || headers["X-Astrorder-Agent-Id"] != "renamed" {
		t.Errorf("headers = %v", headers)
	}
	if strings.Contains(text, redactedValue) {
		t.Errorf("placeholder leaked into the file:\n%s", text)
	}
}

func TestCodexSaveNewServerAndDelete(t *testing.T) {
	in := mustInput(t, map[string]any{"name": "fresh", "transport": "stdio", "command": "npx", "args": []any{"-y", "pkg"}, "env": map[string]any{"A": "1"}})
	out, err := applyMCPTOML([]byte(codexSample), "codex", in)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := parseTOML(out)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	entry := asMap(asMap(cfg["mcp_servers"])["fresh"])
	if asString(entry["command"]) != "npx" || !sameStrings(asStrings(entry["args"]), []string{"-y", "pkg"}) || asStringMap(entry["env"])["A"] != "1" {
		t.Errorf("entry = %v", entry)
	}
	gone, err := removeMCPTOML(out, "codex", "fresh")
	if err != nil {
		t.Fatal(err)
	}
	if string(gone) != codexSample {
		t.Errorf("save + delete is not a no-op:\n--- want\n%s\n--- got\n%s", codexSample, gone)
	}
}

func TestCodexSaveDisabledWritesEnabledFalse(t *testing.T) {
	in := mustInput(t, map[string]any{"name": "node_repl", "transport": "stdio", "command": "node", "args": []any{"a.js"}, "enabled": false})
	out, err := applyMCPTOML([]byte(codexSample), "codex", in)
	if err != nil {
		t.Fatal(err)
	}
	cfg, _ := parseTOML(out)
	if asBool(asMap(asMap(cfg["mcp_servers"])["node_repl"])["enabled"], true) {
		t.Errorf("enabled should be false:\n%s", out)
	}
	if asStringMap(asMap(asMap(cfg["mcp_servers"])["node_repl"])["env"])["API_TOKEN"] != "t0ken" {
		t.Errorf("env was touched although the request had none:\n%s", out)
	}
}

func TestCodexInlineTableIsRefusedNotMangled(t *testing.T) {
	src := "[mcp_servers]\nfoo = { url = \"http://x\" }\n"
	in := mustInput(t, map[string]any{"name": "foo", "url": "http://y"})
	if _, err := applyMCPTOML([]byte(src), "codex", in); err == nil {
		t.Fatal("expected an error for inline-table definitions")
	}
}

func TestInvalidTOMLIsRefused(t *testing.T) {
	in := mustInput(t, map[string]any{"name": "x", "url": "http://y"})
	if _, err := applyMCPTOML([]byte("this is = = not toml"), "codex", in); err == nil {
		t.Fatal("expected a parse error")
	}
}

const grokSample = "\ufeff" + `[cli]
theme = "dark"

[mcp_servers.astrorder]
url = "http://127.0.0.1:30001/api/v1/agent/mcp"

[mcp_servers.astrorder.headers]
Authorization = "Bearer g"

[plugins]
enabled = ["tinyfish", "chrome-devtools-mcp"]
`

func TestGrokDisableUsesDisabledList(t *testing.T) {
	in := mustInput(t, map[string]any{"name": "astrorder", "url": "http://127.0.0.1:30001/api/v1/agent/mcp", "enabled": false})
	out, err := applyMCPTOML([]byte(grokSample), "grok", in)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := parseTOML(out)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !contains(asStrings(cfg["disabled_mcp_servers"]), "astrorder") {
		t.Errorf("disabled list = %v\n%s", cfg["disabled_mcp_servers"], out)
	}
	if asBool(asMap(asMap(cfg["mcp_servers"])["astrorder"])["enabled"], true) {
		t.Errorf("entry should say enabled=false")
	}
	if asStringMap(asMap(asMap(cfg["mcp_servers"])["astrorder"])["headers"])["Authorization"] != "Bearer g" {
		t.Errorf("headers lost")
	}
	if !strings.HasPrefix(string(out), "\ufeff") {
		t.Errorf("BOM dropped")
	}
	on := mustInput(t, map[string]any{"name": "astrorder", "url": "http://127.0.0.1:30001/api/v1/agent/mcp", "enabled": true})
	back, err := applyMCPTOML(out, "grok", on)
	if err != nil {
		t.Fatal(err)
	}
	cfg2, _ := parseTOML(back)
	if contains(asStrings(cfg2["disabled_mcp_servers"]), "astrorder") {
		t.Errorf("still disabled:\n%s", back)
	}
}

func TestGrokDeleteClearsDisabledEntry(t *testing.T) {
	in := mustInput(t, map[string]any{"name": "astrorder", "url": "http://127.0.0.1:1/", "enabled": false})
	disabled, _ := applyMCPTOML([]byte(grokSample), "grok", in)
	out, err := removeMCPTOML(disabled, "grok", "astrorder")
	if err != nil {
		t.Fatal(err)
	}
	cfg, _ := parseTOML(out)
	if asMap(cfg["mcp_servers"])["astrorder"] != nil || contains(asStrings(cfg["disabled_mcp_servers"]), "astrorder") {
		t.Errorf("not fully removed:\n%s", out)
	}
	if !strings.Contains(string(out), `enabled = ["tinyfish", "chrome-devtools-mcp"]`) {
		t.Errorf("plugins section changed:\n%s", out)
	}
}

func TestCodexPluginToggleAndRemove(t *testing.T) {
	off, err := setCodexPluginEnabled([]byte(codexSample), "github@openai-curated", false)
	if err != nil {
		t.Fatal(err)
	}
	cfg, _ := parseTOML(off)
	if asBool(asMap(asMap(cfg["plugins"])["github@openai-curated"])["enabled"], true) {
		t.Errorf("not disabled:\n%s", off)
	}
	gone, err := removeCodexPlugin(off, "github@openai-curated")
	if err != nil {
		t.Fatal(err)
	}
	cfg, _ = parseTOML(gone)
	if asMap(cfg["plugins"])["github@openai-curated"] != nil {
		t.Errorf("not removed:\n%s", gone)
	}
	if !strings.Contains(string(gone), `trusted_hash = "sha256:abc"`) {
		t.Errorf("hook trust lost")
	}
}

const hermesSample = `# hermes config
model: x
plugins:
  enabled:
    - astrorder-hermes
    - rtk-rewrite
  disabled:
    - home-dashboard
  entries:
    astrorder-hermes:
      allow_tool_override: false
mcp_servers:
  astrorder:
    url: http://127.0.0.1:30001/api/v1/agent/mcp
    headers:
      Authorization: Bearer h
  stitch:
    url: https://stitch.googleapis.com/mcp
    connect_timeout: 60
`

func TestHermesMCPSaveKeepsUnknownKeysAndSecrets(t *testing.T) {
	in := mustInput(t, map[string]any{"name": "stitch", "url": "https://stitch.googleapis.com/mcp2", "headers": map[string]any{"X-Key": "k"}})
	out, err := applyMCPYAML([]byte(hermesSample), in)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "# hermes config") {
		t.Errorf("comment lost:\n%s", out)
	}
	cfg, err := parseYAMLMap(out)
	if err != nil {
		t.Fatal(err)
	}
	stitch := asMap(asMap(cfg["mcp_servers"])["stitch"])
	if asString(stitch["url"]) != "https://stitch.googleapis.com/mcp2" || asString(stitch["connect_timeout"]) != "60" {
		t.Errorf("stitch = %v", stitch)
	}
	if asStringMap(asMap(asMap(cfg["mcp_servers"])["astrorder"])["headers"])["Authorization"] != "Bearer h" {
		t.Errorf("neighbour server changed")
	}
	removed, err := removeMCPYAML(out, "stitch")
	if err != nil {
		t.Fatal(err)
	}
	cfg, _ = parseYAMLMap(removed)
	if asMap(cfg["mcp_servers"])["stitch"] != nil || asMap(cfg["mcp_servers"])["astrorder"] == nil {
		t.Errorf("wrong server removed:\n%s", removed)
	}
}

func TestHermesPluginMoves(t *testing.T) {
	out, err := setHermesPlugin([]byte(hermesSample), "rtk-rewrite", false, false)
	if err != nil {
		t.Fatal(err)
	}
	cfg, _ := parseYAMLMap(out)
	plugins := asMap(cfg["plugins"])
	if contains(asStrings(plugins["enabled"]), "rtk-rewrite") || !contains(asStrings(plugins["disabled"]), "rtk-rewrite") {
		t.Errorf("plugins = %v", plugins)
	}
	out, _ = setHermesPlugin(out, "home-dashboard", true, false)
	cfg, _ = parseYAMLMap(out)
	plugins = asMap(cfg["plugins"])
	if !contains(asStrings(plugins["enabled"]), "home-dashboard") || contains(asStrings(plugins["disabled"]), "home-dashboard") {
		t.Errorf("plugins = %v", plugins)
	}
	out, _ = setHermesPlugin(out, "astrorder-hermes", false, true)
	cfg, _ = parseYAMLMap(out)
	plugins = asMap(cfg["plugins"])
	if contains(asStrings(plugins["enabled"]), "astrorder-hermes") || asMap(plugins["entries"])["astrorder-hermes"] != nil {
		t.Errorf("not removed: %v", plugins)
	}
}

func TestHermesEmptyConfigGetsPlugins(t *testing.T) {
	out, err := setHermesPlugin(nil, "x", true, false)
	if err != nil {
		t.Fatal(err)
	}
	cfg, _ := parseYAMLMap(out)
	if !contains(asStrings(asMap(cfg["plugins"])["enabled"]), "x") {
		t.Errorf("%s", out)
	}
}

func TestParseMCPInputRejectsBadInput(t *testing.T) {
	bad := []map[string]any{
		{"name": "has space", "url": "http://x"},
		{"name": "", "url": "http://x"},
		{"name": "a", "url": "ftp://x"},
		{"name": "a", "transport": "stdio"},
		{"name": "a", "url": "http://x", "env": map[string]any{"A=B": "1"}},
		{"name": "a", "url": "http://x", "env": map[string]any{"A": "line\nbreak"}},
		{"name": "a", "url": "http://x", "env": "nope"},
		{"name": "a", "transport": "stdio", "command": "x", "args": []any{"a\nb"}},
	}
	for i, raw := range bad {
		if _, err := parseMCPInput(raw); err == nil {
			t.Errorf("case %d accepted: %v", i, raw)
		}
	}
}

func TestRedactHidesSecretsByKeyName(t *testing.T) {
	got := redactMap(map[string]string{"Authorization": "x", "X-Api-Key": "y", "PATH": "/bin", "NODE_PATH": "/n"})
	if got["Authorization"] != redactedValue || got["X-Api-Key"] != redactedValue || got["PATH"] != "/bin" || got["NODE_PATH"] != "/n" {
		t.Errorf("%v", got)
	}
}

func TestFrontMatter(t *testing.T) {
	name, desc := parseFrontMatter("---\nname: \"my-skill\"\ndescription: >\n  Does things\n---\n", "folder")
	if name != "my-skill" || desc != "Does things" {
		t.Errorf("%q %q", name, desc)
	}
	name, _ = parseFrontMatter("no front matter", "folder")
	if name != "folder" {
		t.Errorf("%q", name)
	}
}
