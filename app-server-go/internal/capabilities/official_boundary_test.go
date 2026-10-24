package capabilities

import (
	"context"
	"encoding/json"
	"path"
	"testing"
)

func TestOfficialInstalledModuleMergesIntoCatalog(t *testing.T) {
	env := localEnv("local")
	provisionLegacyCLI(t, env, env.P.Grok, "grok")
	svc := serviceFor(env)
	ctx := context.Background()
	if r, err := svc.Dispatch(ctx, "skill", "local", "astrorder", "astrorder-browser", []DispatchTarget{{TargetID: "local", Agent: "grok"}}); err != nil || r.SuccessCount != 1 {
		t.Fatalf("dispatch failed: %v", err)
	}
	summary, err := svc.Summary(ctx, "local", "all")
	if err != nil {
		t.Fatal(err)
	}
	matches := 0
	for _, s := range summary.Skills {
		if s.Name == "astrorder-browser" {
			matches++
			if s.Scope != "builtin" || len(s.InstalledAgents) != 1 || s.InstalledAgents[0] != "grok" {
				t.Fatalf("invalid merged installation: %+v", s)
			}
		}
	}
	if matches != 1 {
		t.Fatalf("duplicate module count=%d", matches)
	}
	if _, err := svc.Dispatch(ctx, "skill", "local", "grok", "astrorder-browser", []DispatchTarget{{TargetID: "local", Agent: "hermes"}}); err == nil {
		t.Fatal("installed official module entered generic copy path")
	}
}

func TestOfficialModuleRepairsMissingDocument(t *testing.T) {
	env := localEnv("local")
	provisionLegacyCLI(t, env, env.P.Codex, "codex")
	svc := serviceFor(env)
	ctx := context.Background()
	deliver, _ := svc.officialSkillDelivery("astrorder-browser")
	folder, _, err := deliver(ctx, env, "codex")
	if err != nil {
		t.Fatal(err)
	}
	delete(env.Files, path.Join(folder, "SKILL.md"))
	if _, _, err := deliver(ctx, env, "codex"); err != nil {
		t.Fatal("cannot restore missing managed document:", err)
	}
	if len(env.Files[path.Join(folder, "SKILL.md")]) == 0 {
		t.Fatal("document not restored")
	}
}

func TestOfficialSessionModuleSurvivesLegacyDirectoryRemoval(t *testing.T) {
	env := localEnv("local")
	home := env.P.Grok
	ctx := context.Background()
	provisionLegacyCLI(t, env, home, "grok")
	svc := serviceFor(env)
	deliver, _ := svc.officialSkillDelivery("astrorder-sessions")
	folder, _, err := deliver(ctx, env, "grok")
	if err != nil {
		t.Fatal(err)
	}
	if err := env.RemoveAll(ctx, path.Join(home, "skills/astrorder")); err != nil {
		t.Fatal(err)
	}
	env.Bins["astrorder-agent"] = path.Join(folder, "scripts/astrorder-agent")
	if _, _, err := deliver(ctx, env, "grok"); err != nil {
		t.Fatal("independent module requires old combined directory:", err)
	}
	var manifest officialManifest
	if json.Unmarshal(env.Files[path.Join(folder, officialManifestFile)], &manifest) != nil || manifest.CallerHash == "" {
		t.Fatal("missing destination binding integrity")
	}
}
