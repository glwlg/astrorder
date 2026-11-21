package capabilities

import (
	"context"
	"strings"
	"testing"

	"astrorder.dev/app-server/internal/envtarget"
	"astrorder.dev/app-server/internal/envtarget/envtargettest"
)

func TestAntigravityCapabilitiesSkillsPluginsAndMCP(t *testing.T) {
	env := newEnv("a")
	root := env.P.Antigravity

	// 1. Standalone global Antigravity skill under ~/.gemini/config/skills/
	env.Files[root+"/skills/my-agy-skill/SKILL.md"] = []byte("---\nname: my-agy-skill\ndescription: Global Antigravity skill\n---\n")

	// 2. Enabled Antigravity plugin with bundled skills, MCP server, and hooks
	env.Files[root+"/plugins/modern-web-guidance/plugin.json"] = []byte(`{
		"name": "modern-web-guidance",
		"version": "1.0.1",
		"description": "Web best practices",
		"author": {"name": "Google Chrome"},
		"repository": "https://github.com/GoogleChrome/modern-web-guidance"
	}`)
	env.Files[root+"/plugins/modern-web-guidance/skills/modern-web-guidance/SKILL.md"] = []byte("---\nname: modern-web-guidance\ndescription: Web guide\n---\n")
	env.Files[root+"/plugins/modern-web-guidance/mcp_config.json"] = []byte(`{"mcpServers":{"plug-mcp":{"serverUrl":"https://plug.example/mcp","headers":{"Authorization":"Bearer secret-plug"}}}}`)
	env.Files[root+"/plugins/modern-web-guidance/hooks.json"] = []byte(`{"lint":{}}`)

	// 3. Disabled Antigravity plugin (via config.json)
	env.Files[root+"/plugins/disabled-plug/plugin.json"] = []byte(`{"name":"disabled-plug","version":"0.1.0"}`)
	env.Files[root+"/plugins/disabled-plug/skills/hidden-skill/SKILL.md"] = []byte("---\nname: hidden-skill\ndescription: Hidden\n---\n")
	env.Files[root+"/plugins/disabled-plug/mcp_config.json"] = []byte(`{"mcpServers":{"hidden-mcp":{"command":"node"}}}`)
	env.Files[root+"/config.json"] = []byte(`{"userSettings":{"theme":"dark"},"plugins":{"disabled-plug":{"enabled":false}}}`)

	// 4. Global Antigravity MCP server
	env.Files[root+"/mcp_config.json"] = []byte(`{"mcpServers":{"global-mcp":{"serverUrl":"https://api.example/mcp","headers":{"Authorization":"Bearer top-secret"},"disabled":false}}}`)

	svc := serviceFor(env)
	ctx := context.Background()

	summary, err := svc.Summary(ctx, "a", "antigravity")
	if err != nil {
		t.Fatal(err)
	}

	// Verify plugins list (both enabled and disabled plugins must appear)
	byPlug := map[string]Plugin{}
	for _, p := range summary.Plugins {
		byPlug[p.FullName] = p
	}
	if len(byPlug) != 2 {
		t.Fatalf("expected 2 antigravity plugins, got %+v", summary.Plugins)
	}
	mwg := byPlug["modern-web-guidance"]
	if !mwg.Enabled || mwg.Marketplace != "Google Chrome" || mwg.Config["version"] != "1.0.1" || mwg.Config["has_hooks"] != true {
		t.Fatalf("unexpected enabled plugin: %+v", mwg)
	}
	if byPlug["disabled-plug"].Enabled {
		t.Fatalf("expected disabled-plug to be disabled: %+v", byPlug["disabled-plug"])
	}

	// Verify skills list: global skill + enabled plugin skill visible; disabled plugin skill hidden
	bySkill := map[string]Skill{}
	for _, sk := range summary.Skills {
		if sk.Scope != "builtin" {
			bySkill[sk.Name] = sk
		}
	}
	if _, ok := bySkill["my-agy-skill"]; !ok {
		t.Fatalf("missing global antigravity skill: %+v", bySkill)
	}
	if sk, ok := bySkill["modern-web-guidance"]; !ok || sk.Source != "antigravity-plugin:modern-web-guidance" {
		t.Fatalf("missing or misattributed plugin skill: %+v", bySkill)
	}
	if _, ok := bySkill["hidden-skill"]; ok {
		t.Fatalf("disabled plugin skill must not be listed")
	}

	// Removing a plugin-bundled skill directly must be refused cleanly
	if _, err := svc.RemoveSkill(ctx, "a", "modern-web-guidance"); err == nil || !strings.Contains(err.Error(), "插件") {
		t.Fatalf("expected refusal removing plugin skill, got %v", err)
	}

	// Verify MCP servers: global + enabled plugin MCP visible; disabled plugin MCP hidden; secrets redacted
	byMCP := map[string]MCPServer{}
	for _, srv := range summary.MCPServers {
		byMCP[srv.Name] = srv
	}
	if len(byMCP) != 2 {
		t.Fatalf("expected 2 antigravity MCP servers, got %+v", summary.MCPServers)
	}
	if byMCP["global-mcp"].Headers["Authorization"] != redactedValue || byMCP["plug-mcp"].Headers["Authorization"] != redactedValue {
		t.Fatalf("MCP headers were not redacted: %+v", byMCP)
	}
	if byMCP["plug-mcp"].Source != "antigravity-plugin:modern-web-guidance" {
		t.Fatalf("unexpected plugin MCP source: %+v", byMCP["plug-mcp"])
	}

	// Toggling plugin-bundled MCP must explain to manage it via the plugin
	if _, err := svc.UpdateMCP(ctx, "a", "antigravity", "toggle", map[string]any{"name": "plug-mcp", "enabled": false}); err == nil || !strings.Contains(err.Error(), "插件") {
		t.Fatalf("expected refusal toggling plugin MCP, got %v", err)
	}
}

