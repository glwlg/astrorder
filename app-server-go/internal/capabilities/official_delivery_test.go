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

// localEnv returns an in-memory env whose paths resolve as a local machine, so
// official delivery uses filesystem path checks instead of a remote shell.
func localEnv(id string) *envtargettest.Env {
	env := newEnv(id)
	env.Info.Kind = "local"
	env.Info.ConnectionID = "local"
	return env
}

func callerBinding(t *testing.T, agent string) []byte {
	t.Helper()
	raw, err := json.Marshal(map[string]any{
		"agent_id":      agent + "-main",
		"connection_id": "local",
		"profile_name":  "default",
		"runtime":       agent,
	})
	if err != nil {
		t.Fatalf("marshal caller: %v", err)
	}
	return raw
}

// helpOutput is what a current CLI answers for the probes official delivery
// performs. A binary that only answers the bare --help is an older build.
func helpOutput(args []string) (envtarget.Result, bool) {
	switch strings.Join(args, " ") {
	case "--help":
		return envtarget.Result{Stdout: "usage: astrorder-agent [-h] {catalog,invoke,sessions,browser} ..."}, true
	case "invoke --help":
		return envtarget.Result{Stdout: "usage: astrorder-agent invoke [-h] [--input INPUT] [--input-file INPUT_FILE] capability"}, true
	case "browser eval --help":
		return envtarget.Result{Stdout: "usage: astrorder-agent browser eval [-h] --script-file SCRIPT_FILE"}, true
	}
	return envtarget.Result{}, false
}

func currentCLI(command envtarget.Command, _ *envtargettest.Env) (envtarget.Result, error) {
	if result, ok := helpOutput(command.Args); ok {
		return result, nil
	}
	return envtarget.Result{Code: 2}, nil
}

// provisionLegacyCLI leaves the destination exactly as astrorderctl
// agent-skill install would: verified caller config plus the legacy skill CLI
// and its manifest. It returns the relative binary path.
func provisionLegacyCLI(t *testing.T, env *envtargettest.Env, home, agent string) string {
	t.Helper()
	caller := callerBinding(t, agent)
	env.Files[path.Join(home, agentskill.CallerFile)] = caller

	binName := "astrorder-agent"
	if len(home) > 2 && home[1] == ':' {
		binName += ".exe"
	}
	binary := []byte("#!/bin/sh\nexit 0\n")
	relBin := "scripts/" + binName
	binaryPath := path.Join(home, "skills/astrorder", relBin)
	env.Files[binaryPath] = binary
	// Register every path a module copy may use. The test env resolves commands
	// by exact path, and each module keeps its own verified CLI copy.
	env.Bins[binaryPath] = binaryPath
	for _, skill := range officialskills.ListOfficialSkills() {
		moduleBin := path.Join(home, "skills", skill.Name, relBin)
		env.Bins[moduleBin] = moduleBin
	}
	env.Run_ = currentCLI

	manifest, err := json.Marshal(agentskill.Manifest{
		FormatVersion: agentskill.ManifestVersion,
		Owner:         agentskill.ManifestOwner,
		Runtime:       agent,
		AgentID:       agent + "-main",
		ConnectionID:  "local",
		ProfileName:   "default",
		FileHashes: map[string]string{
			agentskill.CallerFile: payloadHash(caller),
			relBin:                payloadHash(binary),
		},
	})
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}
	env.Files[path.Join(home, "skills/astrorder", agentskill.ManifestFile)] = manifest
	return relBin
}

