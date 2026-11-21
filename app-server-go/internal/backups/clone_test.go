package backups

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

func TestCloneBackupJobAndRestoreIndependentVersions(t *testing.T) {
	db, service, _ := openTestService(t, nil)
	ctx := context.Background()
	root := t.TempDir()
	source := filepath.Join(root, "old", "config.yaml")
	mkdirFixture(t, filepath.Dir(source))
	writeFixture(t, source, "v1")
	retention := 7
	command := "exit 0"
	original := decodeCreatedBackupJob(t, service, jobsPath, jobPayload{
		Name: "old config", ConnectionID: "local", SourcePath: source,
		DestinationDir: filepath.Join(root, "backups"), PostRestoreCommand: &command,
		ScheduleEnabled: true, ScheduleMode: ScheduleDaily, DailyTime: "07:45", RetentionCount: &retention,
	})
	v1, err := service.TriggerManualBackup(ctx, original.ID, "first")
	if err != nil || v1.Status != StatusSuccess {
		t.Fatalf("v1: %+v %v", v1, err)
	}
	// A pre-restore backup records the original target only in its manifest.
	if _, err := db.DB().Exec(`UPDATE backup_versions SET connection_id = '', source_path = '' WHERE id = ?`, v1.ID); err != nil {
		t.Fatal(err)
	}
	writeFixture(t, source, "v2")
	v2, err := service.TriggerManualBackup(ctx, original.ID, "second")
	if err != nil || v2.Status != StatusSuccess {
		t.Fatalf("v2: %+v %v", v2, err)
	}
	clonePath := jobsPrefix + original.ID + "/clone"
	remote := decodeCreatedBackupJob(t, service, clonePath, map[string]any{
		"connection_id": "ssh-debian", "source_path": "/srv/app/config.yaml", "copy_versions": true,
	})
	if remote.ID == original.ID || remote.ConnectionID != "ssh-debian" || remote.VersionCount != 2 ||
		remote.NextRunAt == nil || remote.UpdatedAt == "" || !remote.ScheduleEnabled ||
		remote.ScheduleMode != ScheduleDaily || remote.DailyTime != "07:45" || remote.RetentionCount != retention ||
		remote.PostRestoreCommand != command || remote.LastRunAt != nil {
		t.Fatalf("cloned config: %+v", remote)
	}
	remotePlan, remoteVersion, err := service.loadRestorePlan(ctx, remote.LatestVersion.ID)
	if err != nil || remotePlan.TargetPath != "/srv/app/config.yaml" || remotePlan.ConnectionID != "ssh-debian" ||
		remoteVersion.ID == v2.ID || remoteVersion.Note != "second" {
		t.Fatalf("cloned remote restore: %+v, %+v, %v", remotePlan, remoteVersion, err)
	}
	manifest, err := readRestoreManifest(remoteVersion)
	if err != nil || manifest.ConnectionID != "ssh-debian" || manifest.SourcePath != remotePlan.TargetPath {
		t.Fatalf("cloned manifest: %+v, %v", manifest, err)
	}

	newPath := filepath.Join(root, "new", "renamed.yaml")
	configOnly := decodeCreatedBackupJob(t, service, clonePath, map[string]any{"source_path": newPath})
	if configOnly.SourcePath != newPath || configOnly.VersionCount != 0 || configOnly.LatestVersion != nil {
		t.Fatalf("config-only clone: %+v", configOnly)
	}
	cloned := decodeCreatedBackupJob(t, service, clonePath, map[string]any{
		"source_path": newPath, "post_restore_command": "", "copy_versions": true,
	})
	clonedVersions, err := service.listVersionRows(ctx, cloned.ID)
	if err != nil || len(clonedVersions) != 2 || clonedVersions[1].VersionNumber != 1 ||
		clonedVersions[1].LocalPath == v1.LocalPath || clonedVersions[1].Note != "first" {
		t.Fatalf("cloned versions: %+v %v", clonedVersions, err)
	}
	if err := service.DeleteJob(ctx, original.ID, true); err != nil {
		t.Fatal(err)
	}
	assertRestoreAbsent(t, v1.LocalPath)
	plan, _, err := service.loadRestorePlan(ctx, clonedVersions[1].ID)
	if err != nil || plan.TargetPath != newPath {
		t.Fatalf("local restore plan: %+v %v", plan, err)
	}
	rec := callHTTP(t, service, http.MethodPost, versionsPrefix+clonedVersions[1].ID+"/restore",
		map[string]string{"confirmation": plan.Confirmation})
	if rec.Code != http.StatusOK {
		t.Fatalf("restore clone: %d %s", rec.Code, rec.Body)
	}
	assertRestoreFile(t, newPath, "v1")
	assertRestoreFile(t, source, "v2")
}

func TestCloneRollbackOnCorruptSnapshot(t *testing.T) {
	_, service, _ := openTestService(t, nil)
	ctx := context.Background()
	root := t.TempDir()
	source := filepath.Join(root, "config")
	destination := filepath.Join(root, "backups")
	writeFixture(t, source, "before")
	original := decodeCreatedBackupJob(t, service, jobsPath, jobPayload{
		ConnectionID: "local", SourcePath: source, DestinationDir: destination,
	})
	v1, err := service.TriggerManualBackup(ctx, original.ID, "")
	if err != nil || v1.Status != StatusSuccess {
		t.Fatalf("backup: %+v %v", v1, err)
	}
	v2, err := service.TriggerManualBackup(ctx, original.ID, "")
	if err != nil || v2.Status != StatusSuccess {
		t.Fatalf("backup: %+v %v", v2, err)
	}
	// The healthy newer version is copied before the older version fails.
	writeFixture(t, filepath.Join(v1.LocalPath, "content", "config"), "tampered")
	rec := callHTTP(t, service, http.MethodPost, jobsPrefix+original.ID+"/clone", map[string]any{"copy_versions": true})
	if rec.Code != http.StatusConflict {
		t.Fatalf("clone corrupt backup: %d %s", rec.Code, rec.Body)
	}
	jobs, err := service.listJobRows(ctx)
	if err != nil || len(jobs) != 1 || jobs[0].ID != original.ID {
		t.Fatalf("jobs: %+v %v", jobs, err)
	}
	dirs, err := os.ReadDir(destination)
	if err != nil || len(dirs) != 1 {
		t.Fatalf("cloned files remained: %+v %v", dirs, err)
	}
	assertRestoreFile(t, filepath.Join(v2.LocalPath, "content", "config"), "before")
}

func decodeCreatedBackupJob(t *testing.T, service *Service, url string, payload any) BackupJob {
	t.Helper()
	rec := callHTTP(t, service, http.MethodPost, url, payload)
	var job BackupJob
	if err := json.Unmarshal(rec.Body.Bytes(), &job); err != nil || rec.Code != http.StatusCreated {
		t.Fatalf("create backup job: %d %s %v", rec.Code, rec.Body, err)
	}
	return job
}
