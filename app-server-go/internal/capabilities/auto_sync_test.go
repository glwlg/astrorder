package capabilities

import (
	"context"
	"encoding/json"
	"errors"
	"path"
	"strings"
	"testing"

	"astrorder.dev/app-server/internal/agentskill"
	"astrorder.dev/app-server/internal/capabilities/officialskills"
	"astrorder.dev/app-server/internal/envtarget"
	"astrorder.dev/app-server/internal/envtarget/envtargettest"
)

type multiTargetOpener struct {
	targets []envtarget.Target
	envs    map[string]envtarget.Env
	errs    map[string]error
}

func (m *multiTargetOpener) List(context.Context) ([]envtarget.Target, error) {
	return m.targets, nil
}

func (m *multiTargetOpener) Open(_ context.Context, id string) (envtarget.Env, error) {
	if err, ok := m.errs[id]; ok {
		return nil, err
	}
	env, ok := m.envs[id]
	if !ok {
		return nil, envtarget.Errorf(404, "missing target %s", id)
	}
	return env, nil
}

func TestAutoSyncManagedSkillsUpgradesOfficialInstallsPluginsAndCleansDisabled(t *testing.T) {
	ctx := context.Background()
	env := envtargettest.New("ssh-wsl")
	env.Info.Name = "WSL"
	env.Info.ConnectionID = "ssh-wsl"
	env.Shell_ = func(script string, _ []byte, _ []string, _ *envtargettest.Env) (envtarget.Result, error) {
		return envtarget.Result{Code: 0}, nil
	}
	env.Bins[path.Join(env.P.Codex, "skills", "astrorder-sessions", "scripts", "astrorder-agent")] = path.Join(env.P.Codex, "skills", "astrorder-sessions", "scripts", "astrorder-agent")
	env.Bins[path.Join(env.P.Codex, "skills", "astrorder-gitlab-ci", "scripts", "astrorder-agent")] = path.Join(env.P.Codex, "skills", "astrorder-gitlab-ci", "scripts", "astrorder-agent")
	env.Run_ = func(cmd envtarget.Command, _ *envtargettest.Env) (envtarget.Result, error) {
		if strings.Contains(cmd.Name, "astrorder-agent") && len(cmd.Args) == 1 && cmd.Args[0] == "--help" {
			return envtarget.Result{Code: 0, Stdout: "astrorder-agent help"}, nil
		}
		return envtarget.Result{Code: 0}, nil
	}

	codexHome := env.P.Codex
	callerRaw := []byte(`{"agent_id":"codex-wsl","connection_id":"ssh-wsl","profile_name":"default","runtime":"codex"}` + "\n")
	env.Files[path.Join(codexHome, agentskill.CallerFile)] = callerRaw

	// Seed an outdated official skill (old SKILL.md + old astrorder-agent binary).
	oldMD := []byte("---\nname: astrorder-sessions\ndescription: old\n---\nold content\n")
	oldCLI := []byte("old-astrorder-agent-binary")
	sessDir := path.Join(codexHome, "skills", "astrorder-sessions")
	env.Files[path.Join(sessDir, "SKILL.md")] = oldMD
	env.Files[path.Join(sessDir, "scripts", "astrorder-agent")] = oldCLI
	oldManifest, _ := json.Marshal(officialManifest{
		CallerHash: payloadHash(callerRaw),
		Owner:      officialManifestOwner,
		Version:    officialManifestVersion,
		Skill:      "astrorder-sessions",
		Agent:      "codex",
		Files: map[string]string{
			"SKILL.md":                payloadHash(oldMD),
			"scripts/astrorder-agent": payloadHash(oldCLI),
		},
	})
	env.Files[path.Join(sessDir, officialManifestFile)] = oldManifest

	// Seed a user-edited official skill (astrorder-browser) that MUST NOT be overwritten.
	browserDir := path.Join(codexHome, "skills", "astrorder-browser")
	env.Files[path.Join(browserDir, "SKILL.md")] = []byte("user hand-edited skill")
	env.Files[path.Join(browserDir, "scripts", "astrorder-agent")] = oldCLI
	browserManifest, _ := json.Marshal(officialManifest{
		CallerHash: payloadHash(callerRaw),
		Owner:      officialManifestOwner,
		Version:    officialManifestVersion,
		Skill:      "astrorder-browser",
		Agent:      "codex",
		Files: map[string]string{
			"SKILL.md":                payloadHash([]byte("original unmodified md")),
			"scripts/astrorder-agent": payloadHash(oldCLI),
		},
	})
	env.Files[path.Join(browserDir, officialManifestFile)] = browserManifest

	// Also add an unreachable offline SSH host to prove it never blocks reachable machines.
	opener := &multiTargetOpener{
		targets: []envtarget.Target{
			{ID: "ssh-offline", Name: "Offline", Kind: "ssh", ConnectionID: "ssh-offline"},
			{ID: "ssh-wsl", Name: "WSL", Kind: "ssh", ConnectionID: "ssh-wsl"},
		},
		envs: map[string]envtarget.Env{"ssh-wsl": env},
		errs: map[string]error{"ssh-offline": errors.New("ssh dial timeout")},
	}

	newCLI := []byte("new-astrorder-agent-binary-v2")
	enabledPlugins := []PluginSkill{
		{
			PluginID:    "gitlab-ci-viewer",
			Name:        "astrorder-gitlab-ci",
			DisplayName: "GitLab CI",
			Description: "Query GitLab CI",
			Content:     "---\nname: astrorder-gitlab-ci\ndescription: gitlab\n---\nCaller: {{CALLER_CONFIG_PATH}}\n",
		},
	}

	svc := New(opener)
	svc.CLIBinary = func(context.Context, envtarget.Env) (string, []byte, error) {
		return "astrorder-agent", newCLI, nil
	}
	svc.SetPluginSkillProvider(PluginSkillProviderFunc(func(context.Context) []PluginSkill {
		return enabledPlugins
	}))

	// Pass 1: upgrades astrorder-sessions, installs astrorder-gitlab-ci, skips offline host & user-edited browser skill.
	rep1 := svc.AutoSyncManagedSkills(ctx)
	if rep1.MachinesChecked != 1 || rep1.MachinesSkipped != 1 {
		t.Fatalf("unexpected machine counts: %+v", rep1)
	}
	if len(rep1.SkillsUpdated) != 1 || rep1.SkillsUpdated[0] != "ssh-wsl/codex/astrorder-sessions" {
		t.Fatalf("expected astrorder-sessions updated, got %+v", rep1)
	}
	if len(rep1.SkillsInstalled) != 1 || rep1.SkillsInstalled[0] != "ssh-wsl/codex/astrorder-gitlab-ci" {
		t.Fatalf("expected astrorder-gitlab-ci installed, got %+v", rep1)
	}
	if string(env.Files[path.Join(sessDir, "scripts", "astrorder-agent")]) != string(newCLI) {
		t.Fatalf("expected astrorder-sessions CLI upgraded")
	}
	sessSkill, _ := officialskills.GetOfficialSkill("astrorder-sessions")
	if string(env.Files[path.Join(sessDir, "SKILL.md")]) != string(officialskills.RenderOfficialSkill(*sessSkill, codexHome)) {
		t.Fatalf("expected astrorder-sessions SKILL.md upgraded")
	}
	if string(env.Files[path.Join(browserDir, "SKILL.md")]) != "user hand-edited skill" {
		t.Fatalf("user-edited SKILL.md must NEVER be overwritten")
	}
	glDir := path.Join(codexHome, "skills", "astrorder-gitlab-ci")
	if !strings.Contains(string(env.Files[path.Join(glDir, "SKILL.md")]), path.Join(codexHome, "astrorder-caller.json")) {
		t.Fatalf("expected placeholder rendered in plugin SKILL.md, got %q", string(env.Files[path.Join(glDir, "SKILL.md")]))
	}
	if string(env.Files[path.Join(glDir, "scripts", "astrorder-agent")]) != string(newCLI) {
		t.Fatalf("expected CLI installed alongside plugin skill")
	}

	// Pass 2 (idempotency): zero updates/installs, both unchanged!
	rep2 := svc.AutoSyncManagedSkills(ctx)
	if len(rep2.SkillsUpdated) != 0 || len(rep2.SkillsInstalled) != 0 || len(rep2.SkillsRemoved) != 0 || rep2.SkillsUnchanged != 2 {
		t.Fatalf("expected idempotent no-op pass, got %+v", rep2)
	}

	// Verify Summary rolls installed plugin skill into InstalledAgents instead of duplicating.
	summary, err := svc.Summary(ctx, "ssh-wsl", "all")
	if err != nil {
		t.Fatalf("Summary: %v", err)
	}
	pluginMatches := 0
	for _, sk := range summary.Skills {
		if sk.Name == "astrorder-gitlab-ci" {
			pluginMatches++
			if sk.Source != "plugin" || len(sk.InstalledAgents) != 1 || sk.InstalledAgents[0] != "codex" {
				t.Fatalf("unexpected plugin skill summary entry: %+v", sk)
			}
		}
	}
	if pluginMatches != 1 {
		t.Fatalf("expected exactly 1 merged plugin skill entry, got %d", pluginMatches)
	}

	// Pass 3: disable the plugin -> auto-removes unmodified plugin skill directory cleanly!
	enabledPlugins = nil
	rep3 := svc.AutoSyncManagedSkills(ctx)
	if len(rep3.SkillsRemoved) != 1 || rep3.SkillsRemoved[0] != "ssh-wsl/codex/astrorder-gitlab-ci" {
		t.Fatalf("expected disabled plugin skill removed, got %+v", rep3)
	}
	if _, stillThere := env.Files[path.Join(glDir, "SKILL.md")]; stillThere {
		t.Fatalf("expected plugin skill files deleted on disable")
	}
}