func TestOfficialDeliveryRequiresVerifiedDestinationBinding(t *testing.T) {
	ctx := context.Background()

	t.Run("every module fails without caller binding", func(t *testing.T) {
		for _, skill := range officialskills.ListOfficialSkills() {
			env := localEnv("local")
			deliver, err := serviceFor(env).officialSkillDelivery(skill.Name)
			if err != nil {
				t.Fatalf("officialSkillDelivery(%s): %v", skill.Name, err)
			}
			_, _, err = deliver(ctx, env, "hermes")
			if err == nil {
				t.Fatalf("%s: expected a readiness error without caller binding", skill.Name)
			}
			if status, detail := statusOf(err); status != 422 || detail == "" {
				t.Errorf("%s: expected 422 with detail, got %d %q", skill.Name, status, detail)
			}
			if _, exists := env.Files[path.Join(env.P.Hermes, "skills", skill.Name, "SKILL.md")]; exists {
				t.Errorf("%s: skill must not be written when the target is not ready", skill.Name)
			}
		}
	})

	t.Run("caller runtime for another agent fails", func(t *testing.T) {
		env := localEnv("local")
		env.Files[path.Join(env.P.Hermes, agentskill.CallerFile)] = callerBinding(t, "codex")
		deliver, err := serviceFor(env).officialSkillDelivery("astrorder-sessions")
		if err != nil {
			t.Fatalf("officialSkillDelivery: %v", err)
		}
		_, _, err = deliver(ctx, env, "hermes")
		if err == nil {
			t.Fatal("expected runtime mismatch error")
		}
		if status, _ := statusOf(err); status != 422 {
			t.Errorf("expected 422, got %d (%v)", status, err)
		}
	})

	t.Run("caller without a verified CLI manifest fails", func(t *testing.T) {
		env := localEnv("local")
		env.Files[path.Join(env.P.Hermes, agentskill.CallerFile)] = callerBinding(t, "hermes")
		deliver, err := serviceFor(env).officialSkillDelivery("astrorder-sessions")
		if err != nil {
			t.Fatalf("officialSkillDelivery: %v", err)
		}
		_, _, err = deliver(ctx, env, "hermes")
		if err == nil {
			t.Fatal("expected missing CLI manifest error")
		}
		if status, _ := statusOf(err); status != 422 {
			t.Errorf("expected 422, got %d (%v)", status, err)
		}
	})

	t.Run("tampered destination CLI fails hash verification", func(t *testing.T) {
		env := localEnv("local")
		home := env.P.Hermes
		relBin := provisionLegacyCLI(t, env, home, "hermes")
		env.Files[path.Join(home, "skills/astrorder", relBin)] = []byte("tampered binary")

		deliver, err := serviceFor(env).officialSkillDelivery("astrorder-sessions")
		if err != nil {
			t.Fatalf("officialSkillDelivery: %v", err)
		}
		_, _, err = deliver(ctx, env, "hermes")
		if err == nil {
			t.Fatal("expected CLI hash verification failure")
		}
		if status, _ := statusOf(err); status != 422 {
			t.Errorf("expected 422, got %d (%v)", status, err)
		}
	})

	t.Run("verified destination receives rendered skill and its own CLI copy", func(t *testing.T) {
		env := localEnv("local")
		home := env.P.Hermes
		relBin := provisionLegacyCLI(t, env, home, "hermes")

		deliver, err := serviceFor(env).officialSkillDelivery("astrorder-sessions")
		if err != nil {
			t.Fatalf("officialSkillDelivery: %v", err)
		}
		installed, message, err := deliver(ctx, env, "hermes")
		if err != nil {
			t.Fatalf("deliver: %v", err)
		}
		if installed != path.Join(home, "skills/astrorder-sessions") || message == "" {
			t.Errorf("unexpected receipt: path=%q message=%q", installed, message)
		}

		skillMD, err := env.Read(ctx, path.Join(installed, "SKILL.md"))
		if err != nil || len(skillMD) == 0 {
			t.Fatalf("read back SKILL.md: %v", err)
		}
		if strings.Contains(string(skillMD), "{{CALLER_CONFIG_PATH}}") {
			t.Error("SKILL.md still contains an unrendered caller placeholder")
		}
		if !strings.Contains(string(skillMD), path.Join(home, agentskill.CallerFile)) {
			t.Error("SKILL.md does not reference the destination caller binding")
		}

		binary, err := env.Read(ctx, path.Join(installed, relBin))
		if err != nil {
			t.Fatalf("read back CLI copy: %v", err)
		}
		if string(binary) != string(env.Files[path.Join(home, "skills/astrorder", relBin)]) {
			t.Error("delivered CLI copy does not match the verified destination binary")
		}

		var manifest officialManifest
		if err := json.Unmarshal(env.Files[path.Join(installed, officialManifestFile)], &manifest); err != nil {
			t.Fatalf("decode manifest: %v", err)
		}
		if manifest.CallerHash != payloadHash(callerBinding(t, "hermes")) {
			t.Error("manifest is not bound to the destination caller")
		}

		// The legacy install must stay untouched.
		if _, err := env.Read(ctx, path.Join(home, "skills/astrorder", agentskill.ManifestFile)); err != nil {
			t.Errorf("legacy install changed: %v", err)
		}
	})
}

