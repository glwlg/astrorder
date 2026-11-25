package targetfs

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

const (
	maxWorktreeCopyFileBytes  = 2 * 1024 * 1024
	maxWorktreeStatusScan     = 16
	maxWorktreeIncludeEntries = 100
)

var (
	defaultWorktreeCopyCandidates = []string{
		".env",
		".env.local",
		".env.development",
		".env.development.local",
		".env.test",
		".env.test.local",
		".env.production.local",
		".npmrc",
		".yarnrc.yml",
		".tool-versions",
		".mise.local.toml",
		"docker-compose.override.yml",
		"appsettings.Development.json",
	}
	unsafeWorktreeLeafChars = regexp.MustCompile(`[:*?"<>|]+`)
)

// WorktreeEntryWire describes a single Git worktree in JSON output.
type WorktreeEntryWire struct {
	Path         string `json:"path"`
	Head         string `json:"head"`
	Branch       string `json:"branch"`
	Detached     bool   `json:"detached"`
	Bare         bool   `json:"bare"`
	IsMain       bool   `json:"is_main"`
	IsCurrent    bool   `json:"is_current"`
	Locked       bool   `json:"locked"`
	LockReason   string `json:"lock_reason,omitempty"`
	Prunable     bool   `json:"prunable"`
	PruneReason  string `json:"prune_reason,omitempty"`
	StatusLoaded bool   `json:"status_loaded"`
	Dirty        bool   `json:"dirty"`
	ChangedFiles int    `json:"changed_files"`
	Ahead        int    `json:"ahead"`
	Behind       int    `json:"behind"`
}

// WorktreeBranchWire describes a local Git branch and whether a worktree occupies it.
type WorktreeBranchWire struct {
	Name           string `json:"name"`
	IsCurrent      bool   `json:"is_current"`
	OccupiedByPath string `json:"occupied_by_path,omitempty"`
}

// WorktreeCopyCandidateWire describes a local ignored/untracked config file safe to copy.
type WorktreeCopyCandidateWire struct {
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	Source string `json:"source"` // "worktreeinclude" | "default"
}

// WorktreeRepoConfigWire represents optional .astrorder/worktree.json repo defaults.
type WorktreeRepoConfigWire struct {
	DefaultLayout string   `json:"default_layout,omitempty"`
	CopyFiles     []string `json:"copy_files,omitempty"`
	SetupCommand  string   `json:"setup_command,omitempty"`
}

// WorktreeListWire is emitted as GitResult.Stdout for GitWorktreeList.
type WorktreeListWire struct {
	IsGitRepo          bool                        `json:"is_git_repo"`
	Workspace          string                      `json:"workspace"`
	MainWorkspace      string                      `json:"main_workspace,omitempty"`
	IsWorktree         bool                        `json:"is_worktree"`
	CurrentBranch      string                      `json:"current_branch,omitempty"`
	DefaultBranch      string                      `json:"default_branch,omitempty"`
	DefaultSiblingBase string                      `json:"default_sibling_base,omitempty"`
	DefaultInsideBase  string                      `json:"default_inside_base,omitempty"`
	Worktrees          []WorktreeEntryWire         `json:"worktrees"`
	Branches           []WorktreeBranchWire        `json:"branches"`
	CopyableFiles      []WorktreeCopyCandidateWire `json:"copyable_files"`
	RepoConfig         *WorktreeRepoConfigWire     `json:"repo_config,omitempty"`
}

// WorktreeAddWire is emitted as GitResult.Stdout for GitWorktreeAdd.
type WorktreeAddWire struct {
	Path          string   `json:"path"`
	Branch        string   `json:"branch"`
	MainWorkspace string   `json:"main_workspace"`
	Head          string   `json:"head,omitempty"`
	CreatedBranch bool     `json:"created_branch"`
	CopiedFiles   []string `json:"copied_files"`
	SkippedFiles  []string `json:"skipped_files,omitempty"`
}

// WorktreeRemoveWire is emitted as GitResult.Stdout for GitWorktreeRemove.
type WorktreeRemoveWire struct {
	Removed           bool   `json:"removed"`
	Path              string `json:"path"`
	Branch            string `json:"branch,omitempty"`
	BranchDeleted     bool   `json:"branch_deleted"`
	BranchDeleteError string `json:"branch_delete_error,omitempty"`
}

// WorktreePruneWire is emitted as GitResult.Stdout for GitWorktreePrune.
type WorktreePruneWire struct {
	PrunedCount int    `json:"pruned_count"`
	Output      string `json:"output"`
}

// WorktreeMergeWire is emitted as GitResult.Stdout for GitWorktreeMerge.
type WorktreeMergeWire struct {
	Merged        bool     `json:"merged"`
	Conflicted    bool     `json:"conflicted"`
	Aborted       bool     `json:"aborted"`
	SourceBranch  string   `json:"source_branch"`
	TargetBranch  string   `json:"target_branch"`
	TargetPath    string   `json:"target_path"`
	Strategy      string   `json:"strategy"`
	ConflictFiles []string `json:"conflict_files,omitempty"`
	Detail        string   `json:"detail,omitempty"`
}

// WorktreeBootstrapWire is emitted as GitResult.Stdout for GitWorktreeBootstrap.
type WorktreeBootstrapWire struct {
	TargetPath   string   `json:"target_path"`
	CopiedFiles  []string `json:"copied_files"`
	SkippedFiles []string `json:"skipped_files,omitempty"`
}

