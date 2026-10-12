package targetfs

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
)

var treeIgnored = map[string]bool{
	".git": true, ".venv": true, "node_modules": true, "__pycache__": true,
	".pytest_cache": true, ".ruff_cache": true, "dist": true,
}

var searchIgnored = map[string]bool{
	".git": true, ".venv": true, "venv": true, "node_modules": true,
	"__pycache__": true, ".pytest_cache": true, ".ruff_cache": true,
	"dist": true, "build": true, ".next": true, ".turbo": true,
	"target": true, ".cargo": true, "coverage": true, ".idea": true, ".vscode": true,
}

type LocalOptions struct {
	Workspace    string
	AllowedRoots []string
	GitBinary    string
}

type localFileSystem struct {
	workspace string
	roots     []string
	gitBinary string
}

func NewLocal(options LocalOptions) (FileSystem, error) {
	workspace := strings.TrimSpace(options.Workspace)
	if workspace == "" {
		return nil, errorf(422, "workspace_required", "Workspace is required", nil)
	}
	resolvedWorkspace, err := canonicalExistingDirectory(workspace)
	if err != nil {
		return nil, errorf(422, "invalid_workspace", "Workspace is not an existing directory", err)
	}
	roots := make([]string, 0, len(options.AllowedRoots)+1)
	for _, raw := range options.AllowedRoots {
		if strings.TrimSpace(raw) == "" {
			continue
		}
		root, rootErr := canonicalExistingDirectory(raw)
		if rootErr != nil {
			return nil, errorf(422, "invalid_allowed_workspace", "An allowed workspace is not an existing directory", rootErr)
		}
		roots = appendUniquePath(roots, root)
	}
	if len(roots) == 0 {
		roots = append(roots, resolvedWorkspace)
	} else if !withinAny(resolvedWorkspace, roots) {
		return nil, errorf(403, "workspace_out_of_scope", "Workspace is outside allowed workspaces", nil)
	}
	gitBinary := strings.TrimSpace(options.GitBinary)
	if gitBinary == "" {
		gitBinary = "git"
	}
	return &localFileSystem{workspace: resolvedWorkspace, roots: roots, gitBinary: gitBinary}, nil
}

func canonicalExistingDirectory(raw string) (string, error) {
	absolute, err := filepath.Abs(filepath.Clean(raw))
	if err != nil {
		return "", err
	}
	canonical, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(canonical)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", fmt.Errorf("%s is not a directory", raw)
	}
	return filepath.Clean(canonical), nil
}

func appendUniquePath(paths []string, candidate string) []string {
	for _, existing := range paths {
		if samePath(existing, candidate) {
			return paths
		}
	}
	return append(paths, candidate)
}

func samePath(left, right string) bool {
	if runtime.GOOS == "windows" {
		return strings.EqualFold(filepath.Clean(left), filepath.Clean(right))
	}
	return filepath.Clean(left) == filepath.Clean(right)
}

func within(candidate, root string) bool {
	relative, err := filepath.Rel(root, candidate)
	if err != nil {
		return false
	}
	return relative == "." || (relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)))
}

func withinAny(candidate string, roots []string) bool {
	for _, root := range roots {
		if within(candidate, root) {
			return true
		}
	}
	return false
}

func (l *localFileSystem) existingPath(raw string) (string, error) {
	candidate, err := l.absolutePath(raw)
	if err != nil {
		return "", err
	}
	canonical, err := filepath.EvalSymlinks(candidate)
	if err != nil {
		if os.IsNotExist(err) {
			return "", errorf(404, "path_not_found", "Path does not exist", err)
		}
		return "", errorf(400, "invalid_path", "Invalid file path", err)
	}
	if !withinAny(canonical, l.roots) {
		return "", errorf(403, "path_out_of_scope", "Path is outside allowed workspaces", nil)
	}
	return filepath.Clean(canonical), nil
}

