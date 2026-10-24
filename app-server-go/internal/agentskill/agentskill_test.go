package agentskill

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

var (
	cachedTestCLIPath string
	buildTestCLIOnce  sync.Once
	buildTestCLIErr   error
)

func getTestCLI(t *testing.T) string {
	t.Helper()
	buildTestCLIOnce.Do(func() {
		tmpDir, err := os.MkdirTemp("", "agentskill-test-cli-*")
		if err != nil {
			buildTestCLIErr = err
			return
		}
		src := filepath.Join(tmpDir, "main.go")
		if err := os.WriteFile(src, []byte("package main\nfunc main() {}\n"), 0644); err != nil {
			buildTestCLIErr = err
			return
		}
		exe := filepath.Join(tmpDir, "astrorder-agent-fixture.exe")
		cmd := exec.Command("go", "build", "-o", exe, src)
		if err := cmd.Run(); err != nil {
			buildTestCLIErr = err
			return
		}
		cachedTestCLIPath = filepath.Clean(exe)
	})
	if buildTestCLIErr != nil {
		t.Fatalf("build test CLI fixture: %v", buildTestCLIErr)
	}
	return cachedTestCLIPath
}

func TestFreshInstall_Hermes(t *testing.T) {
	home := t.TempDir()
	cli := getTestCLI(t)

	res, err := Install(InstallOptions{
		Runtime:      "hermes",
		Home:         home,
		CLI:          cli,
		AgentID:      "hermes",
		ConnectionID: "local",
		ProfileName:  "default",
	})
	if err != nil {
		t.Fatalf("expected successful install, got: %v", err)
	}
	if !res.OK {
		t.Fatalf("expected res.OK == true")
	}

	// Verify caller config
	callerBytes, err := os.ReadFile(res.CallerConfig)
	if err != nil {
		t.Fatalf("read caller config: %v", err)
	}
	var rawCaller map[string]any
	if err := json.Unmarshal(callerBytes, &rawCaller); err != nil {
		t.Fatalf("unmarshal caller config: %v", err)
	}
	if len(rawCaller) != 4 {
		t.Fatalf("caller config must have exactly 4 keys, got %d: %v", len(rawCaller), rawCaller)
	}
	if _, ok := rawCaller["session_id"]; ok {
		t.Fatalf("caller config must never have session_id")
	}
	if rawCaller["agent_id"] != "hermes" || rawCaller["runtime"] != "hermes" {
		t.Fatalf("unexpected caller config content: %v", rawCaller)
	}

	// Verify SKILL.md
	skillBytes, err := os.ReadFile(res.SkillFile)
	if err != nil {
		t.Fatalf("read skill file: %v", err)
	}
	skillContent := string(skillBytes)
	if !strings.Contains(skillContent, "description: Use when delegating tasks or reading Astrorder sessions.") {
		t.Fatalf("missing expected description in SKILL.md")
	}

	// Verify manifest
	manifestBytes, err := os.ReadFile(res.ManifestFile)
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	var m Manifest
	if err := json.Unmarshal(manifestBytes, &m); err != nil {
		t.Fatalf("unmarshal manifest: %v", err)
	}
	if m.Owner != ManifestOwner || m.FormatVersion != ManifestVersion {
		t.Fatalf("unexpected manifest header: %+v", m)
	}
	if len(m.FileHashes) != 3 {
		t.Fatalf("expected 3 file hashes in manifest, got %d: %+v", len(m.FileHashes), m.FileHashes)
	}
}

func TestFreshInstall_Codex(t *testing.T) {
	home := t.TempDir()
	cli := getTestCLI(t)

	res, err := Install(InstallOptions{
		Runtime:      "codex",
		Home:         home,
		CLI:          cli,
		AgentID:      "codex",
		ConnectionID: "local",
		ProfileName:  "default",
	})
	if err != nil {
		t.Fatalf("expected successful codex install, got: %v", err)
	}

	skillBytes, err := os.ReadFile(res.SkillFile)
	if err != nil {
		t.Fatalf("read skill: %v", err)
	}
	skillContent := string(skillBytes)
	expectedBash := filepath.ToSlash(filepath.Join(home, CallerFile))
	if !strings.Contains(skillContent, expectedBash) {
		t.Fatalf("expected skill to contain rendered caller config bash path %q", expectedBash)
	}
}

func TestIdempotentInstall(t *testing.T) {
	home := t.TempDir()
	cli := getTestCLI(t)

	opts := InstallOptions{
		Runtime:      "hermes",
		Home:         home,
		CLI:          cli,
		AgentID:      "hermes",
		ConnectionID: "local",
		ProfileName:  "default",
	}

	res1, err := Install(opts)
	if err != nil {
		t.Fatalf("first install: %v", err)
	}

	res2, err := Install(opts)
	if err != nil {
		t.Fatalf("idempotent install failed: %v", err)
	}
	if res2.SkillFile != res1.SkillFile {
		t.Fatalf("path mismatch on idempotent install")
	}
}