func runWorktreeGit(ctx context.Context, gitBinary, dir string, args ...string) (GitResult, error) {
	if err := ctx.Err(); err != nil {
		return GitResult{}, err
	}
	cmd := exec.CommandContext(ctx, gitBinary, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	configureChildProcess(cmd)
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if ctxErr := ctx.Err(); ctxErr != nil {
		return GitResult{}, ctxErr
	}
	if err != nil {
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) {
			return GitResult{}, errorf(503, "git_unavailable", "Git executable is unavailable", err)
		}
	}
	return GitResult{
		ExitCode: exitCode(err),
		Stdout:   []byte(stdout.String()),
		Stderr:   []byte(stderr.String()),
	}, nil
}

func marshalGitJSON(value any) (GitResult, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return GitResult{}, errorf(500, "git_encode_failed", "Failed to encode Git result", err)
	}
	return GitResult{ExitCode: 0, Stdout: encoded}, nil
}

func gitErrDetail(res GitResult, fallback string) string {
	detail := strings.TrimSpace(string(res.Stderr))
	if detail == "" {
		detail = strings.TrimSpace(string(res.Stdout))
	}
	if detail == "" {
		detail = fallback
	}
	return detail
}

func normalizeWorktreePath(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}
	cleaned := filepath.Clean(trimmed)
	if canonical, err := filepath.EvalSymlinks(cleaned); err == nil {
		return filepath.Clean(canonical)
	}
	return cleaned
}

func parseWorktreePorcelain(output []byte, currentTop string) []WorktreeEntryWire {
	normalized := strings.ReplaceAll(string(output), "\r\n", "\n")
	blocks := strings.Split(normalized, "\n\n")
	entries := make([]WorktreeEntryWire, 0, len(blocks))
	for _, block := range blocks {
		block = strings.TrimSpace(block)
		if block == "" {
			continue
		}
		var entry WorktreeEntryWire
		for _, line := range strings.Split(block, "\n") {
			line = strings.TrimSpace(line)
			switch {
			case strings.HasPrefix(line, "worktree "):
				entry.Path = normalizeWorktreePath(strings.TrimPrefix(line, "worktree "))
			case strings.HasPrefix(line, "HEAD "):
				entry.Head = strings.TrimSpace(strings.TrimPrefix(line, "HEAD "))
			case strings.HasPrefix(line, "branch "):
				ref := strings.TrimSpace(strings.TrimPrefix(line, "branch "))
				entry.Branch = strings.TrimPrefix(ref, "refs/heads/")
			case line == "detached":
				entry.Detached = true
			case line == "bare":
				entry.Bare = true
			case line == "locked":
				entry.Locked = true
			case strings.HasPrefix(line, "locked "):
				entry.Locked = true
				entry.LockReason = strings.TrimSpace(strings.TrimPrefix(line, "locked "))
			case line == "prunable":
				entry.Prunable = true
			case strings.HasPrefix(line, "prunable "):
				entry.Prunable = true
				entry.PruneReason = strings.TrimSpace(strings.TrimPrefix(line, "prunable "))
			}
		}
		if entry.Path == "" {
			continue
		}
		if len(entries) == 0 && !entry.Bare {
			entry.IsMain = true
		}
		if currentTop != "" && samePath(entry.Path, currentTop) {
			entry.IsCurrent = true
		}
		entries = append(entries, entry)
	}
	return entries
}

func resolveGitTopAndWorktrees(ctx context.Context, gitBinary, workspace string) (topLevel string, mainRoot string, entries []WorktreeEntryWire, isGitRepo bool, err error) {
	res, runErr := runWorktreeGit(ctx, gitBinary, workspace, "rev-parse", "--show-toplevel")
	if runErr != nil {
		return "", "", nil, false, runErr
	}
	if res.ExitCode != 0 {
		return "", "", nil, false, nil
	}
	topLevel = normalizeWorktreePath(strings.TrimSpace(string(res.Stdout)))
	if topLevel == "" {
		return "", "", nil, false, nil
	}
	listRes, listErr := runWorktreeGit(ctx, gitBinary, topLevel, "worktree", "list", "--porcelain")
	if listErr != nil {
		return "", "", nil, false, listErr
	}
	if listRes.ExitCode != 0 {
		return "", "", nil, false, errorf(422, "git_worktree_list_failed", gitErrDetail(listRes, "无法读取 Git Worktree 列表"), nil)
	}
	entries = parseWorktreePorcelain(listRes.Stdout, topLevel)
	mainRoot = topLevel
	for _, entry := range entries {
		if entry.IsMain {
			mainRoot = entry.Path
			break
		}
	}
	return topLevel, mainRoot, entries, true, nil
}

func detectDefaultBranch(ctx context.Context, gitBinary, mainRoot string, entries []WorktreeEntryWire, branches []WorktreeBranchWire) string {
	// 1. Check origin/HEAD symbolic ref.
	if res, err := runWorktreeGit(ctx, gitBinary, mainRoot, "symbolic-ref", "--quiet", "--short", "refs/remotes/origin/HEAD"); err == nil && res.ExitCode == 0 {
		name := strings.TrimSpace(string(res.Stdout))
		name = strings.TrimPrefix(name, "origin/")
		if name != "" {
			for _, b := range branches {
				if b.Name == name {
					return name
				}
			}
		}
	}
	// 2. Check main worktree's current branch.
	for _, entry := range entries {
		if entry.IsMain && entry.Branch != "" {
			return entry.Branch
		}
	}
	// 3. Fallback to main / master if present.
	for _, candidate := range []string{"main", "master"} {
		for _, b := range branches {
			if b.Name == candidate {
				return candidate
			}
		}
	}
	if len(branches) > 0 {
		return branches[0].Name
	}
	return ""
}

