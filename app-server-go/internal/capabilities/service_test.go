package capabilities

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"astrorder.dev/app-server/internal/envtarget"
	"astrorder.dev/app-server/internal/envtarget/envtargettest"
)

func newEnv(id string) *envtargettest.Env {
	env := envtargettest.New(id)
	env.Bins["codex"], env.Bins["hermes"], env.Bins["grok"], env.Bins["npx"], env.Bins["rtk"] = "/bin/codex", "/bin/hermes", "/bin/grok", "/bin/npx", "/bin/rtk"
	return env
}

func serviceFor(envs ...*envtargettest.Env) *Service {
	return New(envtargettest.Opener{Envs: envs})
}

func TestCommitRollsBackWhenReadBackDisagrees(t *testing.T) {
	env := newEnv("a")
	file := env.P.Codex + "/config.toml"
	env.Files[file] = []byte(codexSample)
	svc := serviceFor(env)
	err := svc.editConfig(context.Background(), env, "codex",
		func(data []byte) ([]byte, error) {
			return append(append([]byte{}, data...), []byte("# changed\n")...), nil
		},
		func([]byte) error { return errors.New("nope") })
	if err == nil || !strings.Contains(err.Error(), "已还原") {
		t.Fatalf("err = %v", err)
	}
	if string(env.Files[file]) != codexSample {
		t.Fatalf("file not restored:\n%s", env.Files[file])
	}
}

