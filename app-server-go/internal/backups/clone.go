package backups

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
)

type cloneJobPayload struct {
	jobPayload
	CopyVersions bool `json:"copy_versions"`
}

func (s *Service) handleCloneJob(w http.ResponseWriter, r *http.Request, sourceID string) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, http.MethodPost)
		return
	}
	ctx := r.Context()
	if !s.acquireJobRun(sourceID, "") {
		writeServiceError(w, policyError(409, "该任务正在执行，请稍后再克隆。"))
		return
	}
	defer s.releaseJobRun(sourceID)
	source, found, err := s.getJobRow(ctx, sourceID)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	if !found {
		writeServiceError(w, policyError(404, "备份任务不存在。"))
		return
	}

	name := []rune(source.Name)
	if len(name) > 117 {
		name = name[:117]
	}
	input := cloneJobPayload{jobPayload: jobPayload{
		Name: string(name) + " 副本", ConnectionID: source.ConnectionID,
		SourcePath: source.SourcePath, DestinationDir: source.DestinationDir,
		ScheduleEnabled: source.ScheduleEnabled, ScheduleMode: source.ScheduleMode,
		IntervalMinutes: &source.IntervalMinutes, DailyTime: source.DailyTime,
		Weekday: &source.Weekday, RetentionCount: &source.RetentionCount,
	}}
	if err := decodeJSONBody(w, r, &input, false); err != nil {
		writeServiceError(w, err)
		return
	}
	seed := BackupJob{
		ID: newID("bkj-"), CreatedAt: formatTS(s.now()),
		PostRestoreCommand: source.PostRestoreCommand, PostRestoreRestartAgent: source.PostRestoreRestartAgent,
	}
	cloned, err := s.buildValidatedJob(ctx, &seed, input.jobPayload)
	if err == nil {
		cloned, err = s.cloneBackupJob(ctx, sourceID, cloned, input.CopyVersions)
	}
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, cloned)
}

func (s *Service) cloneBackupJob(ctx context.Context, sourceID string, job BackupJob, copyVersions bool) (BackupJob, error) {
	targets, err := s.ListTargets(ctx)
	if err != nil {
		return BackupJob{}, err
	}
	targetMap := make(map[string]Target, len(targets.Items))
	for _, target := range targets.Items {
		targetMap[target.ID] = target
	}

	var versions []BackupVersion
	var copiedDir string
	if copyVersions {
		versions, copiedDir, err = s.copyBackupVersions(ctx, sourceID, job)
		if err != nil {
			return BackupJob{}, err
		}
	}
	if err := s.insertClonedJob(ctx, job, versions); err != nil {
		if copiedDir != "" {
			s.removeIncompleteClone(copiedDir)
		}
		return BackupJob{}, err
	}
	counts := map[string]int{job.ID: len(versions)}
	latest := make(map[string]*BackupVersion, 1)
	if len(versions) > 0 {
		latest[job.ID] = &versions[0]
	}
	s.enrichJob(&job, targetMap, counts, latest)
	return job, nil
}

func (s *Service) copyBackupVersions(ctx context.Context, sourceID string, job BackupJob) ([]BackupVersion, string, error) {
	sourceVersions, err := s.listVersionRows(ctx, sourceID)
	if err != nil {
		return nil, "", err
	}
	jobDir := filepath.Join(job.DestinationDir, safeJobFolderName(job))
	if !filepath.IsAbs(jobDir) {
		return nil, "", policyError(422, "本机备份保存目录必须是当前系统的绝对路径。")
	}
	canonicalDir, err := resolveCloneDirectory(jobDir)
	if err != nil {
		return nil, "", err
	}
	if _, err := ValidateDestinationDir(canonicalDir); err != nil {
		return nil, "", err
	}
	versions := make([]BackupVersion, 0, len(sourceVersions))
	for _, source := range sourceVersions {
		if source.Status != StatusSuccess {
			continue
		}
		originalDir, err := filepath.EvalSymlinks(source.LocalPath)
		if err != nil || !source.ExistsOnDisk || source.ManifestSHA256 == nil {
			return nil, "", policyError(409, fmt.Sprintf("备份版本 %s 的内容缺失或不完整。", source.VersionLabel))
		}
		if isSubPathOrEqual(canonicalDir, originalDir) {
			return nil, "", policyError(422, "克隆保存目录不能位于原备份版本目录内。")
		}
		versions = append(versions, source)
	}
	if len(versions) == 0 {
		return nil, "", policyError(409, "当前任务没有成功的备份版本可供克隆。")
	}
	if err := os.MkdirAll(filepath.Dir(canonicalDir), 0o755); err != nil {
		return nil, "", err
	}
	if err := os.Mkdir(canonicalDir, 0o755); err != nil {
		return nil, "", err
	}
	for i, source := range versions {
		version, err := cloneBackupVersion(ctx, source, job, jobDir, canonicalDir)
		if err != nil {
			s.removeIncompleteClone(canonicalDir)
			return nil, "", err
		}
		versions[i] = version
	}
	return versions, canonicalDir, nil
}