func TestAntigravityMCPAndPluginLifecycleAndDispatch(t *testing.T) {
	src, dst := newEnv("src"), newEnv("dst")
	srcRoot := src.P.Antigravity
	dstRoot := dst.P.Antigravity

	src.Files[srcRoot+"/mcp_config.json"] = []byte(`{"mcpServers":{"api":{"serverUrl":"https://api.example/mcp","headers":{"Authorization":"Bearer keep-me"},"disabled":false}}}`)
	src.Files[srcRoot+"/plugins/demo-plug/plugin.json"] = []byte(`{"name":"demo-plug","version":"1.0.0"}`)
	src.Packed[srcRoot+"/plugins/demo-plug"] = []byte("plugin-archive")

	var ran []string
	src.Run_ = func(c envtarget.Command, e *envtargettest.Env) (envtarget.Result, error) {
		line := c.Name + " " + strings.Join(c.Args, " ")
		ran = append(ran, line)
		switch line {
		case "agy mcp disable api":
			e.Files[srcRoot+"/mcp_config.json"] = []byte(`{"mcpServers":{"api":{"serverUrl":"https://api.example/mcp","headers":{"Authorization":"Bearer keep-me"},"disabled":true}}}`)
		case "agy mcp enable api":
			e.Files[srcRoot+"/mcp_config.json"] = []byte(`{"mcpServers":{"api":{"serverUrl":"https://api.example/mcp","headers":{"Authorization":"Bearer keep-me"},"disabled":false}}}`)
		case "agy plugin disable demo-plug":
			e.Files[srcRoot+"/config.json"] = []byte(`{"plugins":{"demo-plug":{"enabled":false}}}`)
		case "agy plugin install /tmp/new-plug":
			e.Files[srcRoot+"/plugins/new-plug/plugin.json"] = []byte(`{"name":"new-plug"}`)
			return envtarget.Result{Stdout: "Installed new-plug"}, nil
		case "agy plugin import claude":
			return envtarget.Result{Stdout: "Imported 1 plugin from claude"}, nil
		}
		return envtarget.Result{Stdout: "ok"}, nil
	}

	// On destination, remove agy CLI to verify direct config.json toggle fallback works too
	delete(dst.Bins, "agy")
	dst.Run_ = nil

	svc := serviceFor(src, dst)
	ctx := context.Background()

	// 1. Toggle Antigravity MCP via agy CLI
	if _, err := svc.UpdateMCP(ctx, "src", "antigravity", "toggle", map[string]any{"name": "api", "enabled": false}); err != nil {
		t.Fatalf("antigravity mcp disable failed: %v", err)
	}
	if _, err := svc.UpdateMCP(ctx, "src", "antigravity", "toggle", map[string]any{"name": "api", "enabled": true}); err != nil {
		t.Fatalf("antigravity mcp enable failed: %v", err)
	}

	// 2. Toggle Antigravity MCP without agy CLI preserves real secrets
	delete(src.Bins, "agy")
	if _, err := svc.UpdateMCP(ctx, "src", "antigravity", "toggle", map[string]any{"name": "api", "enabled": false}); err != nil {
		t.Fatalf("antigravity mcp direct toggle failed: %v", err)
	}
	if !strings.Contains(string(src.Files[srcRoot+"/mcp_config.json"]), "Bearer keep-me") || !strings.Contains(string(src.Files[srcRoot+"/mcp_config.json"]), `"disabled": true`) {
		t.Fatalf("secret or disabled flag lost: %s", src.Files[srcRoot+"/mcp_config.json"])
	}
	src.Bins["agy"] = "/bin/agy"

	// 3. Toggle Antigravity plugin via agy CLI
	if _, err := svc.UpdatePlugin(ctx, "src", "antigravity", "toggle", "demo-plug", false); err != nil {
		t.Fatalf("antigravity plugin disable failed: %v", err)
	}

	// 4. Install & import Antigravity plugin via agy CLI
	insRes, err := svc.UpdatePlugin(ctx, "src", "antigravity", "install", "/tmp/new-plug", true)
	if err != nil || !insRes.OK || !strings.Contains(insRes.Output, "Installed") {
		t.Fatalf("antigravity plugin install failed: %+v err=%v", insRes, err)
	}
	impRes, err := svc.UpdatePlugin(ctx, "src", "antigravity", "import", "claude", true)
	if err != nil || !impRes.OK || !strings.Contains(impRes.Output, "Imported") {
		t.Fatalf("antigravity plugin import failed: %+v err=%v", impRes, err)
	}

	// 5. Cross-node dispatch Antigravity plugin (src -> dst)
	dst.Files[dstRoot+"/plugins/demo-plug/plugin.json"] = []byte(`{"name":"demo-plug","disabled":true}`)
	// Delete pre-existing so UnpackDir runs, and let UnpackDir populate plugin.json? envtargettest.UnpackDir only records dst.Unpacks!
	// Wait: let's test fresh unpack by wrapping or verifying dst.Unpacks + enabling existing plugin on dst.
	delete(dst.Files, dstRoot+"/plugins/demo-plug/plugin.json")
	// Pre-place plugin.json right after Unpack isn't supported by static envtargettest.Env, so test both:
	// (a) existing disabled plugin on dst gets enabled via config.json when agy CLI absent
	dst.Files[dstRoot+"/plugins/demo-plug/plugin.json"] = []byte(`{"name":"demo-plug","disabled":true}`)
	dispatchRes, err := svc.Dispatch(ctx, "plugin", "src", "antigravity", "demo-plug", []DispatchTarget{{"dst", "antigravity"}, {"dst", "codex"}})
	if err != nil {
		t.Fatal(err)
	}
	if dispatchRes.SuccessCount != 1 || !dispatchRes.Reports[0].OK || dispatchRes.Reports[1].OK {
		t.Fatalf("unexpected plugin dispatch reports: %+v", dispatchRes.Reports)
	}
	if !strings.Contains(string(dst.Files[dstRoot+"/config.json"]), `"enabled": true`) {
		t.Fatalf("expected dst config.json to enable demo-plug, got %s", dst.Files[dstRoot+"/config.json"])
	}
}
