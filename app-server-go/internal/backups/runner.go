package backups

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"
	"unicode"

	"astrorder.dev/app-server/internal/targetfs"
)

func newID(prefix string) string {
	var raw [12]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return fmt.Sprintf("%s%d", prefix, time.Now().UnixNano())
	}
	return prefix + hex.EncodeToString(raw[:])
}

func safeJobFolderName(job BackupJob) string {
	var b strings.Builder
	lastUnderscore := false
	for _, r := range strings.TrimSpace(job.Name) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-' || r == '_' {
			b.WriteRune(r)
			lastUnderscore = false
			continue
		}
		if !lastUnderscore && b.Len() > 0 {
			b.WriteByte('_')
			lastUnderscore = true
		}
	}
	slug := strings.Trim(b.String(), "_.- ")
	if len([]rune(slug)) > 40 {
		runes := []rune(slug)
		slug = string(runes[:40])
	}
	if slug == "" {
		slug = "backup"
	}
	suffix := job.ID
	if len(suffix) > 12 {
		suffix = suffix[len(suffix)-12:]
	}
	return slug + "_" + suffix
}

func (s *Service) isJobRunning(jobID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.runningJobs[jobID]
	return ok
}

func (s *Service) acquireJobRun(jobID, versionID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.runningJobs[jobID]; ok {
		return false
	}
	s.runningJobs[jobID] = versionID
	return true
}

func (s *Service) releaseJobRun(jobID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.runningJobs, jobID)
}

// StartBackup validates capacity, creates the initial running version row,
// and starts the backup asynchronously unless syncMode is true (for unit tests).
func (s *Service) TriggerManualBackup(ctx context.Context, jobID, note string) (BackupVersion, error) {
	return s.startBackup(ctx, jobID, TriggerManual, note, false)
}

func (s *Service) startBackup(ctx context.Context, jobID, triggerKind, note string, async bool) (BackupVersion, error) {
	versionID := newID("bkv-")
	if !s.acquireJobRun(jobID, versionID) {
		return BackupVersion{}, policyError(409, "该任务正在备份、还原或更新，请稍后再试。")
	}
	job, found, err := s.getJobRow(ctx, jobID)
	if err != nil {
		s.releaseJobRun(jobID)
		return BackupVersion{}, err
	}
	if !found {
		s.releaseJobRun(jobID)
		return BackupVersion{}, policyError(404, "备份任务不存在。")
	}

	startedAt := s.now().UTC()
	vNum, err := s.nextVersionNumber(ctx, job.ID)
	if err != nil {
		s.releaseJobRun(job.ID)
		return BackupVersion{}, err
	}
	jobDir := filepath.Join(job.DestinationDir, safeJobFolderName(job))
	versionDirName := fmt.Sprintf("v%03d_%s", vNum, startedAt.In(s.location).Format("20060102-150405"))
	versionDir := filepath.Join(jobDir, versionDirName)
	versionLabel := fmt.Sprintf("v%d", vNum)

	runningStatus := StatusRunning
	version := BackupVersion{
		ID:            versionID,
		JobID:         job.ID,
		VersionNumber: vNum,
		VersionLabel:  versionLabel,
		TriggerKind:   triggerKind,
		Note:          strings.TrimSpace(note),
		Status:        runningStatus,
		LocalPath:     versionDir,
		SourceKind:    "unknown",
		FileCount:     0,
		TotalBytes:    0,
		CreatedAt:     formatTS(startedAt),
		ExistsOnDisk:  false,
		ConnectionID:  job.ConnectionID,
		SourcePath:    job.SourcePath,
	}
	if err := s.insertVersionRow(ctx, version); err != nil {
		s.releaseJobRun(job.ID)
		return BackupVersion{}, err
	}
	_ = s.updateJobRunStatus(ctx, job.ID, version.CreatedAt, StatusRunning, nil, job.NextRunAt, version.CreatedAt)

	if async {
		s.runWG.Add(1)
		go func() {
			defer s.runWG.Done()
			defer s.releaseJobRun(job.ID)
			s.executeBackup(context.Background(), job, version)
		}()
		return version, nil
	}

	defer s.releaseJobRun(job.ID)
	completed := s.executeBackup(ctx, job, version)
	return completed, nil
}

func (s *Service) triggerBackupAsync(ctx context.Context, jobID, triggerKind, note string) (BackupVersion, error) {
	return s.startBackup(ctx, jobID, triggerKind, note, true)
}