func TestOfficialDeliveryRefusesUserOwnedTargets(t *testing.T) {
	ctx := context.Background()

	t.Run("user file without an astrorder manifest is never overwritten", func(t *testing.T) {
		env := localEnv("local")
		provisionLegacyCLI(t, env, env.P.Codex, "codex")
		folder := path.Join(env.P.Codex, "skills/astrorder-browser")
		env.Files[path.Join(folder, "SKILL.md")] = []byte("user authored skill\n")

		deliver, err := serviceFor(env).officialSkillDelivery("astrorder-browser")
		if err != nil {
			t.Fatalf("officialSkillDelivery: %v", err)
		}
		_, _, err = deliver(ctx, env, "codex")
		if err == nil {
			t.Fatal("expected refusal for user-owned target")
		}
		if status, _ := statusOf(err); status != 409 {
			t.Errorf("expected 409, got %d (%v)", status, err)
		}
		if got := string(env.Files[path.Join(folder, "SKILL.md")]); got != "user authored skill\n" {
			t.Errorf("user file changed: %q", got)
		}
		if _, exists := env.Files[path.Join(folder, officialManifestFile)]; exists {
			t.Error("manifest must not be written for a refused target")
		}
	})

	t.Run("user modified managed file is detected by hash", func(t *testing.T) {
		env := localEnv("local")
		provisionLegacyCLI(t, env, env.P.Codex, "codex")
		folder := path.Join(env.P.Codex, "skills/astrorder-browser")
		deliver, err := serviceFor(env).officialSkillDelivery("astrorder-browser")
		if err != nil {
			t.Fatalf("officialSkillDelivery: %v", err)
		}
		if _, _, err := deliver(ctx, env, "codex"); err != nil {
			t.Fatalf("initial delivery: %v", err)
		}
		env.Files[path.Join(folder, "SKILL.md")] = []byte("tampered\n")

		_, _, err = deliver(ctx, env, "codex")
		if err == nil {
			t.Fatal("expected refusal for modified managed file")
		}
		if status, _ := statusOf(err); status != 409 {
			t.Errorf("expected 409, got %d (%v)", status, err)
		}
		if got := string(env.Files[path.Join(folder, "SKILL.md")]); got != "tampered\n" {
			t.Errorf("modified file changed again: %q", got)
		}
	})

	t.Run("unknown official skill name is rejected", func(t *testing.T) {
		env := localEnv("local")
		if _, err := serviceFor(env).officialSkillDelivery("astrorder-missing"); err == nil {
			t.Fatal("expected 404 for unknown official skill")
		} else if status, _ := statusOf(err); status != 404 {
			t.Errorf("expected 404, got %d (%v)", status, err)
		}
	})
}

func TestOfficialDeliveryRollsBackPartialWrites(t *testing.T) {
	ctx := context.Background()
	env := localEnv("local")
	provisionLegacyCLI(t, env, env.P.Grok, "grok")
	folder := path.Join(env.P.Grok, "skills/astrorder-browser")
	manifestPath := path.Join(folder, officialManifestFile)
	env.FailOn[manifestPath] = errors.New("disk full")

	deliver, err := serviceFor(env).officialSkillDelivery("astrorder-browser")
	if err != nil {
		t.Fatalf("officialSkillDelivery: %v", err)
	}
	if _, _, err := deliver(ctx, env, "grok"); err == nil {
		t.Fatal("expected write failure")
	}

	if _, exists := env.Files[path.Join(folder, "SKILL.md")]; exists {
		t.Error("partially written SKILL.md was not rolled back")
	}
	if _, exists := env.Files[manifestPath]; exists {
		t.Error("failed manifest must not exist")
	}
}

func TestOfficialDeliveryPreservesPreviousInstallOnRollback(t *testing.T) {
	ctx := context.Background()
	env := localEnv("local")
	provisionLegacyCLI(t, env, env.P.Grok, "grok")
	folder := path.Join(env.P.Grok, "skills/astrorder-browser")
	deliver, err := serviceFor(env).officialSkillDelivery("astrorder-browser")
	if err != nil {
		t.Fatalf("officialSkillDelivery: %v", err)
	}
	if _, _, err := deliver(ctx, env, "grok"); err != nil {
		t.Fatalf("initial delivery: %v", err)
	}
	original := append([]byte{}, env.Files[path.Join(folder, "SKILL.md")]...)

	// Force the manifest rewrite to fail during a re-delivery of the same skill.
	env.FailOn[path.Join(folder, officialManifestFile)] = errors.New("disk full")
	if _, _, err := deliver(ctx, env, "grok"); err == nil {
		t.Fatal("expected re-delivery failure")
	}

	if got := env.Files[path.Join(folder, "SKILL.md")]; string(got) != string(original) {
		t.Error("rollback did not restore the previously installed SKILL.md")
	}
}

