package backups

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"astrorder.dev/app-server/internal/targetfs"
)

type restorePlan struct {
	VersionID                   string              `json:"version_id"`
	ConnectionID                string              `json:"connection_id"`
	TargetPath                  string              `json:"target_path"`
	SourceKind                  string              `json:"source_kind"`
	PostRestoreCommand          string              `json:"post_restore_command"`
	PostRestoreRestartAgent     *RestoreAgentTarget `json:"post_restore_restart_agent,omitempty"`
	RestoreAgentEnvironmentName string              `json:"restore_agent_environment_name,omitempty"`
	HookShell                   string              `json:"hook_shell"`
	Confirmation                string              `json:"confirmation"`
}

type restoreResult struct {
	FileCount  int    `json:"file_count"`
	HookStatus string `json:"hook_status"`
	HookError  string `json:"hook_error,omitempty"`
}

func (s *Service) handleRestoreVersion(response http.ResponseWriter, request *http.Request, versionID string) {
	ctx := request.Context()
	switch request.Method {
	case http.MethodGet:
		plan, _, err := s.loadRestorePlan(ctx, versionID)
		if err != nil {
			writeServiceError(response, err)
			return
		}
		response.Header().Set("Cache-Control", "no-store")
		writeJSON(response, http.StatusOK, plan)
	case http.MethodPost:
		var payload struct {
			Confirmation string `json:"confirmation"`
		}
		if err := decodeJSONBody(response, request, &payload, false); err != nil {
			writeServiceError(response, err)
			return
		}
		result, err := s.restoreVersion(ctx, versionID, payload.Confirmation)
		if err != nil {
			writeServiceError(response, err)
			return
		}
		writeJSON(response, http.StatusOK, result)
	default:
		methodNotAllowed(response, http.MethodGet, http.MethodPost)
	}
}

func (s *Service) loadRestorePlan(ctx context.Context, versionID string) (restorePlan, BackupVersion, error) {
	version, found, err := s.getVersionRow(ctx, versionID)
	if err != nil {
		return restorePlan{}, version, err
	}
	if !found {
		return restorePlan{}, version, policyError(404, "备份版本不存在。")
	}
	if version.Status != StatusSuccess || !version.ExistsOnDisk || version.ManifestSHA256 == nil {
		return restorePlan{}, version, policyError(409, "该版本没有可还原的备份内容。")
	}
	job, found, err := s.getJobRow(ctx, version.JobID)
	if err != nil {
		return restorePlan{}, version, err
	}
	if !found {
		return restorePlan{}, version, policyError(404, "备份任务不存在。")
	}
	connectionID, sourcePath := version.ConnectionID, version.SourcePath
	if connectionID == "" && sourcePath == "" {
		// Versions created before restore was supported recorded their target
		// in the manifest. Never substitute the job's possibly edited target.
		manifest, err := readRestoreManifest(version)
		if err != nil {
			return restorePlan{}, version, err
		}
		connectionID, sourcePath = manifest.ConnectionID, manifest.SourcePath
	}
	if connectionID == "" {
		return restorePlan{}, version, policyError(422, "备份版本未记录原服务器。")
	}
	target, err := ValidateSourcePath(connectionID, sourcePath)
	if err != nil {
		return restorePlan{}, version, err
	}
	plan := restorePlan{
		VersionID: version.ID, ConnectionID: connectionID, TargetPath: target,
		SourceKind: version.SourceKind, PostRestoreCommand: job.PostRestoreCommand,
		PostRestoreRestartAgent: job.PostRestoreRestartAgent,
		HookShell:               "/bin/sh",
	}
	if agent := plan.PostRestoreRestartAgent; agent != nil {
		plan.RestoreAgentEnvironmentName = agent.EnvironmentID
		if agent.EnvironmentID == targetfs.LocalConnectionID {
			plan.RestoreAgentEnvironmentName = "本机"
		} else {
			environment, err := s.store.GetSSHConnection(ctx, agent.EnvironmentID)
			if err != nil {
				return restorePlan{}, version, err
			}
			if name, _ := environment["display_name"].(string); name != "" {
				plan.RestoreAgentEnvironmentName = name
			}
		}
	}
	if connectionID == targetfs.LocalConnectionID && runtime.GOOS == "windows" {
		plan.HookShell = "Windows PowerShell"
	}
	encoded, err := json.Marshal(plan)
	if err != nil {
		return restorePlan{}, version, err
	}
	sum := sha256.Sum256(encoded)
	plan.Confirmation = hex.EncodeToString(sum[:])
	return plan, version, nil
}