func (s *Service) executeBackup(ctx context.Context, job BackupJob, version BackupVersion) BackupVersion {
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 30*time.Minute)
		defer cancel()
	}

	jobDir := filepath.Dir(version.LocalPath)
	var execErr error
	var sourceKind string
	var skipped []string

	if err := os.MkdirAll(jobDir, 0o755); err != nil {
		execErr = fmt.Errorf("创建本机备份目录失败: %w", err)
	} else {
		sourceKind, skipped, execErr = s.materializeVersion(ctx, job, version, jobDir)
	}

	finishedAt := s.now().UTC()
	finishedStr := formatTS(finishedAt)
	version.FinishedAt = &finishedStr

	var errPtr *string
	if execErr != nil {
		msg := execErr.Error()
		errPtr = &msg
		version.Status = StatusFailed
		version.ErrorDetail = errPtr
		version.ExistsOnDisk = false
	} else {
		fileCount, totalBytes, shaHex, sumErr := inspectContentDir(filepath.Join(version.LocalPath, "content"))
		if sumErr != nil {
			msg := fmt.Sprintf("汇总备份内容失败: %v", sumErr)
			errPtr = &msg
			version.Status = StatusFailed
			version.ErrorDetail = errPtr
			_ = os.RemoveAll(version.LocalPath)
			version.ExistsOnDisk = false
		} else {
			version.Status = StatusSuccess
			version.SourceKind = sourceKind
			version.FileCount = fileCount
			version.TotalBytes = totalBytes
			version.ManifestSHA256 = &shaHex
			version.ExistsOnDisk = true

			manifest := BackupManifest{
				VersionID:      version.ID,
				VersionNumber:  version.VersionNumber,
				VersionLabel:   version.VersionLabel,
				JobID:          job.ID,
				JobName:        job.Name,
				ConnectionID:   job.ConnectionID,
				SourcePath:     job.SourcePath,
				SourceKind:     sourceKind,
				TriggerKind:    version.TriggerKind,
				Note:           version.Note,
				FileCount:      fileCount,
				TotalBytes:     totalBytes,
				SkippedLinks:   skipped,
				ManifestSHA256: shaHex,
				CreatedAt:      version.CreatedAt,
				FinishedAt:     finishedStr,
			}
			if writeErr := writeManifestAtomic(filepath.Join(version.LocalPath, manifestFilename), manifest); writeErr != nil {
				s.logger.Warn("write backup manifest failed", "job_id", job.ID, "version_id", version.ID, "error", writeErr)
			}
		}
	}

	if err := s.updateVersionResult(ctx, version); err != nil {
		s.logger.Error("update backup version result failed", "version_id", version.ID, "error", err)
	}

	nextRun := job.NextRunAt
	if version.TriggerKind == TriggerScheduled {
		nextRun = formatTSPtr(NextRunTime(job, finishedAt, s.location))
	}
	if err := s.updateJobRunStatus(ctx, job.ID, finishedStr, version.Status, errPtr, nextRun, finishedStr); err != nil {
		s.logger.Error("update backup job status failed", "job_id", job.ID, "error", err)
	}

	if version.Status == StatusSuccess && job.RetentionCount > 0 {
		s.pruneOldVersions(ctx, job)
	}
	return version
}

func (s *Service) materializeVersion(ctx context.Context, job BackupJob, version BackupVersion, jobDir string) (sourceKind string, skipped []string, err error) {
	if _, err := ValidateSourcePath(job.ConnectionID, job.SourcePath); err != nil {
		return "", nil, err
	}
	if _, err := ValidateDestinationDir(job.DestinationDir); err != nil {
		return "", nil, err
	}

	tempDir, err := os.MkdirTemp(jobDir, ".tmp-"+version.ID+"-*")
	if err != nil {
		return "", nil, fmt.Errorf("准备临时版本目录失败: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = os.RemoveAll(tempDir)
		}
	}()

	contentDir := filepath.Join(tempDir, "content")
	if err := os.MkdirAll(contentDir, 0o755); err != nil {
		return "", nil, fmt.Errorf("创建内容目录失败: %w", err)
	}

	if job.ConnectionID == "" || job.ConnectionID == targetfs.LocalConnectionID {
		if err := ValidateLocalPair(job.SourcePath, job.DestinationDir); err != nil {
			return "", nil, err
		}
		sourceKind, skipped, err = copyLocalSource(ctx, job.SourcePath, contentDir)
	} else {
		sourceKind, err = s.copyRemoteSource(ctx, job, jobDir, tempDir, contentDir)
	}
	if err != nil {
		return "", nil, err
	}

	if err := os.Rename(tempDir, version.LocalPath); err != nil {
		return "", nil, fmt.Errorf("提交备份版本目录失败: %w", err)
	}
	committed = true
	return sourceKind, skipped, nil
}