func TestCommitSkipsWriteWhenNothingChanges(t *testing.T) {
	env := newEnv("a")
	file := env.P.Codex + "/config.toml"
	env.Files[file] = []byte(codexSample)
	svc := serviceFor(env)
	if err := svc.editConfig(context.Background(), env, "codex", func(d []byte) ([]byte, error) { return d, nil }, func([]byte) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if len(env.Writes) != 0 {
		t.Fatalf("wrote %v", env.Writes)
	}
}

func TestCommitKeepsBackupAndRemovesNewFileOnFailure(t *testing.T) {
	env := newEnv("a")
	svc := serviceFor(env)
	file := env.P.Grok + "/config.toml"
	err := svc.editConfig(context.Background(), env, "grok",
		func([]byte) ([]byte, error) { return []byte("x = 1\n"), nil },
		func([]byte) error { return errors.New("bad") })
	if err == nil {
		t.Fatal("expected failure")
	}
	if _, ok := env.Files[file]; ok {
		t.Fatal("a file that did not exist before must not be left behind")
	}
	env.Files[file] = []byte("a = 1\n")
	if err := svc.editConfig(context.Background(), env, "grok", func([]byte) ([]byte, error) { return []byte("a = 2\n"), nil }, func([]byte) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if string(env.Files[file+".astrorder.bak"]) != "a = 1\n" {
		t.Fatalf("backup = %q", env.Files[file+".astrorder.bak"])
	}
}

func TestUpdateMCPEndToEndOnEachAgent(t *testing.T) {
	env := newEnv("a")
	env.Files[env.P.Codex+"/config.toml"] = []byte(codexSample)
	env.Files[env.P.Grok+"/config.toml"] = []byte(grokSample)
	env.Files[env.P.Hermes+"/config.yaml"] = []byte(hermesSample)
	svc := serviceFor(env)
	ctx := context.Background()
	for _, agent := range agents {
		if _, err := svc.UpdateMCP(ctx, "a", agent, "save", map[string]any{"name": "added", "url": "http://127.0.0.1:9/mcp"}); err != nil {
			t.Fatalf("%s save: %v", agent, err)
		}
	}
	summary, err := svc.Summary(ctx, "a", "")
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, server := range summary.MCPServers {
		if server.Name == "added" {
			seen[server.Agent] = true
		}
	}
	if len(seen) != 3 {
		t.Fatalf("added server visible for %v", seen)
	}
	for _, agent := range agents {
		if _, err := svc.UpdateMCP(ctx, "a", agent, "delete", map[string]any{"name": "added"}); err != nil {
			t.Fatalf("%s delete: %v", agent, err)
		}
	}
	if string(env.Files[env.P.Codex+"/config.toml"]) != codexSample {
		t.Errorf("codex config differs after save + delete")
	}
	if _, err := svc.UpdateMCP(ctx, "a", "codex", "save", map[string]any{"name": "bad name", "url": "http://x"}); err == nil {
		t.Error("invalid name accepted")
	}
	if _, err := svc.UpdateMCP(ctx, "a", "claude", "save", map[string]any{"name": "x", "url": "http://x"}); err == nil {
		t.Error("unknown agent accepted")
	}
}

func TestSummaryRedactsSecretsAndFilters(t *testing.T) {
	env := newEnv("a")
	env.Files[env.P.Codex+"/config.toml"] = []byte(codexSample)
	env.Files[env.P.Hermes+"/config.yaml"] = []byte(hermesSample)
	env.Files[env.P.Grok+"/config.toml"] = []byte(grokSample)
	env.Files[env.P.Codex+"/skills/alpha/SKILL.md"] = []byte("---\nname: alpha\ndescription: first\n---\n")
	env.Files[env.P.Hermes+"/skills/beta/SKILL.md"] = []byte("---\nname: beta\n---\n")
	env.Files[env.P.Hermes+"/skills/.archive/old/SKILL.md"] = []byte("---\nname: old\n---\n")
	env.Files[env.P.Agents+"/skills/alpha/SKILL.md"] = []byte("---\nname: alpha\n---\n")
	env.Files[env.P.Agents+"/.skill-lock.json"] = []byte(`{"skills":{"alpha":{"source":"o/r","sourceUrl":"https://github.com/o/r.git"}}}`)
	svc := serviceFor(env)
	all, err := svc.Summary(context.Background(), "a", "all")
	if err != nil {
		t.Fatal(err)
	}
	raw := ""
	for _, server := range all.MCPServers {
		for _, v := range server.Headers {
			raw += v
		}
		for _, v := range server.Env {
			raw += v
		}
	}
	for _, secret := range []string{"top-secret", "t0ken", "Bearer h", "Bearer g"} {
		if strings.Contains(raw, secret) {
			t.Errorf("secret %q leaked", secret)
		}
	}
	names := map[string]Skill{}
	for _, skill := range all.Skills {
		names[skill.Name] = skill
	}
	if _, ok := names["old"]; ok {
		t.Error("archived skill listed")
	}
	if alpha := names["alpha"]; len(alpha.Agents) != 1 || alpha.Agents[0] != "codex" || alpha.Description != "first" {
		t.Errorf("alpha = %+v", alpha)
	}
	if all.Counts["mcp"] != 5 || all.Counts["marketplaces"] != 1 {
		t.Errorf("counts = %v", all.Counts)
	}
	hermes, _ := svc.Summary(context.Background(), "a", "hermes")
	for _, server := range hermes.MCPServers {
		if server.Agent != "hermes" {
			t.Errorf("filter leaked %s", server.Agent)
		}
	}
	installed := filterBy(hermes.Skills, func(skill Skill) bool { return skill.Scope != "builtin" })
	if len(installed) != 1 || installed[0].Name != "beta" {
		t.Errorf("hermes skills = %+v", hermes.Skills)
	}
}

func TestPluginToggleFallsBackToConfigWithoutCLI(t *testing.T) {
	env := newEnv("a")
	delete(env.Bins, "codex")
	delete(env.Bins, "hermes")
	env.Files[env.P.Codex+"/config.toml"] = []byte(codexSample)
	env.Files[env.P.Hermes+"/config.yaml"] = []byte(hermesSample)
	svc := serviceFor(env)
	ctx := context.Background()
	if _, err := svc.UpdatePlugin(ctx, "a", "codex", "toggle", "github@openai-curated", false); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.UpdatePlugin(ctx, "a", "hermes", "toggle", "rtk-rewrite", false); err != nil {
		t.Fatal(err)
	}
	summary, _ := svc.Summary(ctx, "a", "")
	for _, plugin := range summary.Plugins {
		if (plugin.Name == "github" || plugin.Name == "rtk-rewrite") && plugin.Enabled {
			t.Errorf("%s still enabled", plugin.FullName)
		}
	}
	if _, err := svc.UpdatePlugin(ctx, "a", "codex", "toggle", "ghost@nowhere", true); err == nil {
		t.Error("unknown plugin accepted")
	}
	if _, err := svc.UpdatePlugin(ctx, "a", "codex", "toggle", "../x", true); err == nil {
		t.Error("bad plugin name accepted")
	}
}

func TestPluginUninstallUsesAgentCLI(t *testing.T) {
	env := newEnv("a")
	env.Files[env.P.Codex+"/config.toml"] = []byte(codexSample)
	env.Run_ = func(c envtarget.Command, e *envtargettest.Env) (envtarget.Result, error) {
		if c.Name == "codex" && strings.Join(c.Args, " ") == "plugin remove github@openai-curated" {
			e.Files[e.P.Codex+"/config.toml"] = []byte(strings.Replace(string(e.Files[e.P.Codex+"/config.toml"]), "[plugins.\"github@openai-curated\"]\nenabled = true\n", "", 1))
			return envtarget.Result{Stdout: "removed"}, nil
		}
		return envtarget.Result{Code: 1, Stderr: "unexpected"}, nil
	}
	svc := serviceFor(env)
	if _, err := svc.UpdatePlugin(context.Background(), "a", "codex", "uninstall", "github@openai-curated", true); err != nil {
		t.Fatal(err)
	}
	if env.Calls[0].Env["CODEX_HOME"] != env.P.Codex {
		t.Errorf("CODEX_HOME = %q", env.Calls[0].Env["CODEX_HOME"])
	}
}

func TestMarketplaceCommandsAndVerification(t *testing.T) {
	env := newEnv("a")
	env.Files[env.P.Codex+"/config.toml"] = []byte(codexSample)
	var last string
	env.Run_ = func(c envtarget.Command, e *envtargettest.Env) (envtarget.Result, error) {
		last = c.Name + " " + strings.Join(c.Args, " ")
		if strings.HasPrefix(last, "codex plugin marketplace add") {
			e.Files[e.P.Codex+"/config.toml"] = append(e.Files[e.P.Codex+"/config.toml"], []byte("\n[marketplaces.demo]\nsource_type = \"git\"\nsource = \"https://github.com/o/demo.git\"\n")...)
		}
		return envtarget.Result{}, nil
	}
	svc := serviceFor(env)
	ctx := context.Background()
	if _, err := svc.UpdateMarketplace(ctx, "a", "codex", "add", map[string]any{"name": "demo", "source": "o/demo", "ref": "main"}); err != nil {
		t.Fatal(err)
	}
	if last != "codex plugin marketplace add --ref main o/demo" {
		t.Errorf("command = %q", last)
	}
	for _, source := range []string{"--evil", "a b", "x;rm -rf /", "$(id)", ""} {
		if _, err := svc.UpdateMarketplace(ctx, "a", "codex", "add", map[string]any{"name": "n", "source": source}); err == nil {
			t.Errorf("source %q accepted", source)
		}
	}
	if _, err := svc.UpdateMarketplace(ctx, "a", "codex", "delete", map[string]any{"name": "demo"}); err == nil || !strings.Contains(err.Error(), "仍在") {
		t.Errorf("delete that did not remove the entry must fail, got %v", err)
	}
	if _, err := svc.UpdateMarketplace(ctx, "a", "hermes", "add", map[string]any{"name": "n", "source": "o/r"}); err == nil {
		t.Error("hermes has no marketplaces")
	}
}

func TestDispatchMCPToSeveralTargets(t *testing.T) {
	src, dst := newEnv("src"), newEnv("dst")
	src.Files[src.P.Codex+"/config.toml"] = []byte(codexSample)
	svc := serviceFor(src, dst)
	result, err := svc.Dispatch(context.Background(), "mcp", "src", "codex", "astrorder",
		[]DispatchTarget{{"dst", "codex"}, {"dst", "hermes"}, {"dst", "grok"}, {"missing", "codex"}, {"dst", "nope"}})
	if err != nil {
		t.Fatal(err)
	}
	if result.SuccessCount != 3 || result.TotalTargets != 5 || !result.OK {
		t.Fatalf("%+v", result)
	}
	cfg, _ := parseTOML(dst.Files[dst.P.Codex+"/config.toml"])
	headers := asStringMap(asMap(asMap(cfg["mcp_servers"])["astrorder"])["http_headers"])
	if headers["Authorization"] != "Bearer top-secret" {
		t.Errorf("secret header was not carried over: %v", headers)
	}
	hermes, _ := parseYAMLMap(dst.Files[dst.P.Hermes+"/config.yaml"])
	if asStringMap(asMap(asMap(hermes["mcp_servers"])["astrorder"])["headers"])["Authorization"] != "Bearer top-secret" {
		t.Errorf("hermes copy lost the header")
	}
	if _, err := svc.Dispatch(context.Background(), "mcp", "src", "codex", "ghost", []DispatchTarget{{"dst", "codex"}}); err == nil {
		t.Error("unknown source item accepted")
	}
}

func TestDispatchSkillCopiesDirectory(t *testing.T) {
	src, dst := newEnv("src"), newEnv("dst")
	src.Files[src.P.Codex+"/skills/alpha/SKILL.md"] = []byte("---\nname: alpha\n---\n")
	src.Packed[src.P.Codex+"/skills/alpha"] = []byte("archive")
	svc := serviceFor(src, dst)
	result, err := svc.Dispatch(context.Background(), "skill", "src", "codex", "alpha", []DispatchTarget{{"dst", "hermes"}, {"dst", "grok"}})
	if err != nil {
		t.Fatal(err)
	}
	if result.SuccessCount != 2 {
		t.Fatalf("%+v", result)
	}
	if _, ok := dst.Unpacks[dst.P.Hermes+"/skills/alpha"]; !ok {
		t.Errorf("hermes skill dir not written: %v", dst.Unpacks)
	}
	if _, err := svc.Dispatch(context.Background(), "skill", "src", "codex", "../etc", []DispatchTarget{{"dst", "codex"}}); err == nil {
		t.Error("path-like skill name accepted")
	}
}

func TestDispatchPluginOnlyToSameAgent(t *testing.T) {
	src, dst := newEnv("src"), newEnv("dst")
	src.Files[src.P.Codex+"/config.toml"] = []byte(codexSample)
	dst.Files[dst.P.Codex+"/config.toml"] = []byte(strings.Replace(codexSample, "enabled = true", "enabled = false", 1))
	svc := serviceFor(src, dst)
	result, err := svc.Dispatch(context.Background(), "plugin", "src", "codex", "github@openai-curated", []DispatchTarget{{"dst", "codex"}, {"dst", "hermes"}})
	if err != nil {
		t.Fatal(err)
	}
	if result.SuccessCount != 1 || result.Reports[0].OK != true || result.Reports[1].OK {
		t.Fatalf("%+v", result.Reports)
	}
	cfg, _ := parseTOML(dst.Files[dst.P.Codex+"/config.toml"])
	if !asBool(asMap(asMap(cfg["plugins"])["github@openai-curated"])["enabled"], false) {
		t.Errorf("plugin not enabled on the destination")
	}
}

func TestSkillInstallAndRemove(t *testing.T) {
	env := newEnv("a")
	var calls []string
	env.Run_ = func(c envtarget.Command, e *envtargettest.Env) (envtarget.Result, error) {
		calls = append(calls, strings.Join(c.Args, " "))
		if c.Args[2] == "add" {
			e.Files[e.P.Agents+"/skills/context7/SKILL.md"] = []byte("---\nname: context7\n---\n")
			e.Files[e.P.Agents+"/.skill-lock.json"] = []byte(`{"skills":{"context7":{"source":"o/r"}}}`)
		}
		if c.Args[2] == "remove" {
			delete(e.Files, e.P.Agents+"/skills/context7/SKILL.md")
		}
		return envtarget.Result{Stdout: "done"}, nil
	}
	svc := serviceFor(env)
	ctx := context.Background()
	result := svc.InstallSkill(ctx, "a", "o/r@context7", "context7")
	if !result.Success {
		t.Fatalf("%+v", result)
	}
	if calls[0] != "-y skills add o/r@context7 -g -y -a codex hermes-agent grok --skill context7" {
		t.Errorf("install command = %q", calls[0])
	}
	if bad := svc.InstallSkill(ctx, "a", "--global", ""); bad.Success || bad.Error == "" {
		t.Errorf("flag-like package accepted: %+v", bad)
	}
	removed, err := svc.RemoveSkill(ctx, "a", "context7")
	if err != nil || !removed.Success {
		t.Fatalf("%v %+v", err, removed)
	}
	if _, err := svc.RemoveSkill(ctx, "a", "context7"); err == nil {
		t.Error("removing a missing skill must fail")
	}
}

func TestRemoveNativeSkillArchivesInsteadOfDeleting(t *testing.T) {
	env := newEnv("a")
	env.Files[env.P.Hermes+"/skills/mine/SKILL.md"] = []byte("---\nname: mine\n---\n")
	svc := serviceFor(env)
	result, err := svc.RemoveSkill(context.Background(), "a", "mine")
	if err != nil || !result.Success {
		t.Fatalf("%v %+v", err, result)
	}
	found := false
	for name := range env.Files {
		if strings.HasPrefix(name, env.P.Hermes+"/skills/.archive/mine-") {
			found = true
		}
	}
	if !found {
		t.Errorf("not archived: %v", keysOf(env.Files))
	}
}

func keysOf(m map[string][]byte) []string {
	out := []string{}
	for k := range m {
		out = append(out, k)
	}
	return out
}

func TestSearchParsesRegistry(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("q") != "react" {
			t.Errorf("q = %q", r.URL.Query().Get("q"))
		}
		_, _ = w.Write([]byte(`{"skills":[{"id":"vercel-labs/agent-skills/best","source":"vercel-labs/agent-skills","skillId":"best","name":"best","installs":787787},{"id":"","source":"","skillId":"","name":""}]}`))
	}))
	defer server.Close()
	old := SearchURL
	SearchURL = server.URL
	defer func() { SearchURL = old }()
	result := serviceFor().Search(context.Background(), "react")
	if len(result.Items) != 1 || result.Items[0].Pkg != "vercel-labs/agent-skills@best" || result.Items[0].Installs != "787.8K installs" ||
		result.Items[0].URL != "https://skills.sh/vercel-labs/agent-skills/best" {
		t.Fatalf("%+v", result)
	}
	SearchURL = "http://127.0.0.1:1/x"
	if result := serviceFor().Search(context.Background(), "react"); result.Error == "" || result.Items == nil {
		t.Fatalf("unreachable registry must report an error and an empty list: %+v", result)
	}
}