func (l *localFileSystem) writablePath(raw string) (string, error) {
	candidate, err := l.absolutePath(raw)
	if err != nil {
		return "", err
	}
	current := candidate
	remainder := make([]string, 0)
	for {
		_, statErr := os.Lstat(current)
		if statErr == nil {
			break
		}
		if !os.IsNotExist(statErr) {
			return "", errorf(400, "invalid_path", "Invalid file path", statErr)
		}
		parent := filepath.Dir(current)
		if samePath(parent, current) {
			return "", errorf(404, "path_parent_not_found", "Path parent does not exist", statErr)
		}
		remainder = append(remainder, filepath.Base(current))
		current = parent
	}
	canonical, err := filepath.EvalSymlinks(current)
	if err != nil {
		return "", errorf(400, "invalid_path", "Invalid file path", err)
	}
	for i := len(remainder) - 1; i >= 0; i-- {
		canonical = filepath.Join(canonical, remainder[i])
	}
	canonical = filepath.Clean(canonical)
	if !withinAny(canonical, l.roots) {
		return "", errorf(403, "path_out_of_scope", "Path is outside allowed workspaces", nil)
	}
	return canonical, nil
}

func (l *localFileSystem) absolutePath(raw string) (string, error) {
	if strings.ContainsRune(raw, 0) {
		return "", errorf(400, "invalid_path", "Invalid file path", nil)
	}
	candidate := strings.TrimSpace(raw)
	if candidate == "" {
		candidate = l.workspace
	} else if !filepath.IsAbs(candidate) {
		candidate = filepath.Join(l.workspace, candidate)
	}
	absolute, err := filepath.Abs(filepath.Clean(candidate))
	if err != nil {
		return "", errorf(400, "invalid_path", "Invalid file path", err)
	}
	return absolute, nil
}

func (l *localFileSystem) Read(ctx context.Context, path string) (*ReadResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	resolved, err := l.existingPath(path)
	if err != nil {
		return nil, err
	}
	file, err := os.Open(resolved)
	if err != nil {
		return nil, errorf(500, "read_failed", "Failed to read file", err)
	}
	info, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, errorf(500, "stat_failed", "Failed to inspect file", err)
	}
	if !info.Mode().IsRegular() {
		file.Close()
		return nil, errorf(404, "file_not_found", "File was not found", nil)
	}
	contentType := mime.TypeByExtension(filepath.Ext(info.Name()))
	if contentType == "" {
		probe := make([]byte, 512)
		length, readErr := file.Read(probe)
		if readErr != nil && !errors.Is(readErr, io.EOF) {
			file.Close()
			return nil, errorf(500, "read_failed", "Failed to read file", readErr)
		}
		contentType = http.DetectContentType(probe[:length])
		if _, seekErr := file.Seek(0, io.SeekStart); seekErr != nil {
			file.Close()
			return nil, errorf(500, "read_failed", "Failed to read file", seekErr)
		}
	}
	return &ReadResult{Info: portableInfo(resolved, info), ContentType: contentType, Reader: file}, nil
}

func (l *localFileSystem) WriteAtomic(ctx context.Context, path string, source io.Reader, mode fs.FileMode) (Info, error) {
	if err := ctx.Err(); err != nil {
		return Info{}, err
	}
	resolved, err := l.writablePath(path)
	if err != nil {
		return Info{}, err
	}
	parent := filepath.Dir(resolved)
	parentResolved, err := l.writablePath(parent)
	if err != nil {
		return Info{}, err
	}
	if err := os.MkdirAll(parentResolved, 0o755); err != nil {
		return Info{}, errorf(500, "mkdir_failed", "Failed to create file parent", err)
	}
	if canonicalParent, evalErr := filepath.EvalSymlinks(parentResolved); evalErr != nil || !withinAny(canonicalParent, l.roots) {
		return Info{}, errorf(403, "path_out_of_scope", "Path is outside allowed workspaces", evalErr)
	}
	temporary, err := os.CreateTemp(parentResolved, ".astrorder-write-*")
	if err != nil {
		return Info{}, errorf(500, "write_failed", "Failed to write file", err)
	}
	temporaryName := temporary.Name()
	published := false
	defer func() {
		_ = temporary.Close()
		if !published {
			_ = os.Remove(temporaryName)
		}
	}()
	if mode == 0 {
		mode = 0o600
	}
	if err := temporary.Chmod(mode.Perm()); err != nil {
		return Info{}, errorf(500, "write_failed", "Failed to write file", err)
	}
	if _, err := copyContext(ctx, temporary, source); err != nil {
		return Info{}, statusError(err, 500, "write_failed", "Failed to write file")
	}
	if err := temporary.Sync(); err != nil {
		return Info{}, errorf(500, "write_failed", "Failed to write file", err)
	}
	if err := temporary.Close(); err != nil {
		return Info{}, errorf(500, "write_failed", "Failed to write file", err)
	}
	if err := os.Rename(temporaryName, resolved); err != nil {
		return Info{}, errorf(500, "write_failed", "Failed to publish file", err)
	}
	published = true
	info, err := os.Stat(resolved)
	if err != nil {
		return Info{}, errorf(500, "stat_failed", "Failed to inspect file", err)
	}
	return portableInfo(resolved, info), nil
}