func copyLocalSource(ctx context.Context, sourcePath, contentDir string) (string, []string, error) {
	if _, err := os.Lstat(sourcePath); err != nil {
		if os.IsNotExist(err) {
			return "", nil, fmt.Errorf("源路径不存在: %s", sourcePath)
		}
		return "", nil, fmt.Errorf("读取源路径失败: %w", err)
	}
	resolved, err := filepath.EvalSymlinks(sourcePath)
	if err != nil {
		return "", nil, fmt.Errorf("解析源路径失败: %w", err)
	}
	statInfo, err := os.Stat(resolved)
	if err != nil {
		return "", nil, fmt.Errorf("检查源路径状态失败: %w", err)
	}

	baseName := filepath.Base(sourcePath)
	if statInfo.Mode().IsRegular() {
		dstFile := filepath.Join(contentDir, baseName)
		if err := copyFileAtomic(ctx, resolved, dstFile, statInfo.Mode().Perm()); err != nil {
			return "", nil, err
		}
		return "file", nil, nil
	}
	if !statInfo.IsDir() {
		return "", nil, errors.New("仅支持备份普通文件或目录")
	}

	dstRoot := filepath.Join(contentDir, baseName)
	if err := os.MkdirAll(dstRoot, 0o755); err != nil {
		return "", nil, err
	}

	var skipped []string
	walkErr := filepath.WalkDir(resolved, func(current string, d fs.DirEntry, wErr error) error {
		if wErr != nil {
			return wErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		rel, err := filepath.Rel(resolved, current)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		targetPath := filepath.Join(dstRoot, rel)
		if d.Type()&os.ModeSymlink != 0 {
			linkTarget, evalErr := filepath.EvalSymlinks(current)
			if evalErr != nil || !isSubPathOrEqual(linkTarget, resolved) {
				skipped = append(skipped, filepath.ToSlash(rel))
				return nil
			}
			targetInfo, statErr := os.Stat(linkTarget)
			if statErr != nil || targetInfo.IsDir() || !targetInfo.Mode().IsRegular() {
				skipped = append(skipped, filepath.ToSlash(rel))
				return nil
			}
			return copyFileAtomic(ctx, linkTarget, targetPath, targetInfo.Mode().Perm())
		}
		if d.IsDir() {
			return os.MkdirAll(targetPath, 0o755)
		}
		if !d.Type().IsRegular() {
			return nil
		}
		info, infoErr := d.Info()
		if infoErr != nil {
			return infoErr
		}
		return copyFileAtomic(ctx, current, targetPath, info.Mode().Perm())
	})
	if walkErr != nil {
		return "", nil, walkErr
	}
	return "directory", skipped, nil
}

func (s *Service) copyRemoteSource(ctx context.Context, job BackupJob, jobDir, tempDir, contentDir string) (string, error) {
	if s.resolver == nil {
		return "", errors.New("远程文件系统解析器不可用")
	}

	resolved, err := s.resolver.ResolveScopedRemote(ctx, job.ConnectionID, job.SourcePath, []string{jobDir})
	if err == nil {
		stagingParent := filepath.Join(tempDir, "stage")
		if mkErr := os.MkdirAll(stagingParent, 0o700); mkErr != nil {
			return "", mkErr
		}
		stageRes, stageErr := resolved.FileSystem.Stage(ctx, targetfs.StageRequest{
			Paths:       []string{job.SourcePath},
			Destination: stagingParent,
		})
		if stageErr == nil {
			if len(stageRes.Paths) == 0 {
				return "", errors.New("远程导出未产生任何文件")
			}
			staged := stageRes.Paths[0]
			destPath := filepath.Join(contentDir, filepath.Base(staged))
			if renameErr := os.Rename(staged, destPath); renameErr != nil {
				return "", fmt.Errorf("整理导出目录失败: %w", renameErr)
			}
			_ = os.RemoveAll(stagingParent)
			return "directory", nil
		}
		err = stageErr
		_ = os.RemoveAll(stagingParent)
	}

	if !isRemoteNotDirError(err) {
		return "", err
	}

	// Single-file fallback: scope to parentDir and read only the requested file.
	parentDir := path.Dir(job.SourcePath)
	parentResolved, err := s.resolver.ResolveScopedRemote(ctx, job.ConnectionID, parentDir, []string{jobDir})
	if err != nil {
		return "", err
	}
	info, err := parentResolved.FileSystem.Stat(ctx, job.SourcePath)
	if err != nil {
		return "", err
	}
	if info.IsDir {
		return "", errors.New("无法作为目录读取源路径")
	}
	if !info.Mode.IsRegular() {
		return "", errors.New("仅支持备份普通文件或目录")
	}
	readRes, err := parentResolved.FileSystem.Read(ctx, job.SourcePath)
	if err != nil {
		return "", err
	}
	defer readRes.Reader.Close()

	baseName := path.Base(job.SourcePath)
	dstFile := filepath.Join(contentDir, baseName)
	mode := info.Mode.Perm()
	if mode == 0 {
		mode = 0o644
	}
	if err := writeReaderToFile(ctx, dstFile, readRes.Reader, mode); err != nil {
		return "", err
	}
	return "file", nil
}

func isRemoteNotDirError(err error) bool {
	var fsErr *targetfs.Error
	if !errors.As(err, &fsErr) {
		return false
	}
	return fsErr.Code == "invalid_workspace" || fsErr.Code == "invalid_allowed_workspace"
}

func copyFileAtomic(ctx context.Context, src, dst string, mode fs.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	if mode == 0 {
		mode = 0o644
	}
	return writeReaderToFile(ctx, dst, in, mode)
}

func writeReaderToFile(ctx context.Context, dst string, src io.Reader, mode fs.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	buf := make([]byte, 128*1024)
	for {
		if err := ctx.Err(); err != nil {
			_ = out.Close()
			return err
		}
		n, rErr := src.Read(buf)
		if n > 0 {
			if _, wErr := out.Write(buf[:n]); wErr != nil {
				_ = out.Close()
				return wErr
			}
		}
		if errors.Is(rErr, io.EOF) {
			break
		}
		if rErr != nil {
			_ = out.Close()
			return rErr
		}
	}
	return out.Close()
}

type snapshotEntry struct {
	rel  string
	mode fs.FileMode
	size int64
	hash string
}

func inspectContentDir(contentDir string) (int, int64, string, error) {
	_, files, size, digest, err := scanSnapshot(context.Background(), os.DirFS(contentDir))
	return files, size, digest, err
}

func scanSnapshot(ctx context.Context, content fs.FS) (entries []snapshotEntry, fileCount int, totalBytes int64, digest string, err error) {
	err = fs.WalkDir(content, ".", func(rel string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		if d.IsDir() {
			entries = append(entries, snapshotEntry{rel: rel, mode: fs.ModeDir})
			return nil
		}
		if !d.Type().IsRegular() {
			return fmt.Errorf("备份内容包含特殊文件或链接 %q", rel)
		}
		f, openErr := content.Open(rel)
		if openErr != nil {
			return openErr
		}
		h := sha256.New()
		info, statErr := f.Stat()
		if statErr != nil || !info.Mode().IsRegular() {
			_ = f.Close()
			return fmt.Errorf("无法检查备份文件 %q", rel)
		}
		written, copyErr := io.Copy(h, &contextReader{ctx: ctx, source: f})
		closeErr := f.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
		fileCount++
		totalBytes += written
		entries = append(entries, snapshotEntry{
			rel:  rel,
			mode: info.Mode(),
			size: written,
			hash: hex.EncodeToString(h.Sum(nil)),
		})
		return nil
	})
	if err != nil {
		return nil, 0, 0, "", err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].rel < entries[j].rel })
	rootHash := sha256.New()
	for _, entry := range entries {
		if entry.mode.IsDir() {
			fmt.Fprintf(rootHash, "D %s\n", entry.rel)
		} else {
			fmt.Fprintf(rootHash, "F %s %d %s\n", entry.rel, entry.size, entry.hash)
		}
	}
	return entries, fileCount, totalBytes, hex.EncodeToString(rootHash.Sum(nil)), nil
}

