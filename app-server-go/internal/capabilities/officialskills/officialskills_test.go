package officialskills

import (
	"encoding/json"
	"strings"
	"testing"
)

var expectedNames = []string{
	"astrorder-sessions",
	"astrorder-browser",
	"astrorder-blackboard",
	"astrorder-swarm",
	"astrorder-workspace",
	"astrorder-monitor",
}

func TestOfficialSkillsCatalog(t *testing.T) {
	skills := ListOfficialSkills()
	if len(skills) != len(expectedNames) {
		t.Fatalf("expected %d official skills, got %d", len(expectedNames), len(skills))
	}

	names := map[string]bool{}
	for _, sk := range skills {
		names[sk.Name] = true
		if sk.Category != "astrorder" {
			t.Errorf("skill %s has category %q, expected astrorder", sk.Name, sk.Category)
		}
		if sk.Source != "astrorder" {
			t.Errorf("skill %s has source %q, expected astrorder", sk.Name, sk.Source)
		}
		if sk.Scope != "builtin" {
			t.Errorf("skill %s has scope %q, expected builtin", sk.Name, sk.Scope)
		}
		if sk.Agent != "astrorder" {
			t.Errorf("skill %s has agent %q, expected astrorder", sk.Name, sk.Agent)
		}
		if len(sk.Agents) != 0 {
			t.Errorf("skill %s has non-empty agents: %v", sk.Name, sk.Agents)
		}
		if !sk.RequiresCLI || !sk.RequiresCallerBinding {
			t.Errorf("skill %s must require the destination CLI and caller binding", sk.Name)
		}
		if !strings.Contains(sk.Content, "name: "+sk.Name) {
			t.Errorf("skill %s content does not contain its frontmatter name", sk.Name)
		}
		// Every module is self-contained: it points at the sibling CLI and never
		// depends on a registered MCP server.
		if !strings.Contains(sk.Content, "scripts/astrorder-agent") {
			t.Errorf("skill %s does not reference the sibling CLI", sk.Name)
		}
		for _, forbidden := range []string{"mcp__astrorder", "MCP server", "browser_run"} {
			if strings.Contains(sk.Content, forbidden) {
				t.Errorf("skill %s still depends on MCP marker %q", sk.Name, forbidden)
			}
		}
		// Official content must not leak a developer machine path.
		for _, forbidden := range []string{"C:\\Users\\", "/home/", "P:\\workspace"} {
			if strings.Contains(sk.Content, forbidden) {
				t.Errorf("skill %s contains a personal absolute path %q", sk.Name, forbidden)
			}
		}
		if sk.Description == "" || strings.ContainsAny(sk.Description, "\r\n") || len([]rune(sk.Description)) > 90 {
			t.Errorf("skill %s description is not a short single line: %q", sk.Name, sk.Description)
		}
		if len(sk.SupportedAgents) != 3 {
			t.Errorf("skill %s supported agents = %v", sk.Name, sk.SupportedAgents)
		}
	}

	for _, name := range expectedNames {
		if !names[name] {
			t.Errorf("missing %s", name)
		}
	}
	if names["astrorder"] {
		t.Errorf("legacy combined skill must not be a new builtin source")
	}
}

func TestOfficialSkillLookupAndRender(t *testing.T) {
	browser, ok := GetOfficialSkill("astrorder-browser")
	if !ok || browser == nil {
		t.Fatalf("failed to get astrorder-browser")
	}
	if browser.DisplayName != "浏览器控制" {
		t.Errorf("expected display name 浏览器控制, got %s", browser.DisplayName)
	}
	if _, ok := GetOfficialSkill("astrorder-missing"); ok {
		t.Error("unknown name resolved")
	}

	rendered := string(RenderOfficialSkill(*browser, `C:\Users\agent\.codex`))
	if strings.Contains(rendered, "{{CALLER_CONFIG_PATH}}") || strings.Contains(rendered, "{{CALLER_CONFIG_PATH_WIN}}") {
		t.Error("render left an unsubstituted placeholder")
	}
	if !strings.Contains(rendered, "C:/Users/agent/.codex/astrorder-caller.json") {
		t.Error("render did not substitute the caller path")
	}
}

func TestOfficialCatalogJSON(t *testing.T) {
	jsonData, err := OfficialCatalogJSON()
	if err != nil {
		t.Fatalf("failed to marshal official catalog: %v", err)
	}
	var decoded []map[string]any
	if err := json.Unmarshal(jsonData, &decoded); err != nil {
		t.Fatalf("failed to unmarshal official catalog json: %v", err)
	}
	if len(decoded) != len(expectedNames) {
		t.Fatalf("expected %d items in decoded JSON, got %d", len(expectedNames), len(decoded))
	}
	for _, item := range decoded {
		if item["requires_cli"] != true || item["requires_caller_binding"] != true {
			t.Errorf("catalog item %v does not require CLI and caller binding", item["name"])
		}
	}
}
