package backups

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

type restoreAgentFunc func(context.Context, string, string) error

func (f restoreAgentFunc) RestartEnvironmentAgent(ctx context.Context, environmentID, kind string) error {
	return f(ctx, environmentID, kind)
}

func TestRestartAgentRestorePreset(t *testing.T) {
	_, service, _ := openTestService(t, nil)
	ctx := context.Background()
	root := t.TempDir()
	source := filepath.Join(root, "settings")
	writeFixture(t, source, "backup content")
	var restarted []RestoreAgentTarget
	service.agentRestarter = restoreAgentFunc(func(_ context.Context, environmentID, kind string) error {
		assertRestoreFile(t, source, "backup content")
		restarted = append(restarted, RestoreAgentTarget{EnvironmentID: environmentID, Kind: kind})
		return errors.New("runtime refused the restart")
	})

	payload := jobPayload{
		Name: "restart preset", ConnectionID: "local", SourcePath: source,
		DestinationDir:          filepath.Join(root, "versions"),
		PostRestoreRestartAgent: json.RawMessage(`{"environment_id":"missing","kind":"codex"}`),
	}
	if res := callHTTP(t, service, http.MethodPost, jobsPath, payload); res.Code != http.StatusNotFound {
		t.Fatalf("unknown environment: %d %s", res.Code, res.Body)
	}
	payload.PostRestoreRestartAgent = json.RawMessage(`{"environment_id":"ssh-debian","kind":"codex"}`)
	created := callHTTP(t, service, http.MethodPost, jobsPath, payload)
	var saved BackupJob
	if err := json.Unmarshal(created.Body.Bytes(), &saved); err != nil || created.Code != http.StatusCreated {
		t.Fatalf("create preset: %d %s, %v", created.Code, created.Body, err)
	}
	version, err := service.TriggerManualBackup(ctx, saved.ID, "")
	if err != nil || version.Status != StatusSuccess || len(restarted) != 0 {
		t.Fatalf("backup: %+v, restarts=%v, err=%v", version, restarted, err)
	}
	plan, _, err := service.loadRestorePlan(ctx, version.ID)
	want := RestoreAgentTarget{EnvironmentID: "ssh-debian", Kind: "codex"}
	if err != nil || plan.PostRestoreRestartAgent == nil || *plan.PostRestoreRestartAgent != want ||
		plan.RestoreAgentEnvironmentName != "DebianProd" || plan.PostRestoreCommand != "" {
		t.Fatalf("plan: %+v, err=%v", plan, err)
	}

	writeFixture(t, source, "live content")
	snapshotPath := filepath.Join(version.LocalPath, "content", filepath.Base(source))
	writeFixture(t, snapshotPath, "tampered")
	if _, err := service.restoreVersion(ctx, version.ID, plan.Confirmation); err == nil || len(restarted) != 0 {
		t.Fatalf("tampered snapshot: restarts=%v, err=%v", restarted, err)
	}
	writeFixture(t, snapshotPath, "backup content")
	result, err := service.restoreVersion(ctx, version.ID, plan.Confirmation)
	if err != nil || result.HookStatus != "failed" || !strings.Contains(result.HookError, "runtime refused") ||
		len(restarted) != 1 || restarted[0] != want {
		t.Fatalf("restore: %+v, restarts=%v, err=%v", result, restarted, err)
	}
	assertRestoreFile(t, source, "backup content")

	payload.PostRestoreRestartAgent = json.RawMessage("null")
	if res := callHTTP(t, service, http.MethodPut, jobsPrefix+saved.ID, payload); res.Code != http.StatusOK {
		t.Fatalf("clear preset: %d %s", res.Code, res.Body)
	}
	if _, err := service.restoreVersion(ctx, version.ID, plan.Confirmation); err == nil {
		t.Fatal("restore accepted a stale restart confirmation")
	}
	plan, _, err = service.loadRestorePlan(ctx, version.ID)
	if err != nil || plan.PostRestoreRestartAgent != nil {
		t.Fatalf("cleared plan: %+v, %v", plan, err)
	}
}

