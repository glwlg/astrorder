package backups

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"astrorder.dev/app-server/internal/store"
	"astrorder.dev/app-server/internal/targetfs"
)

func TestBackupHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_BACKUP_HELPER") != "1" {
		return
	}
	if err := targetfs.ServeHelper(context.Background(), os.Stdin, os.Stdout); err != nil {
		os.Exit(1)
	}
	os.Exit(0)
}

type recordedOpen struct {
	path   string
	action string
}

type fakeOpener struct {
	mu    sync.Mutex
	calls []recordedOpen
}

func (f *fakeOpener) Open(_ context.Context, path, action string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, recordedOpen{path: path, action: action})
	return nil
}

func TestValidatePathsAndSecurityBoundaries(t *testing.T) {
	validCases := []struct {
		conn string
		raw  string
	}{
		{"local", "/srv/projects/demo"},
		{"local", `C:\Users\luwei\Documents\report.pdf`},
		{"local", `D:\Backups\astrorder`},
		{"local", `\\nas\share\projects\alpha`},
		{"ssh-debian", "/home/luwei/projects/demo"},
		{"wsl:Ubuntu", "/var/backups/data.tar.gz"},
		{"local", `C:\Users\luwei\.gemini\antigravity-oauth-token`},
		{"ssh-debian", "/home/luwei/.ssh/id_rsa"},
		{"wsl:Ubuntu", "/home/luwei/.aws"},
		{"ssh-debian", "/srv/app/my-oauth-token.json"},
	}
	for _, tc := range validCases {
		if tc.conn == "local" && strings.HasPrefix(tc.raw, "/") && filepath.Separator == '\\' {
			continue
		}
		if _, err := ValidateSourcePath(tc.conn, tc.raw); err != nil {
			t.Fatalf("expected valid path %q (%s), got err=%v", tc.raw, tc.conn, err)
		}
	}

	invalidCases := []struct {
		conn   string
		raw    string
		status int
	}{
		{"local", "", 422},
		{"local", "relative/dir", 422},
		{"local", "/srv/app/../secret", 422},
		{"local", "/srv/app\x00bad", 422},
		{"ssh-1", "home/luwei/project", 422},
		{"ssh-1", `/srv\app`, 422},
		{"ssh-1", "/", 422},
		{"ssh-1", "/etc", 422},
		{"ssh-1", "/proc/1/environ", 422},
		{"ssh-1", "/home", 422},
		{"ssh-1", "/home/luwei", 422},
		{"local", "/Users/alice", 422},
		{"local", `C:\`, 422},
		{"local", `C:\Windows`, 422},
		{"local", `C:\Users`, 422},
		{"local", `C:\Users\luwei`, 422},
		{"local", `\\nas\share`, 422},
	}
	for _, tc := range invalidCases {
		_, err := ValidateSourcePath(tc.conn, tc.raw)
		if err == nil {
			t.Fatalf("expected error for (%s, %q)", tc.conn, tc.raw)
		}
		var vErr *ValidationError
		if !errors.As(err, &vErr) || vErr.Status != tc.status {
			t.Fatalf("path (%s, %q) got status %v want %d", tc.conn, tc.raw, err, tc.status)
		}
	}

	if err := ValidateLocalPair("/tmp/project/src", "/tmp/project/src"); err == nil {
		t.Fatal("expected equal source and destination to fail")
	}
	if err := ValidateLocalPair("/tmp/project/src", "/tmp/project/src/backups"); err == nil {
		t.Fatal("expected nested destination inside source to fail")
	}
	if err := ValidateLocalPair("/tmp/project/src", "/tmp/project/backups"); err != nil {
		t.Fatalf("expected sibling destination to pass: %v", err)
	}
}

func TestScheduleNextRunCalculation(t *testing.T) {
	loc := time.FixedZone("CST", 8*3600)
	// 2026-11-20 is Friday, 10:00 CST (02:00 UTC)
	now := time.Date(2026, 11, 20, 10, 0, 0, 0, loc)

	manual := BackupJob{ScheduleEnabled: false, ScheduleMode: ScheduleManual}
	if got := NextRunTime(manual, now, loc); got != nil {
		t.Fatalf("manual schedule returned %v", *got)
	}

	interval := BackupJob{ScheduleEnabled: true, ScheduleMode: ScheduleInterval, IntervalMinutes: 30}
	gotInterval := NextRunTime(interval, now, loc)
	if gotInterval == nil || !gotInterval.Equal(now.Add(30*time.Minute).UTC()) {
		t.Fatalf("interval next=%v", gotInterval)
	}

	dailyLater := BackupJob{ScheduleEnabled: true, ScheduleMode: ScheduleDaily, DailyTime: "14:30"}
	gotDailyLater := NextRunTime(dailyLater, now, loc)
	wantDailyLater := time.Date(2026, 11, 20, 14, 30, 0, 0, loc).UTC()
	if gotDailyLater == nil || !gotDailyLater.Equal(wantDailyLater) {
		t.Fatalf("daily same-day next=%v want %v", gotDailyLater, wantDailyLater)
	}

	dailyTomorrow := BackupJob{ScheduleEnabled: true, ScheduleMode: ScheduleDaily, DailyTime: "03:00"}
	gotDailyTomorrow := NextRunTime(dailyTomorrow, now, loc)
	wantDailyTomorrow := time.Date(2026, 11, 21, 3, 0, 0, 0, loc).UTC()
	if gotDailyTomorrow == nil || !gotDailyTomorrow.Equal(wantDailyTomorrow) {
		t.Fatalf("daily tomorrow next=%v want %v", gotDailyTomorrow, wantDailyTomorrow)
	}

	// Weekly on Monday (1) at 09:00 from Friday 10:00 -> +3 days
	weekly := BackupJob{ScheduleEnabled: true, ScheduleMode: ScheduleWeekly, Weekday: int(time.Monday), DailyTime: "09:00"}
	gotWeekly := NextRunTime(weekly, now, loc)
	wantWeekly := time.Date(2026, 11, 23, 9, 0, 0, 0, loc).UTC()
	if gotWeekly == nil || !gotWeekly.Equal(wantWeekly) {
		t.Fatalf("weekly next=%v want %v", gotWeekly, wantWeekly)
	}

	// Weekly on same weekday (Friday) but past 10:00 -> +7 days
	weeklyPast := BackupJob{ScheduleEnabled: true, ScheduleMode: ScheduleWeekly, Weekday: int(time.Friday), DailyTime: "09:00"}
	gotWeeklyPast := NextRunTime(weeklyPast, now, loc)
	wantWeeklyPast := time.Date(2026, 11, 27, 9, 0, 0, 0, loc).UTC()
	if gotWeeklyPast == nil || !gotWeeklyPast.Equal(wantWeeklyPast) {
		t.Fatalf("weekly past next=%v want %v", gotWeeklyPast, wantWeeklyPast)
	}
}

func callHTTP(t *testing.T, handler http.Handler, method, url string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var reader *bytes.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		reader = bytes.NewReader(raw)
	} else {
		reader = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, url, reader)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

func openTestService(t *testing.T, nowFn func() time.Time) (*store.Store, *Service, *fakeOpener) {
	t.Helper()
	tmp := t.TempDir()
	st, err := store.Open(filepath.Join(tmp, "app.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	if _, err := st.SaveSSHConnection(context.Background(), map[string]any{
		"id":           "ssh-debian",
		"display_name": "DebianProd",
		"host":         "10.0.0.10",
		"port":         22,
		"user":         "luwei",
		"workspace":    "/srv/work",
		"state":        "connected",
	}); err != nil {
		t.Fatal(err)
	}

	opener := &fakeOpener{}
	defaultDest := filepath.Join(tmp, "default-backups")
	resolver := targetfs.NewResolver(targetfs.ResolverOptions{
		Store:            st,
		DefaultWorkspace: tmp,
		AllowTempDirs:    true,
		WSLHelperPath:    "/unused/helper",
	})
	svc := NewService(Options{
		Store:                 st,
		Resolver:              resolver,
		Opener:                opener,
		DefaultWorkspace:      tmp,
		DefaultDestinationDir: defaultDest,
		Now:                   nowFn,
		Location:              time.UTC,
	})
	return st, svc, opener
}

func TestEndToEndLocalDirectoryAndFileBackupWorkflow(t *testing.T) {
	nowVal := time.Date(2026, 11, 20, 2, 0, 0, 0, time.UTC)
	var mu sync.Mutex
	nowFn := func() time.Time {
		mu.Lock()
		defer mu.Unlock()
		current := nowVal
		nowVal = nowVal.Add(2 * time.Second)
		return current
	}

	_, svc, opener := openTestService(t, nowFn)

	workRoot := t.TempDir()
	srcDir := filepath.Join(workRoot, "myproject")
	outsideDir := filepath.Join(workRoot, "outside")
	destDir := filepath.Join(workRoot, "backups")
	if err := os.MkdirAll(filepath.Join(srcDir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(srcDir, ".ssh"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(outsideDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "README.md"), []byte("hello astrorder\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "sub", "config.yaml"), []byte("port: 8080\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, ".ssh", "id_rsa"), []byte("test key fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "sub", "antigravity-oauth-token"), []byte("test token fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outsideDir, "secret.txt"), []byte("OUTSIDE"), 0o600); err != nil {
		t.Fatal(err)
	}
	linkErr := os.Symlink(filepath.Join(outsideDir, "secret.txt"), filepath.Join(srcDir, "escape-link.txt"))
	if linkErr != nil && runtime.GOOS != "windows" {
		t.Fatal(linkErr)
	}

	// 1. Targets & browse check
	targetsRec := callHTTP(t, svc, http.MethodGet, "/api/v1/backups/targets", nil)
	if targetsRec.Code != http.StatusOK {
		t.Fatalf("targets status=%d body=%s", targetsRec.Code, targetsRec.Body.String())
	}
	var targetsResp TargetsResponse
	if err := json.Unmarshal(targetsRec.Body.Bytes(), &targetsResp); err != nil {
		t.Fatal(err)
	}
	if len(targetsResp.Items) < 2 || targetsResp.Items[0].ID != "local" || targetsResp.Items[1].Name != "DebianProd" {
		t.Fatalf("unexpected targets: %+v", targetsResp.Items)
	}

	browseRec := callHTTP(t, svc, http.MethodGet, "/api/v1/backups/browse?connection_id=local&path="+url.QueryEscape(srcDir), nil)
	if browseRec.Code != http.StatusOK {
		t.Fatalf("browse status=%d body=%s", browseRec.Code, browseRec.Body.String())
	}
	var browseResp BrowseResponse
	if err := json.Unmarshal(browseRec.Body.Bytes(), &browseResp); err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(browseResp.Items, func(entry BrowseEntry) bool { return entry.Name == ".ssh" && entry.IsDir }) {
		t.Fatalf("backup source browse did not list .ssh: %+v", browseResp.Items)
	}

	sshBrowseRec := callHTTP(t, svc, http.MethodGet, "/api/v1/backups/browse?connection_id=local&path="+url.QueryEscape(filepath.Join(srcDir, ".ssh")), nil)
	if sshBrowseRec.Code != http.StatusOK {
		t.Fatalf("browse .ssh status=%d body=%s", sshBrowseRec.Code, sshBrowseRec.Body.String())
	}

	// 2. Credential directories are still protected from backup writes.
	badJobRec := callHTTP(t, svc, http.MethodPost, "/api/v1/backups/jobs", map[string]any{
		"name":            "bad",
		"connection_id":   "local",
		"source_path":     outsideDir,
		"destination_dir": filepath.Join(srcDir, ".ssh", "backups"),
		"schedule_mode":   "manual",
	})
	if badJobRec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for credential destination, got %d %s", badJobRec.Code, badJobRec.Body.String())
	}

	// 3. Create job with retention_count=2
	retention := 2
	createRec := callHTTP(t, svc, http.MethodPost, "/api/v1/backups/jobs", map[string]any{
		"name":            "工程目录",
		"connection_id":   "local",
		"source_path":     srcDir,
		"destination_dir": destDir,
		"schedule_mode":   "manual",
		"retention_count": retention,
	})
	if createRec.Code != http.StatusCreated {
		t.Fatalf("create job status=%d body=%s", createRec.Code, createRec.Body.String())
	}
	var job BackupJob
	if err := json.Unmarshal(createRec.Body.Bytes(), &job); err != nil {
		t.Fatal(err)
	}

	// 4. Run 3 manual backups; retention=2 should automatically prune v1.
	notes := []string{"首次快照", "第二次修改后", "第三次上线前"}
	for idx, note := range notes {
		if idx == 1 {
			if err := os.WriteFile(filepath.Join(srcDir, "README.md"), []byte("hello v2\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		runRec := callHTTP(t, svc, http.MethodPost, "/api/v1/backups/jobs/"+job.ID+"/run", map[string]any{"note": note})
		if runRec.Code != http.StatusAccepted {
			t.Fatalf("run %d status=%d body=%s", idx+1, runRec.Code, runRec.Body.String())
		}
		svc.Wait()
	}

	versionsRec := callHTTP(t, svc, http.MethodGet, "/api/v1/backups/jobs/"+job.ID+"/versions", nil)
	if versionsRec.Code != http.StatusOK {
		t.Fatalf("list versions status=%d body=%s", versionsRec.Code, versionsRec.Body.String())
	}
	var versionsResp struct {
		Items []BackupVersion `json:"items"`
	}
	if err := json.Unmarshal(versionsRec.Body.Bytes(), &versionsResp); err != nil {
		t.Fatal(err)
	}
	if len(versionsResp.Items) != 2 {
		t.Fatalf("retention expected 2 versions, got %d: %+v", len(versionsResp.Items), versionsResp.Items)
	}
	latest := versionsResp.Items[0]
	if latest.VersionNumber != 3 || latest.VersionLabel != "v3" || latest.Status != StatusSuccess || latest.SourceKind != "directory" || latest.Note != "第三次上线前" || !latest.ExistsOnDisk {
		t.Fatalf("unexpected latest version: %+v", latest)
	}
	if latest.FileCount != 4 {
		t.Fatalf("expected exactly 4 files copied, got %d", latest.FileCount)
	}

	contentRoot := filepath.Join(latest.LocalPath, "content", "myproject")
	for rel, want := range map[string]string{
		"README.md":                     "hello v2\n",
		filepath.Join(".ssh", "id_rsa"): "test key fixture",
		filepath.Join("sub", "antigravity-oauth-token"): "test token fixture",
	} {
		got, err := os.ReadFile(filepath.Join(contentRoot, rel))
		if err != nil || string(got) != want {
			t.Fatalf("copied %s mismatch: %q err=%v", rel, got, err)
		}
	}
	if _, err := os.Lstat(filepath.Join(contentRoot, "escape-link.txt")); !os.IsNotExist(err) {
		t.Fatalf("outside symlink must not exist in backup: %v", err)
	}

	// Verify v1 directory was pruned from disk as well.
	jobDir := filepath.Dir(latest.LocalPath)
	diskEntries, err := os.ReadDir(jobDir)
	if err != nil || len(diskEntries) != 2 {
		t.Fatalf("expected 2 version folders in %s, got %v err=%v", jobDir, len(diskEntries), err)
	}

	// 5. Edit version note -> synced to DB and backup-manifest.json
	patchRec := callHTTP(t, svc, http.MethodPatch, "/api/v1/backups/versions/"+latest.ID, map[string]any{"note": "已更新备注：确认可用"})
	if patchRec.Code != http.StatusOK {
		t.Fatalf("patch note status=%d body=%s", patchRec.Code, patchRec.Body.String())
	}
	rawManifest, err := os.ReadFile(filepath.Join(latest.LocalPath, manifestFilename))
	if err != nil {
		t.Fatal(err)
	}
	var manifest BackupManifest
	if err := json.Unmarshal(rawManifest, &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.Note != "已更新备注：确认可用" || manifest.ManifestSHA256 == "" {
		t.Fatalf("manifest mismatch: %+v", manifest)
	}
	if linkErr == nil && !slices.Equal(manifest.SkippedLinks, []string{"escape-link.txt"}) {
		t.Fatalf("manifest skipped links mismatch: %+v", manifest.SkippedLinks)
	}

	// 6. Open version folder
	openRec := callHTTP(t, svc, http.MethodPost, "/api/v1/backups/versions/"+latest.ID+"/open", nil)
	if openRec.Code != http.StatusOK {
		t.Fatalf("open status=%d body=%s", openRec.Code, openRec.Body.String())
	}
	if len(opener.calls) != 1 || opener.calls[0].path != latest.LocalPath {
		t.Fatalf("unexpected opener calls: %+v", opener.calls)
	}

	// 7. Delete version v2 without deleting disk files, then v3 with deleting disk files.
	v2 := versionsResp.Items[1]
	delV2Rec := callHTTP(t, svc, http.MethodDelete, "/api/v1/backups/versions/"+v2.ID+"?delete_files=false", nil)
	if delV2Rec.Code != http.StatusOK {
		t.Fatalf("delete v2 status=%d", delV2Rec.Code)
	}
	if _, statErr := os.Stat(v2.LocalPath); statErr != nil {
		t.Fatalf("expected v2 disk dir preserved when delete_files=false: %v", statErr)
	}
	delV3Rec := callHTTP(t, svc, http.MethodDelete, "/api/v1/backups/versions/"+latest.ID+"?delete_files=true", nil)
	if delV3Rec.Code != http.StatusOK {
		t.Fatalf("delete v3 status=%d", delV3Rec.Code)
	}
	if _, statErr := os.Stat(latest.LocalPath); !os.IsNotExist(statErr) {
		t.Fatalf("expected v3 disk dir removed when delete_files=true")
	}

	// 8. Delete job with delete_files=true cleans jobDir too.
	delJobRec := callHTTP(t, svc, http.MethodDelete, "/api/v1/backups/jobs/"+job.ID+"?delete_files=true", nil)
	if delJobRec.Code != http.StatusOK {
		t.Fatalf("delete job status=%d body=%s", delJobRec.Code, delJobRec.Body.String())
	}
	if _, statErr := os.Stat(jobDir); !os.IsNotExist(statErr) {
		t.Fatalf("expected job dir %s removed", jobDir)
	}
}

func TestSingleFileAndScheduledBackupExecution(t *testing.T) {
	fixedNow := time.Date(2026, 11, 20, 4, 0, 0, 0, time.UTC)
	_, svc, _ := openTestService(t, func() time.Time { return fixedNow })

	workRoot := t.TempDir()
	singleFile := filepath.Join(workRoot, "credentials.json")
	destDir := filepath.Join(workRoot, "file-backups")
	if err := os.WriteFile(singleFile, []byte("single file snapshot content"), 0o600); err != nil {
		t.Fatal(err)
	}

	createRec := callHTTP(t, svc, http.MethodPost, "/api/v1/backups/jobs", map[string]any{
		"name":             "单文件定时备份",
		"connection_id":    "local",
		"source_path":      singleFile,
		"destination_dir":  destDir,
		"schedule_enabled": true,
		"schedule_mode":    "interval",
		"interval_minutes": 15,
	})
	if createRec.Code != http.StatusCreated {
		t.Fatalf("create scheduled job status=%d body=%s", createRec.Code, createRec.Body.String())
	}
	var job BackupJob
	if err := json.Unmarshal(createRec.Body.Bytes(), &job); err != nil {
		t.Fatal(err)
	}
	if job.NextRunAt == nil || !job.ScheduleEnabled || job.RetentionCount != DefaultRetentionCount {
		t.Fatalf("unexpected scheduled job fields: %+v", job)
	}

	// Advance clock past next_run_at and trigger due jobs.
	fixedNow = fixedNow.Add(16 * time.Minute)
	svc.runDueJobs(context.Background())
	svc.Wait()

	versionsRec := callHTTP(t, svc, http.MethodGet, "/api/v1/backups/jobs/"+job.ID+"/versions", nil)
	var versionsResp struct {
		Items []BackupVersion `json:"items"`
	}
	if err := json.Unmarshal(versionsRec.Body.Bytes(), &versionsResp); err != nil {
		t.Fatal(err)
	}
	if len(versionsResp.Items) != 1 {
		t.Fatalf("expected 1 scheduled version, got %+v", versionsResp.Items)
	}
	v := versionsResp.Items[0]
	if v.TriggerKind != TriggerScheduled || v.SourceKind != "file" || v.FileCount != 1 || v.Status != StatusSuccess {
		t.Fatalf("unexpected scheduled version: %+v", v)
	}
	raw, err := os.ReadFile(filepath.Join(v.LocalPath, "content", filepath.Base(singleFile)))
	if err != nil || string(raw) != "single file snapshot content" {
		t.Fatalf("copied single file mismatch: %q err=%v", string(raw), err)
	}

	// Verify next_run_at moved forward again relative to fixedNow.
	updatedJob, found, err := svc.getJobRow(context.Background(), job.ID)
	if err != nil || !found || updatedJob.NextRunAt == nil {
		t.Fatalf("updatedJob=%+v found=%v err=%v", updatedJob, found, err)
	}
	parsedNext, err := time.Parse(time.RFC3339Nano, *updatedJob.NextRunAt)
	if err != nil || !parsedNext.After(fixedNow) {
		t.Fatalf("expected next_run_at after %v, got %v err=%v", fixedNow, updatedJob.NextRunAt, err)
	}
}

func TestRemoteDirectoryAndFileBackupViaHelperSubprocess(t *testing.T) {
	tmp := t.TempDir()
	fakeWSL := filepath.Join(tmp, "fake-wsl.sh")
	script := "#!/bin/sh\nif [ \"$4\" = /bin/sh ]; then shift 3; exec \"$@\"; fi\nGO_WANT_BACKUP_HELPER=1 exec \"" + os.Args[0] + "\" -test.run=TestBackupHelperProcess --\n"
	if err := os.WriteFile(fakeWSL, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	st, err := store.Open(filepath.Join(tmp, "app.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	remoteSrc := filepath.Join(tmp, "remote-src")
	if err := os.MkdirAll(filepath.Join(remoteSrc, "nested"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(remoteSrc, ".ssh"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(remoteSrc, "nested", "app.txt"), []byte("remote dir payload"), 0o644); err != nil {
		t.Fatal(err)
	}
	literalPath := filepath.Join(remoteSrc, "literal dir ", "config ")
	mkdirFixture(t, filepath.Dir(literalPath))
	writeFixture(t, literalPath, "literal path")
	remoteFile := filepath.Join(remoteSrc, ".ssh", "id_ed25519")
	if err := os.WriteFile(remoteFile, []byte("remote key fixture"), 0o600); err != nil {
		t.Fatal(err)
	}

	destDir := filepath.Join(tmp, "local-dest")
	resolver := targetfs.NewResolver(targetfs.ResolverOptions{
		Store:         st,
		WSLBinary:     fakeWSL,
		WSLHelperPath: "/unused/helper",
	})
	svc := NewService(Options{
		Store:    st,
		Resolver: resolver,
		Location: time.UTC,
	})

	ctx := context.Background()
	browse, err := svc.BrowsePath(ctx, "wsl:Debian", remoteSrc)
	if err != nil || !slices.ContainsFunc(browse.Items, func(entry BrowseEntry) bool { return entry.Name == ".ssh" && entry.IsDir }) {
		t.Fatalf("remote source browse did not list .ssh: %+v err=%v", browse, err)
	}
	browse, err = svc.BrowsePath(ctx, "wsl:Debian", filepath.Dir(remoteFile))
	if err != nil || !slices.ContainsFunc(browse.Items, func(entry BrowseEntry) bool { return entry.Name == "id_ed25519" && !entry.IsDir }) {
		t.Fatalf("remote .ssh browse did not list id_ed25519: %+v err=%v", browse, err)
	}

	// Verify both ordinary and credential files are included in the remote directory export.
	dirJob := BackupJob{
		ID:                 "bkj-remote-dir",
		Name:               "remote-dir",
		ConnectionID:       "wsl:Debian",
		SourcePath:         remoteSrc,
		DestinationDir:     destDir,
		PostRestoreCommand: `test -f nested/app.txt && printf ok > restored`,
		ScheduleMode:       ScheduleManual,
		RetentionCount:     5,
		CreatedAt:          formatTS(time.Now()),
		UpdatedAt:          formatTS(time.Now()),
	}
	if err := svc.insertJobRow(context.Background(), dirJob); err != nil {
		t.Fatal(err)
	}
	vDir, err := svc.TriggerManualBackup(context.Background(), dirJob.ID, "remote dir note")
	if err != nil {
		t.Fatal(err)
	}
	if vDir.Status != StatusSuccess || vDir.SourceKind != "directory" || vDir.FileCount != 3 {
		t.Fatalf("unexpected remote dir version: %+v", vDir)
	}
	gotApp, err := os.ReadFile(filepath.Join(vDir.LocalPath, "content", "remote-src", "nested", "app.txt"))
	if err != nil || string(gotApp) != "remote dir payload" {
		t.Fatalf("remote dir content=%q err=%v", string(gotApp), err)
	}
	gotKey, err := os.ReadFile(filepath.Join(vDir.LocalPath, "content", "remote-src", ".ssh", "id_ed25519"))
	if err != nil || string(gotKey) != "remote key fixture" {
		t.Fatalf("remote directory key=%q err=%v", gotKey, err)
	}
	writeFixture(t, filepath.Join(remoteSrc, "nested", "app.txt"), "live content")
	writeFixture(t, filepath.Join(remoteSrc, "only-live"), "keep")
	if err := os.Remove(literalPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Dir(literalPath)); err != nil {
		t.Fatal(err)
	}
	plan, _, err := svc.loadRestorePlan(ctx, vDir.ID)
	if err != nil {
		t.Fatal(err)
	}
	result, err := svc.restoreVersion(ctx, vDir.ID, plan.Confirmation)
	if err != nil || result.HookStatus != "success" {
		t.Fatalf("remote restore result=%+v, err=%v", result, err)
	}
	assertRestoreFile(t, filepath.Join(remoteSrc, "nested", "app.txt"), "remote dir payload")
	assertRestoreFile(t, filepath.Join(remoteSrc, "only-live"), "keep")
	assertRestoreFile(t, filepath.Join(remoteSrc, "restored"), "ok")
	assertRestoreFile(t, literalPath, "literal path")

	// Test remote single-file fallback (invalid_workspace -> Stat & Read)
	fileJob := BackupJob{
		ID:             "bkj-remote-file",
		Name:           "remote-file",
		ConnectionID:   "wsl:Debian",
		SourcePath:     remoteFile,
		DestinationDir: destDir,
		ScheduleMode:   ScheduleManual,
		RetentionCount: 5,
		CreatedAt:      formatTS(time.Now()),
		UpdatedAt:      formatTS(time.Now()),
	}
	if err := svc.insertJobRow(context.Background(), fileJob); err != nil {
		t.Fatal(err)
	}
	vFile, err := svc.TriggerManualBackup(context.Background(), fileJob.ID, "remote file note")
	if err != nil {
		t.Fatal(err)
	}
	if vFile.Status != StatusSuccess || vFile.SourceKind != "file" || vFile.FileCount != 1 {
		t.Fatalf("unexpected remote file version: %+v", vFile)
	}
	gotSingle, err := os.ReadFile(filepath.Join(vFile.LocalPath, "content", filepath.Base(remoteFile)))
	if err != nil || string(gotSingle) != "remote key fixture" {
		t.Fatalf("remote file content=%q err=%v", string(gotSingle), err)
	}
}