func readRestoreManifest(version BackupVersion) (BackupManifest, error) {
	var manifest BackupManifest
	invalid := policyError(409, "历史版本的备份清单缺失或不匹配，无法确认原路径。")
	root, err := os.OpenRoot(version.LocalPath)
	if err != nil {
		return manifest, invalid
	}
	defer root.Close()
	info, err := root.Lstat(manifestFilename)
	if err != nil || !info.Mode().IsRegular() || info.Size() > 16<<20 {
		return manifest, invalid
	}
	file, err := root.Open(manifestFilename)
	if err != nil {
		return manifest, invalid
	}
	defer file.Close()
	if err := json.NewDecoder(io.LimitReader(file, 16<<20)).Decode(&manifest); err != nil ||
		manifest.VersionID != version.ID || manifest.JobID != version.JobID ||
		manifest.SourceKind != version.SourceKind || manifest.ManifestSHA256 != *version.ManifestSHA256 {
		return manifest, invalid
	}
	return manifest, nil
}

func (s *Service) restoreVersion(ctx context.Context, versionID, confirmation string) (restoreResult, error) {
	version, found, err := s.getVersionRow(ctx, versionID)
	if err != nil {
		return restoreResult{}, err
	}
	if !found {
		return restoreResult{}, policyError(404, "备份版本不存在。")
	}
	if !s.acquireJobRun(version.JobID, versionID) {
		return restoreResult{}, policyError(409, "该任务正在备份、还原或更新，请稍后再试。")
	}
	defer s.releaseJobRun(version.JobID)
	plan, version, err := s.loadRestorePlan(ctx, versionID)
	if err != nil {
		return restoreResult{}, err
	}
	if plan.Confirmation != confirmation {
		return restoreResult{}, policyError(409, "还原目标或命令需要重新确认，请关闭窗口后重试。")
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Minute)
	defer cancel()
	if err := s.restoreSnapshot(ctx, version, plan); err != nil {
		var validation *ValidationError
		if errors.As(err, &validation) {
			return restoreResult{}, err
		}
		return restoreResult{}, policyError(500, fmt.Sprintf("还原未完成，目标可能已写入部分文件：%v", err))
	}

	result := restoreResult{FileCount: version.FileCount, HookStatus: "skipped"}
	if plan.PostRestoreCommand != "" || plan.PostRestoreRestartAgent != nil {
		hookDir := plan.TargetPath
		if plan.SourceKind == "file" {
			hookDir = plan.paths().dir(hookDir)
		}
		result.HookStatus = "success"
		if err := s.runRestoreHook(ctx, plan, hookDir); err != nil {
			result.HookStatus = "failed"
			result.HookError = err.Error()
		}
	}
	return result, nil
}

type restorePaths struct {
	base func(string) string
	dir  func(string) string
	join func(...string) string
}

func (plan restorePlan) paths() restorePaths {
	if plan.ConnectionID == targetfs.LocalConnectionID {
		return restorePaths{filepath.Base, filepath.Dir, filepath.Join}
	}
	return restorePaths{path.Base, path.Dir, path.Join}
}

func (plan restorePlan) samePath(actual, expected string) bool {
	if plan.ConnectionID == targetfs.LocalConnectionID && runtime.GOOS == "windows" {
		return strings.EqualFold(actual, expected)
	}
	return actual == expected
}