func writeManifestAtomic(targetPath string, manifest BackupManifest) error {
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	tmp := targetPath + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, targetPath)
}

// UpdateVersionNote edits a version's remark and keeps its on-disk manifest synced.
func (s *Service) UpdateVersionNote(ctx context.Context, versionID, note string) (BackupVersion, error) {
	version, found, err := s.getVersionRow(ctx, versionID)
	if err != nil {
		return BackupVersion{}, err
	}
	if !found {
		return BackupVersion{}, policyError(404, "备份版本不存在。")
	}
	cleaned := strings.TrimSpace(note)
	if err := s.updateVersionNoteRow(ctx, versionID, cleaned); err != nil {
		return BackupVersion{}, err
	}
	version.Note = cleaned
	manifestPath := filepath.Join(version.LocalPath, manifestFilename)
	if raw, readErr := os.ReadFile(manifestPath); readErr == nil {
		var manifest BackupManifest
		if json.Unmarshal(raw, &manifest) == nil {
			manifest.Note = cleaned
			_ = writeManifestAtomic(manifestPath, manifest)
		}
	}
	return version, nil
}

// DeleteVersion removes one backup snapshot from SQLite and optionally from local disk.
func (s *Service) DeleteVersion(ctx context.Context, versionID string, deleteFiles bool) error {
	version, found, err := s.getVersionRow(ctx, versionID)
	if err != nil {
		return err
	}
	if !found {
		return policyError(404, "备份版本不存在。")
	}
	if version.Status == StatusRunning || !s.acquireJobRun(version.JobID, "") {
		return policyError(409, "该任务正在执行，请稍后再删除版本。")
	}
	defer s.releaseJobRun(version.JobID)
	job, jobFound, err := s.getJobRow(ctx, version.JobID)
	if err != nil {
		return err
	}
	if deleteFiles && jobFound {
		if isStrictSubPath(version.LocalPath, job.DestinationDir) {
			if rmErr := os.RemoveAll(version.LocalPath); rmErr != nil {
				return fmt.Errorf("删除本机版本目录失败: %w", rmErr)
			}
		}
	}
	return s.deleteVersionRow(ctx, versionID)
}