func listLocalBranches(ctx context.Context, gitBinary, dir string, entries []WorktreeEntryWire) ([]WorktreeBranchWire, string, error) {
	res, err := runWorktreeGit(ctx, gitBinary, dir, "for-each-ref", "--format=%(refname:short)\t%(HEAD)", "refs/heads")
	if err != nil {
		return nil, "", err
	}
	occupied := make(map[string]string, len(entries))
	for _, entry := range entries {
		if entry.Branch != "" && !entry.Prunable {
			occupied[entry.Branch] = entry.Path
		}
	}
	branches := make([]WorktreeBranchWire, 0)
	currentBranch := ""
	if res.ExitCode == 0 {
		for _, line := range strings.Split(strings.ReplaceAll(string(res.Stdout), "\r\n", "\n"), "\n") {
			line = strings.TrimRight(line, "\r\n")
			if strings.TrimSpace(line) == "" {
				continue
			}
			parts := strings.SplitN(line, "\t", 2)
			name := strings.TrimSpace(parts[0])
			if name == "" {
				continue
			}
			isCurrent := len(parts) > 1 && strings.TrimSpace(parts[1]) == "*"
			if isCurrent {
				currentBranch = name
			}
			branches = append(branches, WorktreeBranchWire{
				Name:           name,
				IsCurrent:      isCurrent,
				OccupiedByPath: occupied[name],
			})
		}
	}
	sort.Slice(branches, func(i, j int) bool {
		if branches[i].IsCurrent != branches[j].IsCurrent {
			return branches[i].IsCurrent
		}
		return branches[i].Name < branches[j].Name
	})
	return branches, currentBranch, nil
}

func defaultSiblingWorktreeBase(mainRoot string) string {
	return filepath.Join(filepath.Dir(mainRoot), filepath.Base(mainRoot)+"-worktrees")
}

func defaultInsideWorktreeBase(mainRoot string) string {
	return filepath.Join(mainRoot, ".worktrees")
}

func sanitizeWorktreeLeaf(branch string) string {
	normalized := strings.ReplaceAll(strings.TrimSpace(branch), "\\", "/")
	parts := strings.Split(normalized, "/")
	cleanParts := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		part = unsafeWorktreeLeafChars.ReplaceAllString(part, "-")
		part = strings.Trim(part, ". ")
		if part == "" || part == "." || part == ".." {
			continue
		}
		cleanParts = append(cleanParts, part)
	}
	if len(cleanParts) == 0 {
		return "worktree"
	}
	return filepath.Join(cleanParts...)
}

func canonicalPendingPath(raw string) (string, error) {
	if strings.ContainsRune(raw, 0) {
		return "", errorf(400, "invalid_worktree_path", "Worktree 路径不合法", nil)
	}
	absolute, err := filepath.Abs(filepath.Clean(raw))
	if err != nil {
		return "", errorf(400, "invalid_worktree_path", "Worktree 路径不合法", err)
	}
	current := absolute
	remainder := make([]string, 0)
	for {
		info, statErr := os.Lstat(current)
		if statErr == nil {
			if info.Mode()&os.ModeSymlink != 0 {
				return "", errorf(403, "worktree_symlink_forbidden", "Worktree 路径不能包含符号链接", nil)
			}
			break
		}
		if !os.IsNotExist(statErr) {
			return "", errorf(400, "invalid_worktree_path", "Worktree 路径不可访问", statErr)
		}
		parent := filepath.Dir(current)
		if samePath(parent, current) {
			return "", errorf(404, "worktree_parent_not_found", "Worktree 父目录不存在", statErr)
		}
		remainder = append(remainder, filepath.Base(current))
		current = parent
	}
	canonical, err := filepath.EvalSymlinks(current)
	if err != nil {
		return "", errorf(400, "invalid_worktree_path", "Worktree 路径解析失败", err)
	}
	for i := len(remainder) - 1; i >= 0; i-- {
		canonical = filepath.Join(canonical, remainder[i])
	}
	return filepath.Clean(canonical), nil
}

func resolveWorktreeTargetPath(mainRoot, requestedPath, layout, branch string, allowedRoots []string) (targetPath string, isInside bool, err error) {
	siblingBase, err := canonicalPendingPath(defaultSiblingWorktreeBase(mainRoot))
	if err != nil {
		return "", false, err
	}
	insideBase, err := canonicalPendingPath(defaultInsideWorktreeBase(mainRoot))
	if err != nil {
		return "", false, err
	}
	raw := strings.TrimSpace(requestedPath)
	if raw == "" {
		leaf := sanitizeWorktreeLeaf(branch)
		if strings.EqualFold(strings.TrimSpace(layout), "sibling") {
			raw = filepath.Join(siblingBase, leaf)
		} else {
			raw = filepath.Join(insideBase, leaf)
		}
	} else if !filepath.IsAbs(raw) {
		raw = filepath.Join(mainRoot, raw)
	}
	canonical, err := canonicalPendingPath(raw)
	if err != nil {
		return "", false, err
	}
	if samePath(canonical, mainRoot) || samePath(canonical, siblingBase) || samePath(canonical, insideBase) {
		return "", false, errorf(400, "invalid_worktree_path", "Worktree 路径必须是独立的子目录", nil)
	}
	permitted := within(canonical, siblingBase) || within(canonical, insideBase)
	if !permitted {
		for _, root := range allowedRoots {
			if within(canonical, root) && !samePath(canonical, root) {
				permitted = true
				break
			}
		}
	}
	if !permitted {
		return "", false, errorf(403, "worktree_path_out_of_scope", "Worktree 路径超出允许范围（需位于 <repo>-worktrees/、<repo>/.worktrees/ 或允许工作区内）", nil)
	}
	if info, statErr := os.Lstat(canonical); statErr == nil {
		if !info.IsDir() {
			return "", false, errorf(409, "worktree_path_exists", "目标 Worktree 路径已存在且不是目录", nil)
		}
		entries, readErr := os.ReadDir(canonical)
		if readErr != nil || len(entries) > 0 {
			return "", false, errorf(409, "worktree_path_not_empty", "目标 Worktree 目录已存在且非空，请换一个目录或分支名", readErr)
		}
	}
	return canonical, within(canonical, insideBase), nil
}