func (s *Service) restoreSnapshot(ctx context.Context, version BackupVersion, plan restorePlan) error {
	paths := plan.paths()
	local := plan.ConnectionID == targetfs.LocalConnectionID
	parent := paths.dir(plan.TargetPath)
	rootPath := "/"
	if local {
		if !filepath.IsAbs(plan.TargetPath) {
			return policyError(422, "还原目标不是当前系统的本机绝对路径。")
		}
		rootPath = filepath.VolumeName(plan.TargetPath) + string(filepath.Separator)
		versionPath, err := filepath.EvalSymlinks(version.LocalPath)
		if err != nil {
			return err
		}
		repository := filepath.Dir(versionPath)
		if isSubPathOrEqual(repository, plan.TargetPath) || isSubPathOrEqual(plan.TargetPath, repository) {
			return policyError(422, "还原路径和备份版本目录重叠。")
		}
	}
	content, err := openSnapshotContent(version.LocalPath)
	if err != nil {
		return err
	}
	defer content.Close()
	entries, files, bytes, digest, err := scanSnapshot(ctx, content.FS())
	if err != nil {
		return policyError(409, fmt.Sprintf("无法校验备份内容：%v", err))
	}
	base := paths.base(plan.TargetPath)
	if !snapshotMatches(version, entries, files, bytes, digest) {
		return policyError(409, "备份内容与版本记录不匹配，无法还原。")
	}
	// Remote directory export may use the canonical source basename if the
	// original source was a link. Always map the snapshot root to the saved path.
	snapshotBase := entries[0].rel
	if strings.ContainsAny(snapshotBase, "/\r\n\x00") {
		return policyError(409, "备份内容的根路径不合法。")
	}
	targets := make([]string, 0, len(entries))
	seen := make(map[string]bool, len(entries))
	for _, entry := range entries {
		if entry.rel != snapshotBase && !strings.HasPrefix(entry.rel, snapshotBase+"/") {
			return policyError(409, "备份内容包含多个根路径。")
		}
		target := plan.TargetPath
		if rel, ok := strings.CutPrefix(entry.rel, snapshotBase+"/"); ok {
			for _, name := range strings.Split(rel, "/") {
				if !validRestoreName(name, local) {
					return policyError(422, fmt.Sprintf("无法还原文件名 %q。", name))
				}
				target = paths.join(target, name)
			}
		}
		key := target
		if local && runtime.GOOS == "windows" {
			key = strings.ToLower(key)
		}
		if seen[key] {
			return policyError(422, "备份中的多个文件会写入同一个目标路径。")
		}
		seen[key] = true
		targets = append(targets, target)
	}
	if !validRestoreName(base, local) {
		return policyError(422, "原路径的文件名不合法。")
	}
	probe, err := s.restoreFileSystem(ctx, plan, rootPath)
	if err != nil {
		return err
	}
	var missingParents []string
	for {
		present, err := checkRestoreTarget(ctx, probe, plan, parent, true)
		if err != nil {
			return err
		}
		if present {
			break
		}
		missingParents = append(missingParents, parent)
		next := paths.dir(parent)
		if next == parent {
			return policyError(409, "找不到还原目标所在的目录。")
		}
		parent = next
	}
	filesystem, err := s.restoreFileSystem(ctx, plan, parent)
	if err != nil {
		return err
	}
	for i, target := range targets {
		if _, err := checkRestoreTarget(ctx, filesystem, plan, target, entries[i].mode.IsDir()); err != nil {
			return err
		}
	}
	for i := len(missingParents) - 1; i >= 0; i-- {
		if err := makeRestoreDir(ctx, filesystem, plan, missingParents[i]); err != nil {
			return err
		}
	}
	for i, target := range targets {
		entry := entries[i]
		if entry.mode.IsDir() {
			if err := makeRestoreDir(ctx, filesystem, plan, target); err != nil {
				return err
			}
			continue
		}
		if _, err := checkRestoreTarget(ctx, filesystem, plan, target, false); err != nil {
			return fmt.Errorf("%s: %w", target, err)
		}
		file, err := content.Open(filepath.FromSlash(entry.rel))
		if err != nil {
			return err
		}
		fileInfo, err := file.Stat()
		if err != nil || !fileInfo.Mode().IsRegular() || fileInfo.Size() != entry.size {
			_ = file.Close()
			return fmt.Errorf("备份文件 %q 在还原过程中发生变化", entry.rel)
		}
		reader := &snapshotReader{source: file, remaining: entry.size, hash: sha256.New(), expected: entry.hash}
		info, writeErr := targetfs.WriteBackupFile(ctx, filesystem, target, reader, entry.mode.Perm(), entry.size)
		_ = file.Close()
		if writeErr != nil {
			return fmt.Errorf("写入 %q 失败: %w", target, writeErr)
		}
		if !plan.samePath(info.Path, target) {
			return fmt.Errorf("写入路径与预期不一致: %s", target)
		}
	}
	return nil
}