// A document-only install from before the CLI era carries a valid manifest but
// no scripts/ entry. Upgrading must add the managed CLI instead of refusing on
// a file-count mismatch, and must still never touch user files.
func TestOfficialDeliveryUpgradesDocumentOnlyModule(t *testing.T) {
	ctx := context.Background()
	env := localEnv("local")
	home := env.P.Codex
	relBin := provisionLegacyCLI(t, env, home, "codex")

	folder := path.Join(home, "skills/astrorder-browser")
	oldDoc := []byte("---\nname: astrorder-browser\ndescription: old doc-only copy\n---\n")
	env.Files[path.Join(folder, "SKILL.md")] = oldDoc
	env.Files[path.Join(folder, "notes.md")] = []byte("user notes\n")
	oldManifest, err := json.Marshal(officialManifest{
		Owner:   officialManifestOwner,
		Version: officialManifestVersion,
		Skill:   "astrorder-browser",
		Agent:   "codex",
		Files:   map[string]string{"SKILL.md": payloadHash(oldDoc)},
	})
	if err != nil {
		t.Fatalf("marshal old manifest: %v", err)
	}
	env.Files[path.Join(folder, officialManifestFile)] = oldManifest

	deliver, err := serviceFor(env).officialSkillDelivery("astrorder-browser")
	if err != nil {
		t.Fatalf("officialSkillDelivery: %v", err)
	}
	installed, _, err := deliver(ctx, env, "codex")
	if err != nil {
		t.Fatalf("document-only upgrade failed: %v", err)
	}

	var upgraded officialManifest
	if err := json.Unmarshal(env.Files[path.Join(installed, officialManifestFile)], &upgraded); err != nil {
		t.Fatalf("decode upgraded manifest: %v", err)
	}
	if upgraded.Files["scripts/"+path.Base(relBin)] == "" {
		t.Errorf("upgraded manifest does not track the CLI: %+v", upgraded.Files)
	}
	if upgraded.CallerHash == "" {
		t.Error("upgraded manifest is not bound to the destination caller")
	}
	if _, err := env.Read(ctx, path.Join(installed, relBin)); err != nil {
		t.Errorf("CLI was not installed by the upgrade: %v", err)
	}
	if got := string(env.Files[path.Join(installed, "SKILL.md")]); got == string(oldDoc) {
		t.Error("document-only SKILL.md was not replaced")
	}
	if got := string(env.Files[path.Join(installed, "notes.md")]); got != "user notes\n" {
		t.Errorf("user file inside the module was not preserved: %q", got)
	}
}

func TestOfficialDeliveryUpgradeRefusesModifiedDocument(t *testing.T) {
	ctx := context.Background()
	env := localEnv("local")
	home := env.P.Codex
	provisionLegacyCLI(t, env, home, "codex")

	folder := path.Join(home, "skills/astrorder-browser")
	env.Files[path.Join(folder, "SKILL.md")] = []byte("user edited this\n")
	oldManifest, _ := json.Marshal(officialManifest{
		Owner:   officialManifestOwner,
		Version: officialManifestVersion,
		Skill:   "astrorder-browser",
		Agent:   "codex",
		Files:   map[string]string{"SKILL.md": payloadHash([]byte("original managed copy\n"))},
	})
	env.Files[path.Join(folder, officialManifestFile)] = oldManifest

	deliver, _ := serviceFor(env).officialSkillDelivery("astrorder-browser")
	if _, _, err := deliver(ctx, env, "codex"); err == nil {
		t.Fatal("expected refusal for a modified document during upgrade")
	} else if status, _ := statusOf(err); status != 409 {
		t.Errorf("expected 409, got %d (%v)", status, err)
	}
	if got := string(env.Files[path.Join(folder, "SKILL.md")]); got != "user edited this\n" {
		t.Errorf("user edit was overwritten: %q", got)
	}
}