func ensureLocalGitInfoExclude(ctx context.Context, gitBinary, mainRoot, pattern string) error {
	res, err := runWorktreeGit(ctx, gitBinary, mainRoot, "rev-parse", "--git-common-dir")
	if err != nil || res.ExitCode != 0 {
		return err
	}
	commonDir := strings.TrimSpace(string(res.Stdout))
	if commonDir == "" {
		return nil
	}
	if !filepath.IsAbs(commonDir) {
		commonDir = filepath.Join(mainRoot, commonDir)
	}
	infoDir := filepath.Join(commonDir, "info")
	if err := os.MkdirAll(infoDir, 0o755); err != nil {
		return err
	}
	excludeFile := filepath.Join(infoDir, "exclude")
	existingBytes, _ := os.ReadFile(excludeFile)
	existing := strings.ReplaceAll(string(existingBytes), "\r\n", "\n")
	for _, line := range strings.Split(existing, "\n") {
		if strings.TrimSpace(line) == pattern {
			return nil
		}
	}
	var next strings.Builder
	next.WriteString(existing)
	if len(existing) > 0 && !strings.HasSuffix(existing, "\n") {
		next.WriteByte('\n')
	}
	next.WriteString(pattern)
	next.WriteByte('\n')
	return os.WriteFile(excludeFile, []byte(next.String()), 0o644)
}

func readWorktreeRepoConfig(mainRoot string) *WorktreeRepoConfigWire {
	cfgPath := filepath.Join(mainRoot, ".astrorder", "worktree.json")
	info, err := os.Lstat(cfgPath)
	if err != nil || !info.Mode().IsRegular() || info.Size() > 64*1024 {
		return nil
	}
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		return nil
	}
	var cfg WorktreeRepoConfigWire
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil
	}
	return &cfg
}

func cleanRelativeConfigPath(raw string) (string, bool) {
	trimmed := strings.TrimSpace(strings.ReplaceAll(raw, "\\", "/"))
	if trimmed == "" || strings.HasPrefix(trimmed, "/") || strings.ContainsRune(trimmed, 0) {
		return "", false
	}
	clean := filepath.ToSlash(filepath.Clean(trimmed))
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") || strings.HasPrefix(clean, ".git/") || clean == ".git" {
		return "", false
	}
	return clean, true
}

func isGitTracked(ctx context.Context, gitBinary, repoRoot, relSlash string) bool {
	res, err := runWorktreeGit(ctx, gitBinary, repoRoot, "ls-files", "--error-unmatch", "--", relSlash)
	return err == nil && res.ExitCode == 0
}

func discoverCopyableIgnoredFiles(ctx context.Context, gitBinary, sourceRoot, mainRoot string) []WorktreeCopyCandidateWire {
	seen := make(map[string]bool)
	result := make([]WorktreeCopyCandidateWire, 0)
	addCandidate := func(relRaw, source string) {
		rel, ok := cleanRelativeConfigPath(relRaw)
		if !ok || seen[rel] {
			return
		}
		abs := filepath.Join(sourceRoot, filepath.FromSlash(rel))
		info, err := os.Lstat(abs)
		if err != nil {
			if !samePath(sourceRoot, mainRoot) {
				abs = filepath.Join(mainRoot, filepath.FromSlash(rel))
				info, err = os.Lstat(abs)
			}
			if err != nil {
				return
			}
		}
		if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() > maxWorktreeCopyFileBytes {
			return
		}
		// Skip files already tracked by Git (git worktree add already checks them out).
		if isGitTracked(ctx, gitBinary, mainRoot, rel) {
			return
		}
		seen[rel] = true
		result = append(result, WorktreeCopyCandidateWire{
			Path:   rel,
			Size:   info.Size(),
			Source: source,
		})
	}

	// 1. Repo-defined .worktreeinclude (exact relative paths or simple root globs).
	includePath := filepath.Join(mainRoot, ".worktreeinclude")
	if info, err := os.Lstat(includePath); err == nil && info.Mode().IsRegular() && info.Size() <= 64*1024 {
		if f, openErr := os.Open(includePath); openErr == nil {
			defer f.Close()
			scanner := bufio.NewScanner(f)
			count := 0
			for scanner.Scan() && count < maxWorktreeIncludeEntries {
				line := strings.TrimSpace(scanner.Text())
				if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "!") {
					continue
				}
				count++
				if strings.ContainsAny(line, "*?[") {
					matches, _ := filepath.Glob(filepath.Join(sourceRoot, filepath.FromSlash(line)))
					for _, match := range matches {
						if rel, relErr := filepath.Rel(sourceRoot, match); relErr == nil {
							addCandidate(rel, "worktreeinclude")
						}
					}
				} else {
					addCandidate(line, "worktreeinclude")
				}
			}
		}
	}

	// 2. Repo .astrorder/worktree.json copy_files.
	if repoCfg := readWorktreeRepoConfig(mainRoot); repoCfg != nil {
		for _, item := range repoCfg.CopyFiles {
			addCandidate(item, "worktreeinclude")
		}
	}

	// 3. Common local ignored config files.
	for _, candidate := range defaultWorktreeCopyCandidates {
		addCandidate(candidate, "default")
	}

	return result
}