func openSnapshotContent(versionPath string) (*os.Root, error) {
	versionRoot, err := os.OpenRoot(versionPath)
	if err != nil {
		return nil, err
	}
	defer versionRoot.Close()
	info, err := versionRoot.Lstat("content")
	if err != nil || !info.IsDir() {
		return nil, policyError(409, "备份内容目录缺失或不合法。")
	}
	return versionRoot.OpenRoot("content")
}

func snapshotMatches(version BackupVersion, entries []snapshotEntry, files int, bytes int64, digest string) bool {
	if version.ManifestSHA256 == nil || digest != *version.ManifestSHA256 ||
		files != version.FileCount || bytes != version.TotalBytes || len(entries) == 0 {
		return false
	}
	switch version.SourceKind {
	case "file":
		return len(entries) == 1 && !entries[0].mode.IsDir()
	case "directory":
		return entries[0].mode.IsDir()
	default:
		return false
	}
}

func validRestoreName(name string, local bool) bool {
	return name != "" && name != "." && name != ".." && !strings.ContainsAny(name, "/\r\n\x00") &&
		(!local || filepath.IsLocal(name)) && (!local || runtime.GOOS != "windows" ||
		!strings.ContainsAny(name, `\:<>|"?*`) && !strings.HasSuffix(name, ".") && !strings.HasSuffix(name, " "))
}

func (s *Service) restoreFileSystem(ctx context.Context, plan restorePlan, root string) (targetfs.FileSystem, error) {
	if plan.ConnectionID == targetfs.LocalConnectionID {
		return targetfs.NewLocal(targetfs.LocalOptions{Workspace: root, RestorePaths: true})
	}
	if s.resolver == nil {
		return nil, policyError(503, "远程文件系统解析器不可用。")
	}
	resolved, err := s.resolver.ResolveBackupRestore(ctx, plan.ConnectionID, root)
	return resolved.FileSystem, err
}

func checkRestoreTarget(ctx context.Context, filesystem targetfs.FileSystem, plan restorePlan, target string, dir bool) (bool, error) {
	info, err := filesystem.Stat(ctx, target)
	if err != nil {
		var failure *targetfs.Error
		if errors.As(err, &failure) && failure.Code == "path_not_found" {
			return false, nil
		}
		return false, err
	}
	if !plan.samePath(info.Path, target) || (dir && !info.IsDir) || (!dir && !info.Mode.IsRegular()) {
		return false, policyError(409, fmt.Sprintf("还原路径 %q 包含链接或与备份的文件类型冲突。", target))
	}
	return true, nil
}

func makeRestoreDir(ctx context.Context, filesystem targetfs.FileSystem, plan restorePlan, target string) error {
	present, err := checkRestoreTarget(ctx, filesystem, plan, target, true)
	if err != nil || present {
		return err
	}
	paths := plan.paths()
	name := paths.base(target)
	if !validRestoreName(name, plan.ConnectionID == targetfs.LocalConnectionID) {
		return fmt.Errorf("还原目录的名称不合法: %s", target)
	}
	info, err := filesystem.Mkdir(ctx, targetfs.MkdirRequest{Parent: paths.dir(target), Name: name})
	if err != nil {
		return err
	}
	if !plan.samePath(info.Path, target) {
		return fmt.Errorf("还原目录与预期路径不一致: %s", target)
	}
	return nil
}

type contextReader struct {
	ctx    context.Context
	source io.Reader
}

func (r *contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.source.Read(p)
}

// Withhold the final block until its checksum has been verified. The remote
// helper requires the full byte count before it publishes the temporary file.
type snapshotReader struct {
	source    io.Reader
	remaining int64
	hash      hash.Hash
	expected  string
}

func (r *snapshotReader) Read(p []byte) (int, error) {
	if r.remaining == 0 {
		return 0, io.EOF
	}
	if int64(len(p)) > r.remaining {
		p = p[:r.remaining]
	}
	n, err := io.ReadFull(r.source, p)
	if err != nil {
		return 0, fmt.Errorf("备份文件在还原过程中被截断: %w", err)
	}
	r.remaining -= int64(n)
	_, _ = r.hash.Write(p[:n])
	if r.remaining == 0 && hex.EncodeToString(r.hash.Sum(nil)) != r.expected {
		return 0, errors.New("备份文件在还原过程中发生变化")
	}
	return n, nil
}