func TestUserOwnedSkillConflict(t *testing.T) {
	home := t.TempDir()
	cli := getTestCLI(t)

	// Pre-create user-owned SKILL.md without manifest
	skillDir := filepath.Join(home, "skills", "astrorder")
	if err := os.MkdirAll(skillDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, SkillMDFile), []byte("user custom skill"), 0644); err != nil {
		t.Fatalf("write user skill: %v", err)
	}

	_, err := Install(InstallOptions{
		Runtime:      "hermes",
		Home:         home,
		CLI:          cli,
		AgentID:      "hermes",
		ConnectionID: "local",
		ProfileName:  "default",
	})
	if err == nil || !strings.Contains(err.Error(), "user-owned") {
		t.Fatalf("expected user-owned skill conflict error, got: %v", err)
	}
}

func TestUserOwnedBinaryConflict(t *testing.T) {
	home := t.TempDir()
	cli := getTestCLI(t)

	scriptsDir := filepath.Join(home, "skills", "astrorder", "scripts")
	if err := os.MkdirAll(scriptsDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	binName := targetBinaryName(cli)
	if err := os.WriteFile(filepath.Join(scriptsDir, binName), []byte("user binary"), 0755); err != nil {
		t.Fatalf("write user binary: %v", err)
	}

	_, err := Install(InstallOptions{
		Runtime:      "hermes",
		Home:         home,
		CLI:          cli,
		AgentID:      "hermes",
		ConnectionID: "local",
		ProfileName:  "default",
	})
	if err == nil || !strings.Contains(err.Error(), "user-owned") {
		t.Fatalf("expected user-owned binary conflict error, got: %v", err)
	}
}

func TestUserModifiedSkillConflict(t *testing.T) {
	home := t.TempDir()
	cli := getTestCLI(t)

	opts := InstallOptions{
		Runtime:      "hermes",
		Home:         home,
		CLI:          cli,
		AgentID:      "hermes",
		ConnectionID: "local",
		ProfileName:  "default",
	}
	res, err := Install(opts)
	if err != nil {
		t.Fatalf("initial install: %v", err)
	}

	// User modifies SKILL.md
	if err := os.WriteFile(res.SkillFile, []byte("tampered content"), 0644); err != nil {
		t.Fatalf("tamper skill: %v", err)
	}

	_, err = Install(opts)
	if err == nil || !strings.Contains(err.Error(), "modified by user") {
		t.Fatalf("expected user modification conflict error, got: %v", err)
	}
}

func TestUserModifiedBinaryConflict(t *testing.T) {
	home := t.TempDir()
	cli := getTestCLI(t)

	opts := InstallOptions{
		Runtime:      "hermes",
		Home:         home,
		CLI:          cli,
		AgentID:      "hermes",
		ConnectionID: "local",
		ProfileName:  "default",
	}
	res, err := Install(opts)
	if err != nil {
		t.Fatalf("initial install: %v", err)
	}

	// User modifies binary
	if err := os.WriteFile(res.BinaryFile, []byte("tampered binary"), 0755); err != nil {
		t.Fatalf("tamper binary: %v", err)
	}

	_, err = Install(opts)
	if err == nil || !strings.Contains(err.Error(), "modified by user") {
		t.Fatalf("expected user modification conflict error on binary, got: %v", err)
	}
}

func TestBindingConflict(t *testing.T) {
	home := t.TempDir()
	cli := getTestCLI(t)

	_, err := Install(InstallOptions{
		Runtime:      "hermes",
		Home:         home,
		CLI:          cli,
		AgentID:      "hermes-1",
		ConnectionID: "local",
		ProfileName:  "default",
	})
	if err != nil {
		t.Fatalf("initial install: %v", err)
	}

	// Try installing with different agent ID
	_, err = Install(InstallOptions{
		Runtime:      "hermes",
		Home:         home,
		CLI:          cli,
		AgentID:      "hermes-2",
		ConnectionID: "local",
		ProfileName:  "default",
	})
	if err == nil || !strings.Contains(err.Error(), "binding conflict") {
		t.Fatalf("expected binding conflict error, got: %v", err)
	}
}

func TestCLIPathConflict(t *testing.T) {
	home := t.TempDir()
	cli1 := getTestCLI(t)

	// Create a second real copy of CLI in a different location
	dir2 := t.TempDir()
	cli2 := filepath.Join(dir2, filepath.Base(cli1))
	data, err := os.ReadFile(cli1)
	if err != nil {
		t.Fatalf("read cli: %v", err)
	}
	if err := os.WriteFile(cli2, data, 0755); err != nil {
		t.Fatalf("write cli copy: %v", err)
	}

	_, err = Install(InstallOptions{
		Runtime:      "hermes",
		Home:         home,
		CLI:          cli1,
		AgentID:      "hermes",
		ConnectionID: "local",
		ProfileName:  "default",
	})
	if err != nil {
		t.Fatalf("initial install: %v", err)
	}

	// Install with different CLI path
	_, err = Install(InstallOptions{
		Runtime:      "hermes",
		Home:         home,
		CLI:          cli2,
		AgentID:      "hermes",
		ConnectionID: "local",
		ProfileName:  "default",
	})
	if err == nil || !strings.Contains(err.Error(), "cli path conflict") {
		t.Fatalf("expected cli path conflict error, got: %v", err)
	}
}

func TestMissingBinary(t *testing.T) {
	home := t.TempDir()
	nonExistent := filepath.Join(home, "does-not-exist.exe")

	_, err := Install(InstallOptions{
		Runtime:      "hermes",
		Home:         home,
		CLI:          nonExistent,
		AgentID:      "hermes",
		ConnectionID: "local",
		ProfileName:  "default",
	})
	if err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Fatalf("expected does not exist error, got: %v", err)
	}
}

