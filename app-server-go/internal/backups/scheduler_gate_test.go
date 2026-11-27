package backups

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"astrorder.dev/app-server/internal/store"
	"astrorder.dev/app-server/internal/targetfs"
)

func TestBackupSchedulerSkipsWhenPluginDisabledAndResumesWhenReenabled(t *testing.T) {
	tmp := t.TempDir()
	st, err := store.Open(filepath.Join(tmp, "app.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()
	ctx := context.Background()

	srcDir := filepath.Join(tmp, "src-data")
	dstDir := filepath.Join(tmp, "dst-backups")
	if err := os.MkdirAll(srcDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "hello.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}

	nowRef := time.Date(2026, 11, 27, 12, 0, 0, 0, time.UTC)
	svc := NewService(Options{
		Store:                 st,
		Resolver:              targetfs.NewResolver(targetfs.ResolverOptions{Store: st, DefaultWorkspace: tmp}),
		DefaultWorkspace:      tmp,
		DefaultDestinationDir: dstDir,
		Now:                   func() time.Time { return nowRef },
		Location:              time.UTC,
	})

	past := formatTS(nowRef.Add(-5 * time.Minute))
	created := formatTS(nowRef.Add(-1 * time.Hour))
	job := BackupJob{
		ID:              "job-gate-test",
		Name:            "scheduled-gate-test",
		ConnectionID:    "local",
		SourcePath:      srcDir,
		DestinationDir:  dstDir,
		ScheduleEnabled: true,
		ScheduleMode:    ScheduleInterval,
		IntervalMinutes: 30,
		RetentionCount:  5,
		NextRunAt:       &past,
		CreatedAt:       created,
		UpdatedAt:       created,
	}
	if err := svc.insertJobRow(ctx, job); err != nil {
		t.Fatalf("insertJobRow: %v", err)
	}

	// 1. Disable workspace-backups -> runDueJobs MUST skip without triggering a backup!
	if err := st.SetPreference(ctx, store.PrefPluginsEnabled, map[string]any{PluginID: false}, true); err != nil {
		t.Fatalf("disable backups plugin: %v", err)
	}
	svc.runDueJobs(ctx)
	svc.Wait()
	versions, err := svc.listVersionRows(ctx, job.ID)
	if err != nil {
		t.Fatalf("listVersionRows: %v", err)
	}
	if len(versions) != 0 {
		t.Fatalf("expected 0 backup versions while %s is disabled, got %d", PluginID, len(versions))
	}

	// 2. Re-enable workspace-backups -> runDueJobs immediately picks up and executes the due job!
	if err := st.SetPreference(ctx, store.PrefPluginsEnabled, map[string]any{PluginID: true}, true); err != nil {
		t.Fatalf("re-enable backups plugin: %v", err)
	}
	svc.runDueJobs(ctx)
	svc.Wait()
	versions, err = svc.listVersionRows(ctx, job.ID)
	if err != nil {
		t.Fatalf("listVersionRows after re-enable: %v", err)
	}
	if len(versions) != 1 || versions[0].Status != StatusSuccess {
		t.Fatalf("expected 1 successful scheduled backup after re-enable, got %+v", versions)
	}
}