// Answering --help alone does not prove the binary implements the installed
// surfaces. An older CLI must be rejected before any file is written.
func TestOfficialDeliveryRejectsCLIWithoutRequiredCommands(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name string
		run  envtargettest.RunFunc
	}{
		{
			name: "invoke without --input-file",
			run: func(command envtarget.Command, _ *envtargettest.Env) (envtarget.Result, error) {
				if strings.Join(command.Args, " ") == "invoke --help" {
					return envtarget.Result{Stdout: "usage: astrorder-agent invoke [-h] [--input INPUT] capability"}, nil
				}
				return currentCLI(command, nil)
			},
		},
		{
			name: "browser without eval --script-file",
			run: func(command envtarget.Command, _ *envtargettest.Env) (envtarget.Result, error) {
				if strings.Join(command.Args, " ") == "browser eval --help" {
					return envtarget.Result{Code: 2, Stderr: "invalid choice: 'eval'"}, nil
				}
				return currentCLI(command, nil)
			},
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			env := localEnv("local")
			provisionLegacyCLI(t, env, env.P.Hermes, "hermes")
			env.Run_ = test.run

			deliver, err := serviceFor(env).officialSkillDelivery("astrorder-blackboard")
			if err != nil {
				t.Fatalf("officialSkillDelivery: %v", err)
			}
			_, _, err = deliver(ctx, env, "hermes")
			if err == nil {
				t.Fatal("expected an old CLI to be rejected")
			}
			if status, _ := statusOf(err); status != 422 {
				t.Errorf("expected 422, got %d (%v)", status, err)
			}
			if _, exists := env.Files[path.Join(env.P.Hermes, "skills/astrorder-blackboard/SKILL.md")]; exists {
				t.Error("skill was written despite an incompatible CLI")
			}
		})
	}
}

// A CLI verified for any official module is a valid self-contained source, so
// removing the legacy combined directory must not break the other modules.
func TestOfficialDeliveryReusesCLIFromAnyInstalledModule(t *testing.T) {
	ctx := context.Background()
	env := localEnv("local")
	home := env.P.Grok
	provisionLegacyCLI(t, env, home, "grok")
	svc := serviceFor(env)

	sessionsDeliver, _ := svc.officialSkillDelivery("astrorder-sessions")
	sessionsFolder, _, err := sessionsDeliver(ctx, env, "grok")
	if err != nil {
		t.Fatalf("deliver sessions: %v", err)
	}
	if err := env.RemoveAll(ctx, path.Join(home, "skills/astrorder")); err != nil {
		t.Fatalf("remove legacy: %v", err)
	}
	env.Bins["astrorder-agent"] = path.Join(sessionsFolder, "scripts/astrorder-agent")

	blackboardDeliver, _ := svc.officialSkillDelivery("astrorder-blackboard")
	folder, _, err := blackboardDeliver(ctx, env, "grok")
	if err != nil {
		t.Fatalf("second module could not reuse the installed CLI: %v", err)
	}
	if _, err := env.Read(ctx, path.Join(folder, "scripts/astrorder-agent")); err != nil {
		t.Errorf("second module did not receive its own CLI copy: %v", err)
	}
}

// A hash-valid but functionally stale module CLI must not dead-end the upgrade
// loop: after the user upgrades the legacy CLI, a new module dispatch must skip
// the stale copy and reuse the current one.
func TestOfficialDeliverySkipsStaleModuleCLIAndUsesUpgradedLegacy(t *testing.T) {
	ctx := context.Background()
	env := localEnv("local")
	home := env.P.Hermes
	provisionLegacyCLI(t, env, home, "hermes")

	staleFolder := path.Join(home, "skills/astrorder-sessions")
	staleBinary := []byte("#!/bin/sh\necho stale\n")
	staleDoc := []byte("---\nname: astrorder-sessions\ndescription: stale copy\n---\n")
	env.Files[path.Join(staleFolder, "scripts/astrorder-agent")] = staleBinary
	env.Files[path.Join(staleFolder, "SKILL.md")] = staleDoc
	staleManifest, err := json.Marshal(officialManifest{
		CallerHash: payloadHash(callerBinding(t, "hermes")),
		Owner:      officialManifestOwner,
		Version:    officialManifestVersion,
		Skill:      "astrorder-sessions",
		Agent:      "hermes",
		Files: map[string]string{
			"SKILL.md":                payloadHash(staleDoc),
			"scripts/astrorder-agent": payloadHash(staleBinary),
		},
	})
	if err != nil {
		t.Fatalf("marshal stale manifest: %v", err)
	}
	env.Files[path.Join(staleFolder, officialManifestFile)] = staleManifest

	// The installed module still ships an older CLI; the legacy install was
	// upgraded to a current one. Model both binaries precisely.
	env.Run_ = func(command envtarget.Command, _ *envtargettest.Env) (envtarget.Result, error) {
		if strings.Contains(command.Name, "/skills/astrorder-sessions/") {
			switch strings.Join(command.Args, " ") {
			case "--help":
				return envtarget.Result{Stdout: "usage: astrorder-agent [-h] {catalog,invoke,sessions} ..."}, nil
			case "invoke --help":
				return envtarget.Result{Stdout: "usage: astrorder-agent invoke [-h] [--input INPUT] capability"}, nil
			}
			return envtarget.Result{Code: 2}, nil
		}
		return currentCLI(command, nil)
	}

	deliver, err := serviceFor(env).officialSkillDelivery("astrorder-blackboard")
	if err != nil {
		t.Fatalf("officialSkillDelivery: %v", err)
	}
	folder, _, err := deliver(ctx, env, "hermes")
	if err != nil {
		t.Fatalf("stale module CLI blocked the upgrade: %v", err)
	}
	if _, err := env.Read(ctx, path.Join(folder, "scripts/astrorder-agent")); err != nil {
		t.Errorf("new module did not receive the upgraded CLI: %v", err)
	}
	if got := env.Files[path.Join(staleFolder, "scripts/astrorder-agent")]; string(got) != string(staleBinary) {
		t.Error("stale module binary was rewritten instead of skipped")
	}
}