func TestNonExecutableBinary(t *testing.T) {
	home := t.TempDir()
	// Pass a fake text file as exe
	fakeCLI := filepath.Join(home, "fake-agent.exe")
	if err := os.WriteFile(fakeCLI, []byte("plain text not executable"), 0644); err != nil {
		t.Fatalf("write fake cli: %v", err)
	}

	_, err := Install(InstallOptions{
		Runtime:      "hermes",
		Home:         home,
		CLI:          fakeCLI,
		AgentID:      "hermes",
		ConnectionID: "local",
		ProfileName:  "default",
	})
	if err == nil || !strings.Contains(err.Error(), "--help execution check") {
		t.Fatalf("expected execution check failure on fake executable, got: %v", err)
	}
}

func TestInvalidInputs(t *testing.T) {
	home := t.TempDir()
	cli := getTestCLI(t)

	tests := []struct {
		name    string
		opts    InstallOptions
		wantErr string
	}{
		{
			name: "invalid runtime",
			opts: InstallOptions{
				Runtime:      "unsupported",
				Home:         home,
				CLI:          cli,
				AgentID:      "a",
				ConnectionID: "c",
				ProfileName:  "p",
			},
			wantErr: "must be hermes, codex or grok",
		},
		{
			name: "relative home",
			opts: InstallOptions{
				Runtime:      "hermes",
				Home:         "relative/path",
				CLI:          cli,
				AgentID:      "a",
				ConnectionID: "c",
				ProfileName:  "p",
			},
			wantErr: "must be absolute",
		},
		{
			name: "relative cli",
			opts: InstallOptions{
				Runtime:      "hermes",
				Home:         home,
				CLI:          "relative/agent.exe",
				AgentID:      "a",
				ConnectionID: "c",
				ProfileName:  "p",
			},
			wantErr: "must be absolute",
		},
		{
			name: "agent-id with whitespace",
			opts: InstallOptions{
				Runtime:      "hermes",
				Home:         home,
				CLI:          cli,
				AgentID:      "my agent",
				ConnectionID: "c",
				ProfileName:  "p",
			},
			wantErr: "whitespace",
		},
		{
			name: "connection-id with whitespace",
			opts: InstallOptions{
				Runtime:      "hermes",
				Home:         home,
				CLI:          cli,
				AgentID:      "a",
				ConnectionID: "conn id",
				ProfileName:  "p",
			},
			wantErr: "whitespace",
		},
		{
			name: "profile-name with whitespace",
			opts: InstallOptions{
				Runtime:      "hermes",
				Home:         home,
				CLI:          cli,
				AgentID:      "a",
				ConnectionID: "c",
				ProfileName:  "prof name",
			},
			wantErr: "whitespace",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Install(tc.opts)
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("expected error containing %q, got: %v", tc.wantErr, err)
			}
		})
	}
}

func TestExistingIdenticalCallerConfigReuse(t *testing.T) {
	home := t.TempDir()
	cli := getTestCLI(t)

	// Pre-create identical caller config
	callerPath := filepath.Join(home, CallerFile)
	identical := callerConfigFile{
		AgentID:      "hermes",
		ConnectionID: "local",
		ProfileName:  "default",
		Runtime:      "hermes",
	}
	bytes, err := json.MarshalIndent(identical, "", "  ")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(callerPath, bytes, 0644); err != nil {
		t.Fatalf("write caller: %v", err)
	}

	res, err := Install(InstallOptions{
		Runtime:      "hermes",
		Home:         home,
		CLI:          cli,
		AgentID:      "hermes",
		ConnectionID: "local",
		ProfileName:  "default",
	})
	if err != nil {
		t.Fatalf("expected identical caller config to be reused, got: %v", err)
	}
	if !res.OK {
		t.Fatalf("expected res.OK == true")
	}
}