func copySafeWorktreeFiles(sourceRoot, mainRoot, targetRoot string, files []string) (copied []string, skipped []string) {
	copied = make([]string, 0)
	skipped = make([]string, 0)
	seen := make(map[string]bool)
	for _, raw := range files {
		rel, ok := cleanRelativeConfigPath(raw)
		if !ok || seen[rel] {
			continue
		}
		seen[rel] = true
		srcPath := filepath.Join(sourceRoot, filepath.FromSlash(rel))
		info, err := os.Lstat(srcPath)
		if err != nil && !samePath(sourceRoot, mainRoot) {
			srcPath = filepath.Join(mainRoot, filepath.FromSlash(rel))
			info, err = os.Lstat(srcPath)
		}
		if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() > maxWorktreeCopyFileBytes {
			skipped = append(skipped, rel)
			continue
		}
		dstPath := filepath.Join(targetRoot, filepath.FromSlash(rel))
		if !within(dstPath, targetRoot) {
			skipped = append(skipped, rel)
			continue
		}
		if mkErr := os.MkdirAll(filepath.Dir(dstPath), 0o755); mkErr != nil {
			skipped = append(skipped, rel)
			continue
		}
		if copyErr := copyRegularFile(srcPath, dstPath, info.Mode().Perm()); copyErr != nil {
			skipped = append(skipped, rel)
			continue
		}
		copied = append(copied, rel)
	}
	return copied, skipped
}

func copyRegularFile(srcPath, dstPath string, mode os.FileMode) error {
	src, err := os.Open(srcPath)
	if err != nil {
		return err
	}
	defer src.Close()
	if mode == 0 {
		mode = 0o644
	}
	dst, err := os.OpenFile(dstPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(dst, io.LimitReader(src, maxWorktreeCopyFileBytes+1)); err != nil {
		_ = dst.Close()
		_ = os.Remove(dstPath)
		return err
	}
	return dst.Close()
}

func countPorcelainChangedFiles(output []byte) int {
	if len(output) == 0 {
		return 0
	}
	count := 0
	fields := strings.Split(string(output), "\x00")
	for i := 0; i < len(fields); i++ {
		f := fields[i]
		if len(f) < 3 {
			continue
		}
		count++
		if f[0] == 'R' || f[0] == 'C' {
			i++
		}
	}
	return count
}

func listLocalWorktrees(ctx context.Context, gitBinary, workspace string, includeStatuses bool) (GitResult, error) {
	topLevel, mainRoot, entries, isGitRepo, err := resolveGitTopAndWorktrees(ctx, gitBinary, workspace)
	if err != nil {
		return GitResult{}, err
	}
	if !isGitRepo {
		return marshalGitJSON(WorktreeListWire{
			IsGitRepo:     false,
			Workspace:     workspace,
			Worktrees:     []WorktreeEntryWire{},
			Branches:      []WorktreeBranchWire{},
			CopyableFiles: []WorktreeCopyCandidateWire{},
		})
	}
	branches, currentBranch, err := listLocalBranches(ctx, gitBinary, topLevel, entries)
	if err != nil {
		return GitResult{}, err
	}
	defaultBranch := detectDefaultBranch(ctx, gitBinary, mainRoot, entries, branches)

	if includeStatuses {
		scanned := 0
		for i := range entries {
			if entries[i].Bare || entries[i].Prunable {
				continue
			}
			if scanned >= maxWorktreeStatusScan && !entries[i].IsMain && !entries[i].IsCurrent {
				continue
			}
			info, statErr := os.Stat(entries[i].Path)
			if statErr != nil || !info.IsDir() {
				entries[i].Prunable = true
				entries[i].PruneReason = "directory missing"
				continue
			}
			scanned++
			if stRes, stErr := runWorktreeGit(ctx, gitBinary, entries[i].Path, "status", "--porcelain=v1", "-z", "--untracked-files=all"); stErr == nil && stRes.ExitCode == 0 {
				changed := countPorcelainChangedFiles(stRes.Stdout)
				entries[i].ChangedFiles = changed
				entries[i].Dirty = changed > 0
				entries[i].StatusLoaded = true
			}
			if entries[i].Branch != "" && defaultBranch != "" && entries[i].Branch != defaultBranch {
				if abRes, abErr := runWorktreeGit(ctx, gitBinary, entries[i].Path, "rev-list", "--left-right", "--count", defaultBranch+"..."+entries[i].Branch); abErr == nil && abRes.ExitCode == 0 {
					parts := strings.Fields(string(abRes.Stdout))
					if len(parts) >= 2 {
						entries[i].Behind, _ = strconv.Atoi(parts[0])
						entries[i].Ahead, _ = strconv.Atoi(parts[1])
					}
				}
			}
		}
	}

	copyable := discoverCopyableIgnoredFiles(ctx, gitBinary, topLevel, mainRoot)
	repoCfg := readWorktreeRepoConfig(mainRoot)

	return marshalGitJSON(WorktreeListWire{
		IsGitRepo:          true,
		Workspace:          topLevel,
		MainWorkspace:      mainRoot,
		IsWorktree:         !samePath(topLevel, mainRoot),
		CurrentBranch:      currentBranch,
		DefaultBranch:      defaultBranch,
		DefaultSiblingBase: defaultSiblingWorktreeBase(mainRoot),
		DefaultInsideBase:  defaultInsideWorktreeBase(mainRoot),
		Worktrees:          entries,
		Branches:           branches,
		CopyableFiles:      copyable,
		RepoConfig:         repoCfg,
	})
}

func addLocalWorktree(ctx context.Context, gitBinary, workspace string, allowedRoots []string, request GitRequest) (GitResult, error) {
	topLevel, mainRoot, entries, isGitRepo, err := resolveGitTopAndWorktrees(ctx, gitBinary, workspace)
	if err != nil {
		return GitResult{}, err
	}
	if !isGitRepo {
		return GitResult{}, errorf(422, "not_a_git_repo", "当前工作区不是 Git 仓库，无法创建 Worktree", nil)
	}
	branch := strings.TrimSpace(request.Branch)
	if branch == "" {
		return GitResult{}, errorf(400, "branch_required", "分支名称不能为空", nil)
	}
	checkRes, err := runWorktreeGit(ctx, gitBinary, mainRoot, "check-ref-format", "--branch", branch)
	if err != nil {
		return GitResult{}, err
	}
	if checkRes.ExitCode != 0 {
		return GitResult{}, errorf(400, "invalid_branch", fmt.Sprintf("分支名称 %q 不符合 Git 规范", branch), nil)
	}
	for _, entry := range entries {
		if entry.Branch == branch && !entry.Prunable {
			return GitResult{}, errorf(409, "branch_already_used", fmt.Sprintf("分支 %q 已被其他 Worktree（%s）占用", branch, entry.Path), nil)
		}
	}
	targetPath, isInside, err := resolveWorktreeTargetPath(mainRoot, request.TargetPath, request.Layout, branch, allowedRoots)
	if err != nil {
		return GitResult{}, err
	}
	if isInside {
		_ = ensureLocalGitInfoExclude(ctx, gitBinary, mainRoot, ".worktrees/")
	}
	if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
		return GitResult{}, errorf(500, "worktree_mkdir_failed", "无法创建 Worktree 父目录", err)
	}

	args := []string{"worktree", "add"}
	if request.Create {
		args = append(args, "-b", branch, targetPath)
		if baseRef := strings.TrimSpace(request.BaseBranch); baseRef != "" {
			verifyRes, verifyErr := runWorktreeGit(ctx, gitBinary, mainRoot, "rev-parse", "--verify", "--quiet", baseRef+"^{commit}")
			if verifyErr != nil {
				return GitResult{}, verifyErr
			}
			if verifyRes.ExitCode != 0 {
				return GitResult{}, errorf(400, "invalid_base_ref", fmt.Sprintf("基线分支或提交 %q 不存在", baseRef), nil)
			}
			args = append(args, baseRef)
		}
	} else {
		verifyRes, verifyErr := runWorktreeGit(ctx, gitBinary, mainRoot, "show-ref", "--verify", "--quiet", "refs/heads/"+branch)
		if verifyErr != nil {
			return GitResult{}, verifyErr
		}
		if verifyRes.ExitCode != 0 {
			return GitResult{}, errorf(404, "branch_not_found", fmt.Sprintf("本地分支 %q 不存在", branch), nil)
		}
		args = append(args, targetPath, branch)
	}

	addRes, err := runWorktreeGit(ctx, gitBinary, mainRoot, args...)
	if err != nil {
		return GitResult{}, err
	}
	if addRes.ExitCode != 0 {
		return GitResult{}, errorf(400, "git_worktree_add_failed", gitErrDetail(addRes, "创建 Git Worktree 失败"), nil)
	}

	head := ""
	if headRes, headErr := runWorktreeGit(ctx, gitBinary, targetPath, "rev-parse", "HEAD"); headErr == nil && headRes.ExitCode == 0 {
		head = strings.TrimSpace(string(headRes.Stdout))
	}
	copied, skipped := copySafeWorktreeFiles(topLevel, mainRoot, targetPath, request.CopyFiles)

	return marshalGitJSON(WorktreeAddWire{
		Path:          targetPath,
		Branch:        branch,
		MainWorkspace: mainRoot,
		Head:          head,
		CreatedBranch: request.Create,
		CopiedFiles:   copied,
		SkippedFiles:  skipped,
	})
}