// A module binary that no longer matches its manifest is tampering, not
// staleness: it must abort instead of falling through to another CLI.
func TestOfficialDeliveryRejectsTamperedModuleCLI(t *testing.T) {
	ctx := context.Background()
	env := localEnv("local")
	home := env.P.Grok
	provisionLegacyCLI(t, env, home, "grok")
	svc := serviceFor(env)

	deliverSessions, _ := svc.officialSkillDelivery("astrorder-sessions")
	sessionsFolder, _, err := deliverSessions(ctx, env, "grok")
	if err != nil {
		t.Fatalf("deliver sessions: %v", err)
	}
	env.Files[path.Join(sessionsFolder, "scripts/astrorder-agent")] = []byte("tampered")

	deliver, _ := svc.officialSkillDelivery("astrorder-blackboard")
	if _, _, err := deliver(ctx, env, "grok"); err == nil {
		t.Fatal("expected tampered module CLI to be rejected")
	} else if status, _ := statusOf(err); status != 422 {
		t.Errorf("expected 422, got %d (%v)", status, err)
	}
	if _, exists := env.Files[path.Join(home, "skills/astrorder-blackboard/SKILL.md")]; exists {
		t.Error("skill was written despite a tampered CLI")
	}
}

// A managed astrorder copy carries the destination's caller binding and CLI.
// Treating it as an ordinary skill source would leak those into another agent.
func TestOrdinarySkillDispatchRefusesManagedAstrorderCopies(t *testing.T) {
	ctx := context.Background()

	t.Run("skill directory with an astrorder manifest is not copied", func(t *testing.T) {
		env := localEnv("local")
		dir := path.Join(env.P.Codex, "skills/custom-tools")
		env.Files[path.Join(dir, "SKILL.md")] = []byte("---\nname: custom-tools\ndescription: local helper\n---\n")
		env.Files[path.Join(dir, "astrorder-manifest.json")] = []byte(`{"owner":"astrorderctl"}`)
		env.Packed[dir] = []byte("archive")

		_, err := serviceFor(env).Dispatch(ctx, "skill", "local", "codex", "custom-tools",
			[]DispatchTarget{{TargetID: "local", Agent: "hermes"}})
		if err == nil {
			t.Fatal("expected managed copy to be refused")
		}
		if status, _ := statusOf(err); status != 422 {
			t.Errorf("expected 422, got %d (%v)", status, err)
		}
	})

	t.Run("legacy astrorder skill name is routed to the official catalog", func(t *testing.T) {
		env := localEnv("local")
		dir := path.Join(env.P.Codex, "skills/astrorder")
		env.Files[path.Join(dir, "SKILL.md")] = []byte("---\nname: astrorder\n---\n")
		env.Packed[dir] = []byte("archive")

		_, err := serviceFor(env).Dispatch(ctx, "skill", "local", "codex", "astrorder",
			[]DispatchTarget{{TargetID: "local", Agent: "hermes"}})
		if err == nil {
			t.Fatal("expected legacy skill copy to be refused")
		}
		if status, _ := statusOf(err); status != 422 {
			t.Errorf("expected 422, got %d (%v)", status, err)
		}
	})
}