// DeleteJob removes a backup job and all its versions, optionally deleting local backup folders.
func (s *Service) DeleteJob(ctx context.Context, jobID string, deleteFiles bool) error {
	if !s.acquireJobRun(jobID, "") {
		return policyError(409, "该任务正在执行，请稍后再删除。")
	}
	defer s.releaseJobRun(jobID)
	job, found, err := s.getJobRow(ctx, jobID)
	if err != nil {
		return err
	}
	if !found {
		return policyError(404, "备份任务不存在。")
	}
	versions, err := s.listVersionRows(ctx, job.ID)
	if err != nil {
		return err
	}
	if deleteFiles {
		dirsToRemove := make(map[string]bool)
		slugDir := filepath.Join(job.DestinationDir, safeJobFolderName(job))
		dirsToRemove[slugDir] = true
		for _, v := range versions {
			if isStrictSubPath(v.LocalPath, job.DestinationDir) {
				dirsToRemove[v.LocalPath] = true
				parent := filepath.Dir(v.LocalPath)
				if isStrictSubPath(parent, job.DestinationDir) {
					dirsToRemove[parent] = true
				}
			}
		}
		for dir := range dirsToRemove {
			if isStrictSubPath(dir, job.DestinationDir) {
				_ = os.RemoveAll(dir)
			}
		}
	}
	return s.deleteJobAndVersionsRows(ctx, job.ID)
}

func (s *Service) pruneOldVersions(ctx context.Context, job BackupJob) {
	prunable, err := s.listPrunableVersions(ctx, job.ID, job.RetentionCount)
	if err != nil || len(prunable) == 0 {
		return
	}
	for _, oldVersion := range prunable {
		if isStrictSubPath(oldVersion.LocalPath, job.DestinationDir) {
			_ = os.RemoveAll(oldVersion.LocalPath)
		}
		if err := s.deleteVersionRow(ctx, oldVersion.ID); err != nil {
			s.logger.Warn("prune backup version failed", "version_id", oldVersion.ID, "error", err)
		}
	}
}

func isSubPathOrEqual(candidate, root string) bool {
	cClean := filepath.Clean(candidate)
	rClean := filepath.Clean(root)
	if runtime.GOOS == "windows" {
		if strings.EqualFold(cClean, rClean) {
			return true
		}
		cLower, rLower := strings.ToLower(cClean), strings.ToLower(rClean)
		rel, err := filepath.Rel(rLower, cLower)
		return err == nil && (rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))))
	}
	rel, err := filepath.Rel(rClean, cClean)
	return err == nil && (rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))))
}

func isStrictSubPath(candidate, root string) bool {
	if candidate == "" || root == "" {
		return false
	}
	cClean := filepath.Clean(candidate)
	rClean := filepath.Clean(root)
	if runtime.GOOS == "windows" && strings.EqualFold(cClean, rClean) {
		return false
	}
	if cClean == rClean {
		return false
	}
	return isSubPathOrEqual(cClean, rClean)
}