func removeLocalWorktree(ctx context.Context, gitBinary, workspace string, request GitRequest) (GitResult, error) {
	_, mainRoot, entries, isGitRepo, err := resolveGitTopAndWorktrees(ctx, gitBinary, workspace)
	if err != nil {
		return GitResult{}, err
	}
	if !isGitRepo {
		return GitResult{}, errorf(422, "not_a_git_repo", "当前工作区不是 Git 仓库", nil)
	}
	requested := normalizeWorktreePath(request.TargetPath)
	if requested == "" {
		return GitResult{}, errorf(400, "target_path_required", "需要指定要移除的 Worktree 路径", nil)
	}
	var matched *WorktreeEntryWire
	for i := range entries {
		if samePath(entries[i].Path, requested) {
			matched = &entries[i]
			break
		}
	}
	if matched == nil {
		return GitResult{}, errorf(404, "worktree_not_found", "未在当前仓库中找到该 Worktree", nil)
	}
	if matched.IsMain {
		return GitResult{}, errorf(403, "cannot_remove_main_worktree", "禁止移除主工作区目录", nil)
	}

	// Safety check: refuse removing dirty worktree unless Force is set.
	if !request.Force && !matched.Prunable {
		if info, statErr := os.Stat(matched.Path); statErr == nil && info.IsDir() {
			if stRes, stErr := runWorktreeGit(ctx, gitBinary, matched.Path, "status", "--porcelain=v1", "-z", "--untracked-files=all"); stErr == nil && stRes.ExitCode == 0 {
				if countPorcelainChangedFiles(stRes.Stdout) > 0 {
					return GitResult{}, errorf(409, "worktree_dirty", "该 Worktree 存在未提交的修改，请先提交/暂存或勾选强制移除", nil)
				}
			}
		}
	}

	args := []string{"worktree", "remove"}
	if request.Force {
		args = append(args, "--force")
	}
	args = append(args, matched.Path)
	rmRes, err := runWorktreeGit(ctx, gitBinary, mainRoot, args...)
	if err != nil {
		return GitResult{}, err
	}
	if rmRes.ExitCode != 0 {
		// If directory was already gone, prune and succeed cleanly.
		if _, statErr := os.Stat(matched.Path); os.IsNotExist(statErr) {
			_, _ = runWorktreeGit(ctx, gitBinary, mainRoot, "worktree", "prune")
		} else {
			return GitResult{}, errorf(400, "git_worktree_remove_failed", gitErrDetail(rmRes, "移除 Git Worktree 失败"), nil)
		}
	}

	branchDeleted := false
	branchDeleteErr := ""
	if request.DeleteBranch && matched.Branch != "" {
		branches, _, _ := listLocalBranches(ctx, gitBinary, mainRoot, entries)
		defaultBranch := detectDefaultBranch(ctx, gitBinary, mainRoot, entries, branches)
		if matched.Branch == defaultBranch {
			branchDeleteErr = fmt.Sprintf("保护默认分支 %s，未删除", defaultBranch)
		} else {
			flag := "-d"
			if request.Force {
				flag = "-D"
			}
			delRes, delErr := runWorktreeGit(ctx, gitBinary, mainRoot, "branch", flag, matched.Branch)
			if delErr != nil {
				branchDeleteErr = delErr.Error()
			} else if delRes.ExitCode != 0 {
				branchDeleteErr = gitErrDetail(delRes, "分支尚未合并，未自动删除")
			} else {
				branchDeleted = true
			}
		}
	}

	return marshalGitJSON(WorktreeRemoveWire{
		Removed:           true,
		Path:              matched.Path,
		Branch:            matched.Branch,
		BranchDeleted:     branchDeleted,
		BranchDeleteError: branchDeleteErr,
	})
}