func copyContext(ctx context.Context, destination io.Writer, source io.Reader) (int64, error) {
	buffer := make([]byte, 128*1024)
	var total int64
	for {
		if err := ctx.Err(); err != nil {
			return total, err
		}
		length, readErr := source.Read(buffer)
		if length > 0 {
			written, writeErr := destination.Write(buffer[:length])
			total += int64(written)
			if writeErr != nil {
				return total, writeErr
			}
			if written != length {
				return total, io.ErrShortWrite
			}
		}
		if errors.Is(readErr, io.EOF) {
			return total, nil
		}
		if readErr != nil {
			return total, readErr
		}
	}
}

func (l *localFileSystem) Stat(ctx context.Context, path string) (Info, error) {
	if err := ctx.Err(); err != nil {
		return Info{}, err
	}
	resolved, err := l.existingPath(path)
	if err != nil {
		return Info{}, err
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return Info{}, errorf(500, "stat_failed", "Failed to inspect path", err)
	}
	return portableInfo(resolved, info), nil
}

func portableInfo(path string, info fs.FileInfo) Info {
	return Info{Name: info.Name(), Path: path, IsDir: info.IsDir(), Size: info.Size(), Mode: info.Mode(), ModTime: info.ModTime().UTC()}
}

func clampDepth(depth int) int {
	if depth < 1 {
		return 1
	}
	if depth > 8 {
		return 8
	}
	return depth
}

func (l *localFileSystem) List(ctx context.Context, request ListRequest) (ListResult, error) {
	root, err := l.existingPath(request.Path)
	if err != nil {
		return ListResult{}, err
	}
	info, err := os.Stat(root)
	if err != nil {
		return ListResult{}, errorf(500, "stat_failed", "Failed to inspect path", err)
	}
	if !info.IsDir() {
		if request.RevealPath == "" {
			request.RevealPath = root
		}
		root = filepath.Dir(root)
	}
	var reveal string
	if request.RevealPath != "" {
		reveal, err = l.existingPath(request.RevealPath)
		if err != nil {
			return ListResult{}, err
		}
		if !within(reveal, root) {
			return ListResult{}, errorf(403, "reveal_out_of_scope", "Reveal path is outside the requested root", nil)
		}
	}
	items, truncated, err := l.walkTree(ctx, root, clampDepth(request.Depth), reveal)
	if err != nil {
		return ListResult{}, err
	}
	var parent *string
	if value := filepath.Dir(root); !samePath(value, root) && withinAny(value, l.roots) {
		parent = &value
	}
	return ListResult{Root: root, Name: filepath.Base(root), Parent: parent, Drives: localDrives(l.roots), Items: items, Truncated: truncated}, nil
}

// maxTreeEntries bounds one listing so a huge workspace cannot exceed the
// helper protocol header or freeze the browser. Directories are expanded level
// by level, so what is cut is always the deepest part.
const maxTreeEntries = 3000

func sortTreeRows(rows []os.DirEntry) {
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].IsDir() != rows[j].IsDir() {
			return rows[i].IsDir()
		}
		left, right := strings.ToLower(rows[i].Name()), strings.ToLower(rows[j].Name())
		if left == right {
			return rows[i].Name() < rows[j].Name()
		}
		return left < right
	})
}

// readTreeDir lists one directory. An entry that cannot be resolved (a broken
// symlink) or that resolves outside the allowed roots is still listed, as a
// leaf that is never followed, so one odd entry cannot fail the whole tree.
func (l *localFileSystem) readTreeDir(current string) ([]Entry, error) {
	rows, err := os.ReadDir(current)
	if err != nil {
		return nil, err
	}
	sortTreeRows(rows)
	items := make([]Entry, 0, len(rows))
	for _, row := range rows {
		if treeIgnored[row.Name()] {
			continue
		}
		path := filepath.Join(current, row.Name())
		item := Entry{Name: row.Name(), Path: path}
		if row.Type()&(fs.ModeSymlink|fs.ModeIrregular) != 0 {
			canonical, evalErr := filepath.EvalSymlinks(path)
			if evalErr != nil || !withinAny(canonical, l.roots) {
				items = append(items, item)
				continue
			}
		}
		info, infoErr := row.Info()
		if infoErr != nil {
			items = append(items, item)
			continue
		}
		item.IsDir = info.IsDir()
		if !item.IsDir {
			item.Size = info.Size()
		}
		items = append(items, item)
	}
	return items, nil
}

