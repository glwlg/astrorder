package capabilities

import (
	"strings"
	"testing"
)

func TestYAMLEditPreservesSyntaxAndTypes(t *testing.T) {
	data := []byte(`# user settings
model: "on" # keep quoted value
shared: &shared
  timeout: 60
other: *shared
prompt: |
  hello
  world
mcp_servers:
  demo:
    # service address
    url: 'https://old.example/mcp' # keep address comment
    enabled: false
    unknown: {nested: [one, two]}
plugins: null
`)
	in := mustInput(t, map[string]any{"name": "demo", "url": "https://new.example/mcp", "args": []any{}, "enabled": true})
	out, err := applyMCPYAML(data, in)
	if err != nil {
		t.Fatal(err)
	}
	for _, keep := range []string{"# user settings", `model: "on"`, "# keep quoted value", "&shared", "*shared", "prompt: |", "# service address", "# keep address comment", "unknown: {nested: [one, two]}"} {
		if !strings.Contains(string(out), keep) {
			t.Errorf("lost %q:\n%s", keep, out)
		}
	}
	cfg, err := parseYAMLMap(out)
	if err != nil {
		t.Fatal(err)
	}
	if cfg["model"] != "on" || asMap(cfg["other"])["timeout"] == nil || cfg["prompt"] != "hello\nworld\n" {
		t.Fatalf("untouched values changed: %#v", cfg)
	}
	entry := asMap(asMap(cfg["mcp_servers"])["demo"])
	if entry["url"] != in.URL || entry["enabled"] != nil {
		t.Fatalf("MCP edit not applied: %#v", entry)
	}
	out, err = setHermesPlugin(out, "demo", true, false)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err = parseYAMLMap(out)
	if err != nil || !contains(asStrings(asMap(cfg["plugins"])["enabled"]), "demo") {
		t.Fatalf("null plugin mapping not replaced: %s, %v", out, err)
	}
}

func TestYAMLEditFlowMappingAndQuotedServerName(t *testing.T) {
	in := mustInput(t, map[string]any{"name": "demo.server", "transport": "stdio", "command": "node", "args": []any{"on", "001", "a: b", "a#b"}})
	for _, input := range []string{"", "{}\n", "mcp_servers: {}\n", "{mcp_servers: {'demo.server': {url: 'https://old.example'}}}\n"} {
		t.Run(input, func(t *testing.T) {
			out, err := applyMCPYAML([]byte(input), in)
			if err != nil {
				t.Fatal(err)
			}
			if err := verifyMCP("hermes", in)(out); err != nil {
				t.Fatalf("%v:\n%s", err, out)
			}
			out, err = removeMCPYAML(out, in.Name)
			if err != nil {
				t.Fatal(err)
			}
			if err := verifyMCPRemoved("hermes", in.Name)(out); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestYAMLEditRejectsAmbiguousDocuments(t *testing.T) {
	for _, data := range []string{"- a\n- b\n", "key: one\nkey: two\n", "a: b\n---\nc: d\n"} {
		if _, _, err := yamlParse([]byte(data)); err == nil {
			t.Errorf("accepted invalid config %q", data)
		}
	}
}