func pruneLocalWorktrees(ctx context.Context, gitBinary, workspace string) (GitResult, error) {
	_, mainRoot, _, isGitRepo, err := resolveGitTopAndWorktrees(ctx, gitBinary, workspace)
	if err != nil {
		return GitResult{}, err
	}
	if !isGitRepo {
		return GitResult{}, errorf(422, "not_a_git_repo", "当前工作区不是 Git 仓库", nil)
	}
	res, err := runWorktreeGit(ctx, gitBinary, mainRoot, "worktree", "prune", "--verbose")
	if err != nil {
		return GitResult{}, err
	}
	if res.ExitCode != 0 {
		return GitResult{}, errorf(400, "git_worktree_prune_failed", gitErrDetail(res, "清理 Worktree 记录失败"), nil)
	}
	out := strings.TrimSpace(string(res.Stdout) + "\n" + string(res.Stderr))
	pruned := 0
	for _, line := range strings.Split(strings.ReplaceAll(out, "\r\n", "\n"), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "Removing worktrees/") {
			pruned++
		}
	}
	return marshalGitJSON(WorktreePruneWire{
		PrunedCount: pruned,
		Output:      out,
	})
}

func mergeLocalWorktree(ctx context.Context, gitBinary, workspace string, request GitRequest) (GitResult, error) {
	_, mainRoot, entries, isGitRepo, err := resolveGitTopAndWorktrees(ctx, gitBinary, workspace)
	if err != nil {
		return GitResult{}, err
	}
	if !isGitRepo {
		return GitResult{}, errorf(422, "not_a_git_repo", "当前工作区不是 Git 仓库", nil)
	}
	sourceBranch := strings.TrimSpace(request.Branch)
	if sourceBranch == "" {
		return GitResult{}, errorf(400, "branch_required", "需要指定要合并的来源分支", nil)
	}
	branches, _, err := listLocalBranches(ctx, gitBinary, mainRoot, entries)
	if err != nil {
		return GitResult{}, err
	}
	targetBranch := strings.TrimSpace(request.BaseBranch)
	if targetBranch == "" {
		targetBranch = detectDefaultBranch(ctx, gitBinary, mainRoot, entries, branches)
	}
	if targetBranch == "" {
		return GitResult{}, errorf(400, "target_branch_required", "无法确定目标分支", nil)
	}
	if sourceBranch == targetBranch {
		return GitResult{}, errorf(400, "same_branch_merge", "来源分支与目标分支相同，无需合并", nil)
	}

	// Verify target branch is checked out in a worktree (preferring main worktree).
	targetPath := ""
	for _, entry := range entries {
		if entry.Branch == targetBranch && !entry.Prunable {
			targetPath = entry.Path
			break
		}
	}
	if targetPath == "" {
		return GitResult{}, errorf(409, "target_branch_not_checked_out", fmt.Sprintf("目标分支 %q 当前未在任何工作区检出，请先将主工作区切换到 %q 后再合并", targetBranch, targetBranch), nil)
	}

	// Check target worktree is clean.
	targetStatus, err := runWorktreeGit(ctx, gitBinary, targetPath, "status", "--porcelain=v1", "-z", "--untracked-files=all")
	if err != nil {
		return GitResult{}, err
	}
	if targetStatus.ExitCode == 0 && countPorcelainChangedFiles(targetStatus.Stdout) > 0 {
		return GitResult{}, errorf(409, "target_dirty", fmt.Sprintf("目标工作区（%s）存在未提交的修改，请先提交或暂存后再合并", targetPath), nil)
	}

	// Check source worktree is clean (unless force).
	if !request.Force {
		for _, entry := range entries {
			if entry.Branch == sourceBranch && !entry.Prunable {
				if stRes, stErr := runWorktreeGit(ctx, gitBinary, entry.Path, "status", "--porcelain=v1", "-z", "--untracked-files=all"); stErr == nil && stRes.ExitCode == 0 && countPorcelainChangedFiles(stRes.Stdout) > 0 {
					return GitResult{}, errorf(409, "source_dirty", fmt.Sprintf("来源 Worktree（%s）还有未提交修改，Git 合并只包含已提交的 commit；请先提交变更或勾选忽略未提交修改", entry.Path), nil)
				}
				break
			}
		}
	}

	strategy := strings.TrimSpace(request.MergeStrategy)
	if strategy == "" {
		strategy = "merge"
	}
	var mergeArgs []string
	switch strategy {
	case "merge":
		mergeArgs = []string{"merge", "--no-ff", "--no-edit", sourceBranch}
	case "ff_only":
		mergeArgs = []string{"merge", "--ff-only", sourceBranch}
	case "squash":
		mergeArgs = []string{"merge", "--squash", sourceBranch}
	default:
		return GitResult{}, errorf(400, "invalid_merge_strategy", "不支持的合并策略: "+strategy, nil)
	}

	var prefixArgs []string
	if nameRes, _ := runWorktreeGit(ctx, gitBinary, targetPath, "config", "--get", "user.name"); nameRes.ExitCode != 0 || strings.TrimSpace(string(nameRes.Stdout)) == "" {
		prefixArgs = append(prefixArgs, "-c", "user.name=Astrorder")
	}
	if emailRes, _ := runWorktreeGit(ctx, gitBinary, targetPath, "config", "--get", "user.email"); emailRes.ExitCode != 0 || strings.TrimSpace(string(emailRes.Stdout)) == "" {
		prefixArgs = append(prefixArgs, "-c", "user.email=worktree@astrorder.local")
	}

	mergeRes, err := runWorktreeGit(ctx, gitBinary, targetPath, append(append([]string{}, prefixArgs...), mergeArgs...)...)
	if err != nil {
		return GitResult{}, err
	}
	if mergeRes.ExitCode != 0 {
		conflictFiles := []string{}
		if cRes, cErr := runWorktreeGit(ctx, gitBinary, targetPath, "diff", "--name-only", "--diff-filter=U"); cErr == nil && cRes.ExitCode == 0 {
			for _, line := range strings.Split(strings.ReplaceAll(string(cRes.Stdout), "\r\n", "\n"), "\n") {
				if trimmed := strings.TrimSpace(line); trimmed != "" {
					conflictFiles = append(conflictFiles, trimmed)
				}
			}
		}
		aborted := false
		if strategy == "squash" {
			if rRes, rErr := runWorktreeGit(ctx, gitBinary, targetPath, "reset", "--merge"); rErr == nil && rRes.ExitCode == 0 {
				aborted = true
			}
		} else {
			if aRes, aErr := runWorktreeGit(ctx, gitBinary, targetPath, "merge", "--abort"); aErr == nil && aRes.ExitCode == 0 {
				aborted = true
			}
		}
		return marshalGitJSON(WorktreeMergeWire{
			Merged:        false,
			Conflicted:    len(conflictFiles) > 0 || strings.Contains(strings.ToLower(string(mergeRes.Stdout)+string(mergeRes.Stderr)), "conflict"),
			Aborted:       aborted,
			SourceBranch:  sourceBranch,
			TargetBranch:  targetBranch,
			TargetPath:    targetPath,
			Strategy:      strategy,
			ConflictFiles: conflictFiles,
			Detail:        gitErrDetail(mergeRes, "合并失败，已自动回滚目标工作区"),
		})
	}

	if strategy == "squash" {
		// Commit the squashed changes cleanly if anything was staged.
		stRes, _ := runWorktreeGit(ctx, gitBinary, targetPath, "status", "--porcelain=v1", "-z")
		if countPorcelainChangedFiles(stRes.Stdout) > 0 {
			commitRes, commitErr := runWorktreeGit(ctx, gitBinary, targetPath, append(append([]string{}, prefixArgs...), "commit", "-m", fmt.Sprintf("Squash merge branch '%s' into %s", sourceBranch, targetBranch))...)
			if commitErr != nil {
				return GitResult{}, commitErr
			}
			if commitRes.ExitCode != 0 {
				_, _ = runWorktreeGit(ctx, gitBinary, targetPath, "reset", "--merge")
				return GitResult{}, errorf(400, "git_squash_commit_failed", gitErrDetail(commitRes, "Squash 提交失败，已回滚工作区"), nil)
			}
		}
	}

	return marshalGitJSON(WorktreeMergeWire{
		Merged:       true,
		SourceBranch: sourceBranch,
		TargetBranch: targetBranch,
		TargetPath:   targetPath,
		Strategy:     strategy,
		Detail:       strings.TrimSpace(string(mergeRes.Stdout)),
	})
}