func TestPingHTTPAndCommand(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer real" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusMethodNotAllowed)
	}))
	defer server.Close()
	env := newEnv("a")
	env.Info.Kind = "local"
	env.Files[env.P.Codex+"/config.toml"] = []byte("[mcp_servers.s]\nurl = \"" + server.URL + "\"\n[mcp_servers.s.http_headers]\nAuthorization = \"Bearer real\"\n")
	svc := serviceFor(env)
	ctx := context.Background()
	res, err := svc.Ping(ctx, "a", map[string]any{"name": "s", "agent": "codex", "url": server.URL, "headers": map[string]any{"Authorization": redactedValue}})
	if err != nil || !res.OK || res.StatusCode != 405 {
		t.Fatalf("%v %+v", err, res)
	}
	res, _ = svc.Ping(ctx, "a", map[string]any{"url": server.URL})
	if res.OK || res.StatusCode != 401 || !strings.Contains(res.Message, "认证") {
		t.Errorf("%+v", res)
	}
	res, _ = svc.Ping(ctx, "a", map[string]any{"url": "http://127.0.0.1:1/"})
	if res.OK || res.LatencyMS != -1 {
		t.Errorf("%+v", res)
	}
	res, _ = svc.Ping(ctx, "a", map[string]any{"command": "npx"})
	if !res.OK {
		t.Errorf("%+v", res)
	}
	res, _ = svc.Ping(ctx, "a", map[string]any{"command": "no-such-binary"})
	if res.OK {
		t.Errorf("%+v", res)
	}
	res, _ = svc.Ping(ctx, "a", map[string]any{"url": "file:///etc/passwd"})
	if res.OK {
		t.Errorf("non-http url accepted: %+v", res)
	}
}