func TestRestoreHistoricalDirectoryAndHook(t *testing.T) {
	st, service, _ := openTestService(t, nil)
	ctx := context.Background()
	root := t.TempDir()
	source := filepath.Join(root, "original", "project")
	mkdirFixture(t, filepath.Join(source, "empty"))
	writeFixture(t, filepath.Join(source, "config"), "version 1")
	hook := `test -f config && printf x >> hook-runs`
	if runtime.GOOS == "windows" {
		hook = `if (!(Test-Path config)) { exit 4 }; [IO.File]::AppendAllText((Join-Path (Get-Location) 'hook-runs'), 'x')`
	}
	job := jobPayload{
		Name: "restore directory", ConnectionID: "local", SourcePath: source,
		DestinationDir: filepath.Join(root, "versions"), PostRestoreCommand: &hook,
	}
	created := callHTTP(t, service, http.MethodPost, jobsPath, job)
	if created.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", created.Code, created.Body)
	}
	var saved BackupJob
	if err := json.Unmarshal(created.Body.Bytes(), &saved); err != nil || saved.PostRestoreCommand != hook {
		t.Fatalf("saved hook=%q, err=%v", saved.PostRestoreCommand, err)
	}
	version, err := service.TriggerManualBackup(ctx, saved.ID, "")
	if err != nil || version.Status != StatusSuccess {
		t.Fatalf("backup: %+v, %v", version, err)
	}
	assertRestoreAbsent(t, filepath.Join(source, "hook-runs"))
	// Simulate a version created with the old schema and a later job edit.
	if _, err := st.DB().ExecContext(ctx, `UPDATE backup_versions SET connection_id = '', source_path = '' WHERE id = ?`, version.ID); err != nil {
		t.Fatal(err)
	}
	job.SourcePath = filepath.Join(root, "new-project")
	job.PostRestoreCommand = nil
	if res := callHTTP(t, service, http.MethodPut, jobsPrefix+saved.ID, job); res.Code != http.StatusOK {
		t.Fatalf("edit: %d %s", res.Code, res.Body)
	}
	if err := os.RemoveAll(filepath.Dir(source)); err != nil {
		t.Fatal(err)
	}
	endpoint := versionsPrefix + version.ID + "/restore"
	preview := callHTTP(t, service, http.MethodGet, endpoint, nil)
	var plan restorePlan
	if err := json.Unmarshal(preview.Body.Bytes(), &plan); err != nil || preview.Code != http.StatusOK ||
		plan.TargetPath != source || plan.PostRestoreCommand != hook {
		t.Fatalf("preview: %d %s, %v", preview.Code, preview.Body, err)
	}
	assertRestoreAbsent(t, source)
	changedHook := "exit 7"
	job.PostRestoreCommand = &changedHook
	if res := callHTTP(t, service, http.MethodPut, jobsPrefix+saved.ID, job); res.Code != http.StatusOK {
		t.Fatalf("change hook: %d %s", res.Code, res.Body)
	}
	if res := callHTTP(t, service, http.MethodPost, endpoint, plan); res.Code != http.StatusConflict {
		t.Fatalf("stale confirmation: %d %s", res.Code, res.Body)
	}
	job.PostRestoreCommand = &hook
	if res := callHTTP(t, service, http.MethodPut, jobsPrefix+saved.ID, job); res.Code != http.StatusOK {
		t.Fatalf("reset hook: %d %s", res.Code, res.Body)
	}
	assertRestoreAbsent(t, source)
	if !service.acquireJobRun(saved.ID, "test") {
		t.Fatal("expected to acquire the job lock")
	}
	if res := callHTTP(t, service, http.MethodPost, endpoint, plan); res.Code != http.StatusConflict {
		t.Fatalf("concurrent restore: %d %s", res.Code, res.Body)
	}
	service.releaseJobRun(saved.ID)
	res := callHTTP(t, service, http.MethodPost, endpoint, plan)
	var result restoreResult
	if err := json.Unmarshal(res.Body.Bytes(), &result); err != nil || res.Code != http.StatusOK ||
		result.FileCount != 1 || result.HookStatus != "success" {
		t.Fatalf("restore: %d %s, %v", res.Code, res.Body, err)
	}
	assertRestoreFile(t, filepath.Join(source, "config"), "version 1")
	assertRestoreFile(t, filepath.Join(source, "hook-runs"), "x")
	if info, err := os.Stat(filepath.Join(source, "empty")); err != nil || !info.IsDir() {
		t.Fatalf("empty directory not restored: %v", err)
	}
	assertRestoreAbsent(t, job.SourcePath)
}