func (l *localFileSystem) walkTree(ctx context.Context, root string, depth int, reveal string) ([]Entry, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	if depth <= 0 && (reveal == "" || (!samePath(root, reveal) && !within(reveal, root))) {
		return []Entry{}, false, nil
	}
	top, err := l.readTreeDir(root)
	if err != nil {
		return nil, false, errorf(500, "list_failed", "Failed to read directory", err)
	}
	remaining := maxTreeEntries - len(top)
	truncated := false
	type pending struct {
		entry *Entry
		depth int
	}
	var queue []pending
	enqueue := func(items []Entry, childDepth int) {
		for i := range items {
			if items[i].IsDir {
				queue = append(queue, pending{entry: &items[i], depth: childDepth})
			}
		}
	}
	enqueue(top, depth-1)
	for head := 0; head < len(queue); head++ {
		if err := ctx.Err(); err != nil {
			return nil, false, err
		}
		node := queue[head]
		onReveal := reveal != "" && (samePath(node.entry.Path, reveal) || within(reveal, node.entry.Path))
		if node.depth <= 0 && !onReveal {
			empty := []Entry{}
			node.entry.Children = &empty
			continue
		}
		if remaining <= 0 && !onReveal {
			truncated = true // Children stays unset: the client loads it on demand.
			continue
		}
		children, readErr := l.readTreeDir(node.entry.Path)
		if readErr != nil {
			children = []Entry{} // an unreadable subdirectory shows as empty
		}
		remaining -= len(children)
		node.entry.Children = &children
		enqueue(children, node.depth-1)
	}
	return top, truncated, nil
}