func TestAggregateGainFiltersAndRecomputes(t *testing.T) {
	saved := 5.0
	rows := []gainDay{
		{Date: "2026-10-11", Commands: 2, InputTokens: 100, OutputTokens: 20, SavedTokens: &saved},
		{Date: "2026-10-10", Commands: 1, InputTokens: 50, OutputTokens: 10},
		{Date: "2026-09-01", Commands: 9, InputTokens: 900, OutputTokens: 100},
		{Date: "bad", Commands: 1},
	}
	now := time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)
	daily, summary := aggregateGain(rows, "7", now)
	if len(daily) != 2 || summary.TotalCommands != 3 || summary.TotalInput != 150 || summary.TotalSaved != 45 || summary.AvgSavingsPct != 30 {
		t.Fatalf("%+v %+v", daily, summary)
	}
	if daily, _ := aggregateGain(rows, "all", now); len(daily) != 3 {
		t.Fatalf("all: %d", len(daily))
	}
	if daily, _ := aggregateGain(rows, "garbage", now); len(daily) != 2 {
		t.Fatalf("garbage days should fall back to 30: %d", len(daily))
	}
}

func TestRTKStatusReadsAgentFiles(t *testing.T) {
	env := newEnv("a")
	env.Info.Kind = "ssh"
	p := env.P
	env.Files[p.Codex+"/hooks.json"] = []byte(`{"hooks":{"PreToolUse":[{"hooks":[{"command":"rtk hook codex"}]}]}}`)
	env.Files[p.Codex+"/RTK.md"] = []byte("x")
	env.Files[p.Codex+"/AGENTS.md"] = []byte("@/home/test/.codex/RTK.md\n")
	env.Files[p.Hermes+"/plugins/rtk-rewrite/__init__.py"] = []byte("x")
	env.Files[p.Hermes+"/plugins/rtk-rewrite/plugin.yaml"] = []byte("x")
	env.Files[p.Hermes+"/config.yaml"] = []byte("plugins:\n  enabled: []\n")
	env.Run_ = func(c envtarget.Command, e *envtargettest.Env) (envtarget.Result, error) {
		if len(c.Args) > 0 && c.Args[0] == "--version" {
			return envtarget.Result{Stdout: "rtk 0.50.0\n"}, nil
		}
		return envtarget.Result{Stdout: `{"daily":[{"date":"2026-10-11","commands":2,"input_tokens":100,"output_tokens":40}]}`}, nil
	}
	status, err := serviceFor(env).rtkStatus(context.Background(), env, "all", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if !status.Installed || status.Version == nil || *status.Version != "0.50.0" || status.Summary.TotalSaved != 60 {
		t.Fatalf("%+v", status)
	}
	byKind := map[string]RTKAgent{}
	for _, agent := range status.Agents {
		byKind[agent.Kind] = agent
	}
	if !byKind["codex"].Enabled || byKind["hermes"].Enabled || byKind["grok"].Supported {
		t.Fatalf("%+v", byKind)
	}
	delete(env.Bins, "rtk")
	status, _ = serviceFor(env).rtkStatus(context.Background(), env, "30", time.Now())
	if status.Installed || status.Version != nil || status.Executable != nil {
		t.Fatalf("%+v", status)
	}
}

func TestSetRTKAgentRestoresOnMismatchAndFailure(t *testing.T) {
	env := newEnv("a")
	p := env.P
	env.Files[p.Codex+"/AGENTS.md"] = []byte("mine\n")
	mode := "lie"
	env.Run_ = func(c envtarget.Command, e *envtargettest.Env) (envtarget.Result, error) {
		if len(c.Args) > 0 && c.Args[0] == "--version" {
			return envtarget.Result{Stdout: "rtk 0.50.0"}, nil
		}
		if len(c.Args) > 0 && c.Args[0] == "gain" {
			return envtarget.Result{Stdout: `{"daily":[]}`}, nil
		}
		switch mode {
		case "lie":
			e.Files[p.Codex+"/RTK.md"] = []byte("x")
			e.Files[p.Codex+"/AGENTS.md"] = []byte("mine\n@/home/test/.codex/RTK.md\n")
			return envtarget.Result{}, nil // no hook written: only half enabled
		case "fail":
			e.Files[p.Codex+"/RTK.md"] = []byte("partial")
			return envtarget.Result{Code: 1, Stderr: "boom"}, nil
		}
		e.Files[p.Codex+"/RTK.md"] = []byte("x")
		e.Files[p.Codex+"/AGENTS.md"] = []byte("mine\n@/home/test/.codex/RTK.md\n")
		e.Files[p.Codex+"/hooks.json"] = []byte(`{"command":"rtk hook codex"}`)
		return envtarget.Result{}, nil
	}
	svc := serviceFor(env)
	ctx := context.Background()
	if _, err := svc.SetRTKAgent(ctx, "a", "codex", true, "30"); err == nil || !strings.Contains(err.Error(), "回读不符") {
		t.Fatalf("err = %v", err)
	}
	if string(env.Files[p.Codex+"/AGENTS.md"]) != "mine\n" || env.Files[p.Codex+"/RTK.md"] != nil {
		t.Fatalf("not restored: %v", keysOf(env.Files))
	}
	mode = "fail"
	if _, err := svc.SetRTKAgent(ctx, "a", "codex", true, "30"); err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("err = %v", err)
	}
	if env.Files[p.Codex+"/RTK.md"] != nil {
		t.Fatalf("partial file left behind")
	}
	mode = "ok"
	status, err := svc.SetRTKAgent(ctx, "a", "codex", true, "30")
	if err != nil {
		t.Fatal(err)
	}
	for _, agent := range status.Agents {
		if agent.Kind == "codex" && !agent.Enabled {
			t.Fatalf("codex should be enabled: %+v", agent)
		}
	}
	if _, err := svc.SetRTKAgent(ctx, "a", "grok", true, "30"); err == nil {
		t.Error("grok has no RTK support")
	}
	delete(env.Bins, "rtk")
	if _, err := svc.SetRTKAgent(ctx, "a", "codex", false, "30"); err == nil {
		t.Error("toggle without rtk installed must fail")
	}
}

func TestRTKArgs(t *testing.T) {
	cases := map[string]string{"codex/true": "init -g --codex", "codex/false": "init -g --codex --uninstall",
		"hermes/true": "init -g --agent hermes", "hermes/false": "init -g --agent hermes --uninstall"}
	for key, want := range cases {
		kind, on, _ := strings.Cut(key, "/")
		got, err := rtkAgentArgs(kind, on == "true")
		if err != nil || strings.Join(got, " ") != want {
			t.Errorf("%s: %v %v", key, got, err)
		}
	}
}