func TestRollback_ManifestIsDirectory_NewFilesRemoved(t *testing.T) {
	home := t.TempDir()
	cli := getTestCLI(t)

	// Pre-create manifest path as a directory so atomic rename fails at final step
	manifestDir := filepath.Join(home, "skills", "astrorder", ManifestFile)
	if err := os.MkdirAll(manifestDir, 0755); err != nil {
		t.Fatalf("mkdir manifest: %v", err)
	}

	_, err := Install(InstallOptions{
		Runtime:      "hermes",
		Home:         home,
		CLI:          cli,
		AgentID:      "hermes",
		ConnectionID: "local",
		ProfileName:  "default",
	})
	if err == nil {
		t.Fatalf("expected install to fail when manifest target is a directory")
	}

	// Verify rollback: caller, skill, binary must not exist
	callerPath := filepath.Join(home, CallerFile)
	if fileExists(callerPath) {
		t.Errorf("caller config %s should have been removed on rollback", callerPath)
	}
	skillPath := filepath.Join(home, "skills", "astrorder", SkillMDFile)
	if fileExists(skillPath) {
		t.Errorf("skill file %s should have been removed on rollback", skillPath)
	}
	binName := targetBinaryName(cli)
	binPath := filepath.Join(home, "skills", "astrorder", "scripts", binName)
	if fileExists(binPath) {
		t.Errorf("binary file %s should have been removed on rollback", binPath)
	}
}

func TestRollback_ManifestIsDirectory_PreExistingCallerRestored(t *testing.T) {
	home := t.TempDir()
	cli := getTestCLI(t)

	// Pre-create an existing caller config with custom whitespace/bytes
	callerPath := filepath.Join(home, CallerFile)
	originalCallerBytes := []byte("{\n  \"agent_id\": \"hermes\",\n  \"connection_id\": \"local\",\n  \"profile_name\": \"default\",\n  \"runtime\": \"hermes\"\n}\n")
	if err := os.WriteFile(callerPath, originalCallerBytes, 0644); err != nil {
		t.Fatalf("write original caller: %v", err)
	}

	// Pre-create manifest path as a directory
	manifestDir := filepath.Join(home, "skills", "astrorder", ManifestFile)
	if err := os.MkdirAll(manifestDir, 0755); err != nil {
		t.Fatalf("mkdir manifest: %v", err)
	}

	_, err := Install(InstallOptions{
		Runtime:      "hermes",
		Home:         home,
		CLI:          cli,
		AgentID:      "hermes",
		ConnectionID: "local",
		ProfileName:  "default",
	})
	if err == nil {
		t.Fatalf("expected install to fail when manifest target is a directory")
	}

	// Caller config existed before, so it MUST be restored byte-for-byte!
	restoredBytes, err := os.ReadFile(callerPath)
	if err != nil {
		t.Fatalf("read restored caller config: %v", err)
	}
	if !bytes.Equal(restoredBytes, originalCallerBytes) {
		t.Fatalf("restored caller config bytes do not match original bytes!\nexpected: %q\ngot: %q", string(originalCallerBytes), string(restoredBytes))
	}

	// And newly written skill and binary must NOT exist
	skillPath := filepath.Join(home, "skills", "astrorder", SkillMDFile)
	if fileExists(skillPath) {
		t.Errorf("skill file %s should have been removed on rollback", skillPath)
	}
	binName := targetBinaryName(cli)
	binPath := filepath.Join(home, "skills", "astrorder", "scripts", binName)
	if fileExists(binPath) {
		t.Errorf("binary file %s should have been removed on rollback", binPath)
	}
}

func TestAtomicWriteFile_PreservesOldTargetOnFailure(t *testing.T) {
	dir := t.TempDir()
	targetDir := filepath.Join(dir, "target-as-dir")
	if err := os.MkdirAll(targetDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	// Calling atomicWriteFile targeting an existing directory fails because rename fails
	err := atomicWriteFile(targetDir, []byte("new data"), 0644)
	if err == nil {
		t.Fatalf("expected atomicWriteFile to fail on directory target")
	}

	// Directory still exists and is not destroyed
	stat, statErr := os.Stat(targetDir)
	if statErr != nil || !stat.IsDir() {
		t.Fatalf("target directory was deleted or modified: %v", statErr)
	}
}