func localDrives(roots []string) []string {
	if runtime.GOOS != "windows" {
		return []string{}
	}
	drives := make([]string, 0)
	seen := make(map[string]bool)
	for _, root := range roots {
		volume := filepath.VolumeName(root)
		if volume == "" {
			continue
		}
		drive := volume + `\`
		key := strings.ToLower(drive)
		if !seen[key] {
			seen[key] = true
			drives = append(drives, drive)
		}
	}
	sort.Strings(drives)
	return drives
}

func (l *localFileSystem) Mkdir(ctx context.Context, request MkdirRequest) (Info, error) {
	if err := ctx.Err(); err != nil {
		return Info{}, err
	}
	name := strings.TrimSpace(request.Name)
	if name == "" || name == "." || name == ".." || strings.ContainsAny(name, "/\\\x00") {
		return Info{}, errorf(400, "invalid_directory_name", "文件夹名称不合法。", nil)
	}
	parent, err := l.existingPath(request.Parent)
	if err != nil {
		return Info{}, err
	}
	parentInfo, err := os.Stat(parent)
	if err != nil || !parentInfo.IsDir() {
		return Info{}, errorf(400, "invalid_parent", "父级路径不合法。", err)
	}
	target, err := l.writablePath(filepath.Join(parent, name))
	if err != nil {
		return Info{}, err
	}
	if err := os.MkdirAll(target, 0o755); err != nil {
		return Info{}, errorf(400, "mkdir_failed", "创建文件夹失败", err)
	}
	return l.Stat(ctx, target)
}

func (l *localFileSystem) Search(ctx context.Context, request SearchRequest) (SearchResult, error) {
	root, err := l.existingPath(request.Root)
	if err != nil {
		return SearchResult{}, err
	}
	limit := request.Limit
	if limit < 1 {
		limit = 1
	}
	if limit > 100 {
		limit = 100
	}
	query := strings.ToLower(strings.TrimSpace(request.Query))
	items := make([]SearchItem, 0, limit)
	err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if path == root {
			return nil
		}
		name := entry.Name()
		if entry.IsDir() {
			if searchIgnored[name] || strings.HasPrefix(name, ".") {
				return filepath.SkipDir
			}
			canonical, evalErr := filepath.EvalSymlinks(path)
			if evalErr != nil {
				return evalErr
			}
			if !withinAny(canonical, l.roots) {
				return errorf(403, "path_out_of_scope", "Search encountered a path outside allowed workspaces", nil)
			}
			return nil
		}
		if strings.HasPrefix(name, ".") || (query != "" && !strings.Contains(strings.ToLower(name), query)) {
			return nil
		}
		if len(items) >= limit {
			return fs.SkipAll
		}
		relative, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		items = append(items, SearchItem{Name: name, Path: filepath.ToSlash(path), RelativePath: filepath.ToSlash(relative)})
		return nil
	})
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return SearchResult{}, err
		}
		var target *Error
		if errors.As(err, &target) {
			return SearchResult{}, target
		}
		return SearchResult{}, errorf(500, "search_failed", "Failed to search files", err)
	}
	return SearchResult{Items: items, Root: filepath.ToSlash(root)}, nil
}

func (l *localFileSystem) Stage(ctx context.Context, request StageRequest) (StageResult, error) {
	if len(request.Paths) == 0 {
		return StageResult{}, errorf(400, "paths_required", "At least one path is required", nil)
	}
	if strings.TrimSpace(request.Destination) == "" {
		result := make([]string, len(request.Paths))
		for index, raw := range request.Paths {
			resolved, err := l.existingPath(raw)
			if err != nil {
				return StageResult{}, err
			}
			result[index] = resolved
		}
		return StageResult{Paths: result}, nil
	}
	destination, err := l.writablePath(request.Destination)
	if err != nil {
		return StageResult{}, err
	}
	if err := os.MkdirAll(destination, 0o700); err != nil {
		return StageResult{}, errorf(500, "stage_failed", "Failed to create staging directory", err)
	}
	sources := make([]string, len(request.Paths))
	names := make(map[string]bool)
	for i, raw := range request.Paths {
		source, sourceErr := l.existingPath(raw)
		if sourceErr != nil {
			return StageResult{}, sourceErr
		}
		name := filepath.Base(source)
		key := name
		if runtime.GOOS == "windows" {
			key = strings.ToLower(key)
		}
		if names[key] {
			return StageResult{}, errorf(409, "stage_name_conflict", "Staged paths have duplicate filenames", nil)
		}
		names[key] = true
		sources[i] = source
	}
	temporary, err := os.MkdirTemp(destination, ".astrorder-stage-*")
	if err != nil {
		return StageResult{}, errorf(500, "stage_failed", "Failed to prepare staging directory", err)
	}
	defer os.RemoveAll(temporary)
	for _, source := range sources {
		if err := copyTree(ctx, source, filepath.Join(temporary, filepath.Base(source)), l.roots); err != nil {
			return StageResult{}, statusError(err, 500, "stage_failed", "Failed to stage files")
		}
	}
	result := make([]string, 0, len(sources))
	for _, source := range sources {
		name := filepath.Base(source)
		final := filepath.Join(destination, name)
		if _, statErr := os.Lstat(final); statErr == nil {
			return StageResult{}, errorf(409, "stage_target_exists", "A staged target already exists", nil)
		} else if !os.IsNotExist(statErr) {
			return StageResult{}, errorf(500, "stage_failed", "Failed to inspect staging target", statErr)
		}
		if err := os.Rename(filepath.Join(temporary, name), final); err != nil {
			return StageResult{}, errorf(500, "stage_failed", "Failed to publish staged files", err)
		}
		result = append(result, final)
	}
	return StageResult{Paths: result}, nil
}

func copyTree(ctx context.Context, source, destination string, roots []string) error {
	info, err := os.Stat(source)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		input, err := os.Open(source)
		if err != nil {
			return err
		}
		defer input.Close()
		output, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, info.Mode().Perm())
		if err != nil {
			return err
		}
		_, copyErr := copyContext(ctx, output, input)
		closeErr := output.Close()
		if copyErr != nil {
			return copyErr
		}
		return closeErr
	}
	if err := os.Mkdir(destination, info.Mode().Perm()); err != nil {
		return err
	}
	entries, err := os.ReadDir(source)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		childSource := filepath.Join(source, entry.Name())
		canonical, err := filepath.EvalSymlinks(childSource)
		if err != nil {
			return err
		}
		if !withinAny(canonical, roots) {
			return errorf(403, "path_out_of_scope", "Staged directory contains a path outside allowed workspaces", nil)
		}
		if err := copyTree(ctx, childSource, filepath.Join(destination, entry.Name()), roots); err != nil {
			return err
		}
	}
	return nil
}

func cleanGitPath(value string) (string, error) {
	if strings.ContainsRune(value, 0) || filepath.IsAbs(value) || (len(value) >= 2 && value[1] == ':') || strings.HasPrefix(value, `/`) || strings.HasPrefix(value, `\\`) {
		return "", errorf(400, "invalid_git_path", "Git path must be relative to the workspace", nil)
	}
	clean := filepath.Clean(value)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) || strings.Contains(clean, `\\`) {
		return "", errorf(400, "invalid_git_path", "Git path must be relative to the workspace", nil)
	}
	return filepath.ToSlash(clean), nil
}

func (l *localFileSystem) ExecGit(ctx context.Context, request GitRequest) (GitResult, error) {
	workspace, err := l.existingPath(request.Workspace)
	if err != nil {
		return GitResult{}, err
	}
	if request.Path != "" {
		request.Path, err = cleanGitPath(request.Path)
		if err != nil {
			return GitResult{}, err
		}
	}
	info, err := os.Stat(workspace)
	if err != nil || !info.IsDir() {
		return GitResult{}, errorf(422, "invalid_workspace", "无法读取该工作区的 Git 状态；请确认会话工作区目录。", err)
	}
	if request.Operation == GitSnapshotReadonly {
		return captureLocalSnapshot(ctx, l.gitBinary, workspace, l.roots)
	}
	args, err := gitArgs(request)
	if err != nil {
		return GitResult{}, err
	}
	if request.Operation == GitCheckout {
		check := exec.CommandContext(ctx, l.gitBinary, "check-ref-format", "--branch", request.Branch)
		check.Dir = workspace
		if output, checkErr := check.CombinedOutput(); checkErr != nil {
			return GitResult{ExitCode: exitCode(checkErr), Stderr: output}, nil
		}
	}
	command := exec.CommandContext(ctx, l.gitBinary, args...)
	command.Dir = workspace
	var stdout, stderr strings.Builder
	command.Stdout = &stdout
	command.Stderr = &stderr
	err = command.Run()
	if ctxErr := ctx.Err(); ctxErr != nil {
		return GitResult{}, ctxErr
	}
	if err != nil {
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) {
			return GitResult{}, errorf(503, "git_unavailable", "Git executable is unavailable", err)
		}
	}
	return GitResult{ExitCode: exitCode(err), Stdout: []byte(stdout.String()), Stderr: []byte(stderr.String())}, nil
}

func gitArgs(request GitRequest) ([]string, error) {
	switch request.Operation {
	case GitBranches:
		return []string{"branch", "-a", "--no-color"}, nil
	case GitStatus:
		return []string{"status", "--porcelain=v1", "-z", "--untracked-files=all"}, nil
	case GitNumstat:
		args := []string{"diff"}
		if request.Cached {
			args = append(args, "--cached")
		}
		args = append(args, "--numstat", "-z")
		return args, nil
	case GitAheadBehind:
		if request.BaseBranch == "" || request.Branch == "" {
			return nil, errorf(400, "git_revision_required", "Git base and branch are required", nil)
		}
		return []string{"rev-list", "--left-right", "--count", request.BaseBranch + "..." + request.Branch}, nil
	case GitDiff:
		args := []string{"diff"}
		if request.Cached {
			args = append(args, "--cached")
		}
		if request.BaseBranch != "" {
			args = append(args, request.BaseBranch+"...HEAD")
		}
		if request.Path != "" {
			args = append(args, "--", request.Path)
		}
		return args, nil
	case GitPathStatus:
		if request.Path == "" {
			return nil, errorf(400, "git_path_required", "Git path is required", nil)
		}
		return []string{"status", "--porcelain=v1", "-z", "--", request.Path}, nil
	case GitCheckout:
		if strings.TrimSpace(request.Branch) == "" {
			return nil, errorf(400, "branch_required", "branch name is required", nil)
		}
		if request.Create {
			return []string{"checkout", "-b", request.Branch}, nil
		}
		return []string{"checkout", request.Branch}, nil
	default:
		return nil, errorf(400, "unsupported_git_operation", "Unsupported Git operation", nil)
	}
}

func exitCode(err error) int {
	if err == nil {
		return 0
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode()
	}
	return -1
}

var _ = strconv.IntSize