func TestRestoreChecksContentAndPreservesExistingFilesOnHookFailure(t *testing.T) {
	_, service, _ := openTestService(t, nil)
	ctx := context.Background()
	root := t.TempDir()
	source := filepath.Join(root, "source")
	mkdirFixture(t, source)
	config := filepath.Join(source, "config")
	writeFixture(t, config, "snapshot")
	hook := `printf ran > hook-runs` + "\nexit 7"
	if runtime.GOOS == "windows" {
		hook = `[IO.File]::WriteAllText((Join-Path (Get-Location) 'hook-runs'), 'ran')` + "\nexit 7"
	}
	job := BackupJob{
		ID: newID("bkj-"), Name: "restore file", ConnectionID: "local", SourcePath: config,
		DestinationDir: filepath.Join(root, "versions"), PostRestoreCommand: hook, ScheduleMode: ScheduleManual,
		CreatedAt: formatTS(service.now()), UpdatedAt: formatTS(service.now()),
	}
	if err := service.insertJobRow(ctx, job); err != nil {
		t.Fatal(err)
	}
	version, err := service.TriggerManualBackup(ctx, job.ID, "")
	if err != nil || version.Status != StatusSuccess {
		t.Fatalf("backup: %+v, %v", version, err)
	}
	snapshotFile := filepath.Join(version.LocalPath, "content", "config")
	plan, _, err := service.loadRestorePlan(ctx, version.ID)
	if err != nil {
		t.Fatal(err)
	}
	writeFixture(t, config, "live")
	writeFixture(t, filepath.Join(source, "keep"), "keep")
	writeFixture(t, snapshotFile, "modified")
	if _, err := service.restoreVersion(ctx, version.ID, plan.Confirmation); err == nil {
		t.Fatal("modified snapshot was accepted")
	}
	assertRestoreFile(t, config, "live")
	assertRestoreAbsent(t, filepath.Join(source, "hook-runs"))
	writeFixture(t, snapshotFile, "snapshot")
	result, err := service.restoreVersion(ctx, version.ID, plan.Confirmation)
	if err != nil || result.HookStatus != "failed" || !strings.Contains(result.HookError, "7") {
		t.Fatalf("restore: %+v, %v", result, err)
	}
	assertRestoreFile(t, config, "snapshot")
	assertRestoreFile(t, filepath.Join(source, "keep"), "keep")
	assertRestoreFile(t, filepath.Join(source, "hook-runs"), "ran")
	if runtime.GOOS != "windows" {
		outside := filepath.Join(root, "outside")
		writeFixture(t, outside, "untouched")
		if err := os.Remove(config); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(outside, config); err != nil {
			t.Fatal(err)
		}
		if _, err := service.restoreVersion(ctx, version.ID, plan.Confirmation); err == nil {
			t.Fatal("symbolic link destination was accepted")
		}
		assertRestoreFile(t, outside, "untouched")
	}
}

func mkdirFixture(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
}

func writeFixture(t *testing.T, file, value string) {
	t.Helper()
	if err := os.WriteFile(file, []byte(value), 0o600); err != nil {
		t.Fatal(err)
	}
}

func assertRestoreAbsent(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatalf("expected %q to be absent, got %v", path, err)
	}
}

func assertRestoreFile(t *testing.T, path, want string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil || string(data) != want {
		t.Fatalf("expected %q to contain %q, got %q, %v", path, want, data, err)
	}
}