func bootstrapLocalWorktree(ctx context.Context, gitBinary, workspace string, request GitRequest) (GitResult, error) {
	topLevel, mainRoot, entries, isGitRepo, err := resolveGitTopAndWorktrees(ctx, gitBinary, workspace)
	if err != nil {
		return GitResult{}, err
	}
	if !isGitRepo {
		return GitResult{}, errorf(422, "not_a_git_repo", "当前工作区不是 Git 仓库", nil)
	}
	requested := normalizeWorktreePath(request.TargetPath)
	if requested == "" {
		requested = topLevel
	}
	var matched *WorktreeEntryWire
	for i := range entries {
		if samePath(entries[i].Path, requested) {
			matched = &entries[i]
			break
		}
	}
	if matched == nil || matched.IsMain {
		return GitResult{}, errorf(400, "invalid_bootstrap_target", "只能向当前仓库的独立 Worktree 同步配置文件", nil)
	}
	files := request.CopyFiles
	if len(files) == 0 {
		candidates := discoverCopyableIgnoredFiles(ctx, gitBinary, mainRoot, mainRoot)
		for _, c := range candidates {
			files = append(files, c.Path)
		}
	}
	copied, skipped := copySafeWorktreeFiles(mainRoot, mainRoot, matched.Path, files)
	return marshalGitJSON(WorktreeBootstrapWire{
		TargetPath:   matched.Path,
		CopiedFiles:  copied,
		SkippedFiles: skipped,
	})
}