// Resolve existing directory ancestors before creating anything on disk.
func resolveCloneDirectory(dir string) (string, error) {
	resolved, err := filepath.EvalSymlinks(dir)
	if err == nil {
		return resolved, nil
	}
	parent := filepath.Dir(dir)
	if !errors.Is(err, fs.ErrNotExist) || parent == dir {
		return "", err
	}
	if _, err := os.Lstat(dir); !errors.Is(err, fs.ErrNotExist) {
		return "", fmt.Errorf("无法解析克隆保存目录 %q", dir)
	}
	resolved, err = resolveCloneDirectory(parent)
	if err != nil {
		return "", err
	}
	return filepath.Join(resolved, filepath.Base(dir)), nil
}

func (s *Service) removeIncompleteClone(dir string) {
	if err := os.RemoveAll(dir); err != nil {
		s.logger.Warn("remove incomplete backup clone failed", "path", dir, "error", err)
	}
}

func cloneBackupVersion(ctx context.Context, source BackupVersion, job BackupJob, jobDir, canonicalDir string) (BackupVersion, error) {
	manifest, err := readRestoreManifest(source)
	if err != nil {
		return BackupVersion{}, err
	}
	content, err := openSnapshotContent(source.LocalPath)
	if err != nil {
		return BackupVersion{}, err
	}
	defer content.Close()

	version := source
	version.ID = newID("bkv-")
	version.JobID = job.ID
	version.ConnectionID = job.ConnectionID
	version.SourcePath = job.SourcePath
	dirName := fmt.Sprintf("v%03d_%s", source.VersionNumber, version.ID)
	version.LocalPath = filepath.Join(jobDir, dirName)
	versionDir := filepath.Join(canonicalDir, dirName)
	contentDir := filepath.Join(versionDir, "content")
	if err := os.MkdirAll(contentDir, 0o755); err != nil {
		return BackupVersion{}, err
	}
	err = fs.WalkDir(content.FS(), ".", func(rel string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		localRel, err := filepath.Localize(rel)
		if err != nil {
			return err
		}
		dest := filepath.Join(contentDir, localRel)
		if entry.IsDir() {
			return os.Mkdir(dest, 0o755)
		}
		if !entry.Type().IsRegular() {
			return policyError(409, "备份内容包含特殊文件或链接，无法克隆。")
		}
		file, err := content.Open(localRel)
		if err != nil {
			return err
		}
		defer file.Close()
		info, err := file.Stat()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return policyError(409, "备份内容已发生变化，无法克隆。")
		}
		return writeReaderToFile(ctx, dest, io.LimitReader(file, info.Size()), info.Mode().Perm())
	})
	if err != nil {
		return BackupVersion{}, err
	}
	entries, fileCount, totalBytes, digest, err := scanSnapshot(ctx, os.DirFS(contentDir))
	if err != nil {
		return BackupVersion{}, err
	}
	if !snapshotMatches(version, entries, fileCount, totalBytes, digest) {
		return BackupVersion{}, policyError(409, fmt.Sprintf("备份版本 %s 的内容校验失败，无法克隆。", source.VersionLabel))
	}

	manifest.VersionID = version.ID
	manifest.JobID = job.ID
	manifest.JobName = job.Name
	manifest.ConnectionID = job.ConnectionID
	manifest.SourcePath = job.SourcePath
	manifest.Note = version.Note
	if err := writeManifestAtomic(filepath.Join(versionDir, manifestFilename), manifest); err != nil {
		return BackupVersion{}, err
	}
	return version, nil
}
